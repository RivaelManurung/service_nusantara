// Package server wires every dependency together and owns the HTTP lifecycle.
//
// All construction happens here, in one readable place, instead of each route
// file reaching for globals to build its own repository and use case the way
// the previous routes/*.go files did.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/config"
	"service_nusantara/internal/middleware"
	"service_nusantara/internal/modules/banner"
	"service_nusantara/internal/modules/cashier"
	"service_nusantara/internal/modules/customeraddress"
	"service_nusantara/internal/modules/event"
	"service_nusantara/internal/modules/health"
	"service_nusantara/internal/modules/notification"
	"service_nusantara/internal/modules/product"
	"service_nusantara/internal/modules/role"
	"service_nusantara/internal/modules/shop"
	"service_nusantara/internal/modules/typeproduct"
	"service_nusantara/internal/modules/user"
	"service_nusantara/internal/modules/voucher"
	"service_nusantara/internal/platform/storage"
)

// apiPrefix is the single place the API version is declared.
const apiPrefix = "/api/v1"

// corsMaxAge tells browsers how long a preflight result may be cached.
const corsMaxAge = 12 * time.Hour

// Server owns the http.Server and the shutdown sequence.
type Server struct {
	http *http.Server
	log  *slog.Logger
	cfg  config.Config
}

// New builds the router and the configured http.Server.
func New(cfg config.Config, db *gorm.DB, rdb *redis.Client, log *slog.Logger) *Server {
	tokens := auth.NewManager(cfg.Auth)
	sessions := auth.NewSessionStore(rdb)
	hasher := auth.NewHasher(cfg.Auth.BcryptCost)

	userRepo := user.NewGormRepository(db)

	deps := user.Deps{
		Repo:               userRepo,
		Profiles:           userRepo,
		Identities:         user.NewGormIdentityRepository(db),
		Tokens:             tokens,
		Sessions:           sessions,
		Hasher:             hasher,
		Logins:             user.NewLoginLimiter(rdb, cfg.Limiter.LoginAttempts, cfg.Limiter.LoginWindow),
		Logger:             log,
		Social:             socialVerifiers(cfg.Social),
		DefaultRoleName:    cfg.Auth.DefaultRoleName,
		DefaultCountryCode: cfg.OTP.DefaultCountryCode,
	}

	if store, sender, err := phoneSignIn(cfg, rdb); err != nil {
		// Phone sign-in is switched off rather than silently half-working; the
		// route then answers 404 instead of 500 on every request.
		log.Error("phone sign-in disabled", slog.String("error", err.Error()))
	} else if store != nil {
		deps.OTP = store
		deps.Sender = sender
		deps.OTPResendCooldown = cfg.OTP.ResendCooldown
	}

	userService := user.NewService(deps)

	authenticate := middleware.Authenticate(tokens, sessions)
	limiter := middleware.NewRateLimiter(rdb, cfg.Limiter.Requests, cfg.Limiter.Window)

	images, imagesErr := imageUploader(cfg.Storage)
	if imagesErr != nil {
		// Not fatal: the API is still useful for everything that does not
		// upload, and saying so once at startup beats a mystery 503 later.
		log.Warn("image uploads disabled", slog.String("reason", imagesErr.Error()))
	}

	limit := limiter.Limit(cfg.HTTP.TrustProxyHeaders)

	mux := http.NewServeMux()
	health.NewHandler(db, rdb, cfg.App.Version).Register(mux)
	user.Register(mux, apiPrefix, user.NewHandler(userService), authenticate, limit)

	registerCatalogModules(mux, db, images, hasher, log, authenticate, limit)

	// Outermost first: an id and a logger must exist before anything can panic,
	// and Recover must wrap everything that follows.
	//
	// Rate limiting is deliberately absent here and mounted per route instead:
	// a global limiter would also throttle /health, and an orchestrator whose
	// probes start returning 429 restarts the pod.
	handler := middleware.Chain(mux,
		middleware.RequestID(log),
		middleware.Logger(cfg.HTTP.TrustProxyHeaders),
		middleware.Recover,
		middleware.SecurityHeaders,
		middleware.CORS(cfg.HTTP.CORSOrigins, corsMaxAge),
		middleware.BodyLimit(cfg.HTTP.MaxBodyBytes),
	)

	return &Server{
		http: &http.Server{
			Addr:              ":" + cfg.HTTP.Port,
			Handler:           handler,
			ReadTimeout:       cfg.HTTP.ReadTimeout,
			ReadHeaderTimeout: cfg.HTTP.ReadHeaderTimeout,
			WriteTimeout:      cfg.HTTP.WriteTimeout,
			IdleTimeout:       cfg.HTTP.IdleTimeout,
			// Route the server's own errors into the structured logger.
			ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		log: log,
		cfg: cfg,
	}
}

// Run serves until ctx is cancelled, then drains in-flight requests.
//
// The previous main ended in log.Fatal(e.Start(...)), which meant SIGTERM
// killed the process mid-request; a checkout could be interrupted between
// writing the order and committing the transaction.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		s.log.Info("http server listening",
			slog.String("addr", s.http.Addr),
			slog.String("env", s.cfg.App.Environment))

		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.log.Info("shutdown signal received, draining connections",
			slog.Duration("grace_period", s.cfg.HTTP.ShutdownTimeout))

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.HTTP.ShutdownTimeout)
		defer cancel()

		if err := s.http.Shutdown(shutdownCtx); err != nil {
			// Force the remaining connections closed so the process can exit.
			_ = s.http.Close()
			return fmt.Errorf("graceful shutdown: %w", err)
		}

		s.log.Info("http server stopped cleanly")
		return nil
	}
}

// registerCatalogModules mounts every CRUD module.
//
// They are grouped here rather than inline above so adding one is a single
// line, and so the shared dependencies are constructed once.
func registerCatalogModules(
	mux *http.ServeMux,
	db *gorm.DB,
	images storage.Uploader,
	// hasher is threaded through because creating a cashier creates a user
	// account; hardcoding a bcrypt cost here would silently ignore BCRYPT_COST.
	hasher *auth.Hasher,
	log *slog.Logger,
	authenticate, limit middleware.Middleware,
) {
	reaper := storage.NewReaper(images, log)

	typeProductService := typeproduct.NewService(typeproduct.NewGormRepository(db), images, reaper, log)
	typeproduct.Register(mux, apiPrefix, typeproduct.NewHandler(typeProductService), authenticate, limit)

	productService := product.NewService(product.NewGormRepository(db), images, reaper, log)
	product.Register(mux, apiPrefix, product.NewHandler(productService), authenticate, limit)

	shopService := shop.NewService(shop.NewGormRepository(db), images, reaper, log)
	shop.Register(mux, apiPrefix, shop.NewHandler(shopService), authenticate, limit)

	bannerService := banner.NewService(banner.NewGormRepository(db), images, reaper, log)
	banner.Register(mux, apiPrefix, banner.NewHandler(bannerService), authenticate, limit)

	cashierService := cashier.NewService(cashier.NewGormRepository(db), images, reaper, hasher, log)
	cashier.Register(mux, apiPrefix, cashier.NewHandler(cashierService), authenticate, limit)

	eventService := event.NewService(event.NewGormRepository(db), images, reaper, log)
	event.Register(mux, apiPrefix, event.NewHandler(eventService), authenticate, limit)

	voucherService := voucher.NewService(voucher.NewGormRepository(db), log)
	voucher.Register(mux, apiPrefix, voucher.NewHandler(voucherService), authenticate, limit)

	roleService := role.NewService(role.NewGormRepository(db), log)
	role.Register(mux, apiPrefix, role.NewHandler(roleService), authenticate, limit)

	notificationService := notification.NewService(notification.NewGormRepository(db), log)
	notification.Register(mux, apiPrefix, notification.NewHandler(notificationService), authenticate, limit)

	customerAddressService := customeraddress.NewService(customeraddress.NewGormRepository(db), log)
	customeraddress.Register(mux, apiPrefix, customeraddress.NewHandler(customerAddressService), authenticate, limit)
}
