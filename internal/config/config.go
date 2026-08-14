// Package config loads and validates every runtime setting once, at startup.
//
// Nothing else in the codebase reads os.Getenv: handlers, repositories and
// workers receive the values they need through explicit dependencies. A missing
// or malformed variable is reported as an error (never a log.Fatal buried in a
// package) so main decides how the process exits and tests can build a Config
// by hand.
package config

import (
	"slices"
	"time"

	"github.com/joho/godotenv"
)

const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// minJWTSecretLen mirrors the 256-bit key size recommended for HS256.
const minJWTSecretLen = 32

type Config struct {
	App      App
	HTTP     HTTP
	Postgres Postgres
	Redis    Redis
	Auth     Auth
	Social   Social
	OTP      OTP
	Limiter  Limiter
	Log      Log
}

type App struct {
	Name        string
	Environment string
	Version     string
}

// IsProduction reports whether hardening that is inconvenient in development
// (strict CORS, no auto-migration) must be enforced.
func (a App) IsProduction() bool { return a.Environment == EnvProduction }

type HTTP struct {
	Port              string
	ReadTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ShutdownTimeout   time.Duration
	MaxBodyBytes      int64
	CORSOrigins       []string
	TrustProxyHeaders bool
}

type Postgres struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	AutoMigrate     bool
	SlowQuery       time.Duration
}

type Redis struct {
	Addr     string
	Username string
	Password string
	DB       int
	PoolSize int
}

type Auth struct {
	JWTSecret       string
	Issuer          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	BcryptCost      int
	// DefaultRoleName is granted to accounts created by a self-service sign-up
	// (Google, Apple or phone). Back-office accounts are still created with an
	// explicit role.
	DefaultRoleName string
}

// Social configures the third-party sign-in providers.
type Social struct {
	// GoogleAudiences and AppleAudiences are the client IDs this backend
	// accepts. A mobile app usually has one per platform. Leaving a list empty
	// disables that provider rather than accepting every audience.
	GoogleAudiences []string
	AppleAudiences  []string
	HTTPTimeout     time.Duration
	JWKSCacheTTL    time.Duration
}

// GoogleEnabled reports whether Google sign-in is configured.
func (s Social) GoogleEnabled() bool { return len(s.GoogleAudiences) > 0 }

// AppleEnabled reports whether Apple sign-in is configured.
func (s Social) AppleEnabled() bool { return len(s.AppleAudiences) > 0 }

// OTP configures phone sign-in.
type OTP struct {
	Enabled        bool
	Length         int
	TTL            time.Duration
	MaxAttempts    int
	ResendCooldown time.Duration
	// Sender selects the delivery adapter: "log" prints the code instead of
	// sending it, for development only.
	Sender string
	// DefaultCountryCode expands local numbers such as 08123... to E.164.
	DefaultCountryCode string
}

type Limiter struct {
	// Requests/Window is the global per-client budget.
	Requests int
	Window   time.Duration
	// LoginAttempts/LoginWindow is the brute-force lock on credential checks.
	LoginAttempts int
	LoginWindow   time.Duration
}

type Log struct {
	Level  string
	Format string // "json" or "text"
}

// Load reads .env when present (never required) then the process environment.
// Every validation problem is aggregated into a single error.
func Load() (Config, error) {
	// A missing .env is normal in containers, so the file is best effort.
	_ = godotenv.Load()

	r := &envReader{}

	cfg := Config{
		App: App{
			Name:        r.str("APP_NAME", "nusantara-service"),
			Environment: r.str("APP_ENV", EnvDevelopment),
			Version:     r.str("APP_VERSION", "dev"),
		},
		HTTP: HTTP{
			Port:              r.str("PORT", "8080"),
			ReadTimeout:       r.duration("HTTP_READ_TIMEOUT", 15*time.Second),
			ReadHeaderTimeout: r.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			WriteTimeout:      r.duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:       r.duration("HTTP_IDLE_TIMEOUT", 60*time.Second),
			ShutdownTimeout:   r.duration("HTTP_SHUTDOWN_TIMEOUT", 15*time.Second),
			MaxBodyBytes:      int64(r.int("HTTP_MAX_BODY_BYTES", 2<<20)),
			CORSOrigins:       r.csv("CORS_ALLOWED_ORIGINS", nil),
			TrustProxyHeaders: r.boolean("HTTP_TRUST_PROXY_HEADERS", false),
		},
		Postgres: Postgres{
			DSN:             r.required("DATABASE_URL"),
			MaxOpenConns:    r.int("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    r.int("DB_MAX_IDLE_CONNS", 5),
			ConnMaxLifetime: r.duration("DB_CONN_MAX_LIFETIME", time.Hour),
			ConnMaxIdleTime: r.duration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute),
			AutoMigrate:     r.boolean("DB_AUTO_MIGRATE", false),
			SlowQuery:       r.duration("DB_SLOW_QUERY_THRESHOLD", 300*time.Millisecond),
		},
		Redis: Redis{
			Addr:     r.required("REDIS_ADDR"),
			Username: r.str("REDIS_USERNAME", ""),
			Password: r.str("REDIS_PASSWORD", ""),
			DB:       r.int("REDIS_DB", 0),
			PoolSize: r.int("REDIS_POOL_SIZE", 10),
		},
		Auth: Auth{
			JWTSecret:       r.requiredMinLen("JWT_SECRET", minJWTSecretLen),
			Issuer:          r.str("JWT_ISSUER", "nusantara-service"),
			AccessTokenTTL:  r.duration("JWT_ACCESS_TTL", 15*time.Minute),
			RefreshTokenTTL: r.duration("JWT_REFRESH_TTL", 720*time.Hour),
			BcryptCost:      r.int("BCRYPT_COST", 12),
			DefaultRoleName: r.str("DEFAULT_SIGNUP_ROLE", "customer"),
		},
		Social: Social{
			GoogleAudiences: r.csv("GOOGLE_CLIENT_IDS", nil),
			AppleAudiences:  r.csv("APPLE_CLIENT_IDS", nil),
			HTTPTimeout:     r.duration("OIDC_HTTP_TIMEOUT", 10*time.Second),
			JWKSCacheTTL:    r.duration("OIDC_JWKS_CACHE_TTL", time.Hour),
		},
		OTP: OTP{
			Enabled:            r.boolean("OTP_ENABLED", true),
			Length:             r.int("OTP_LENGTH", 6),
			TTL:                r.duration("OTP_TTL", 5*time.Minute),
			MaxAttempts:        r.int("OTP_MAX_ATTEMPTS", 5),
			ResendCooldown:     r.duration("OTP_RESEND_COOLDOWN", 60*time.Second),
			Sender:             r.str("OTP_SENDER", "log"),
			DefaultCountryCode: r.str("PHONE_DEFAULT_COUNTRY_CODE", "62"),
		},
		Limiter: Limiter{
			Requests:      r.int("RATE_LIMIT_REQUESTS", 120),
			Window:        r.duration("RATE_LIMIT_WINDOW", time.Minute),
			LoginAttempts: r.int("LOGIN_MAX_ATTEMPTS", 5),
			LoginWindow:   r.duration("LOGIN_LOCK_WINDOW", 15*time.Minute),
		},
		Log: Log{
			Level:  r.str("LOG_LEVEL", "info"),
			Format: r.str("LOG_FORMAT", "json"),
		},
	}

	cfg.validate(r)

	return cfg, r.err()
}

func (c Config) validate(r *envReader) {
	validEnvs := []string{EnvDevelopment, EnvStaging, EnvProduction}
	if !slices.Contains(validEnvs, c.App.Environment) {
		r.fail("APP_ENV must be one of %v, got %q", validEnvs, c.App.Environment)
	}

	if c.Auth.BcryptCost < 10 || c.Auth.BcryptCost > 31 {
		r.fail("BCRYPT_COST must be between 10 and 31, got %d", c.Auth.BcryptCost)
	}
	if c.Auth.AccessTokenTTL >= c.Auth.RefreshTokenTTL {
		r.fail("JWT_ACCESS_TTL (%s) must be shorter than JWT_REFRESH_TTL (%s)",
			c.Auth.AccessTokenTTL, c.Auth.RefreshTokenTTL)
	}

	if c.OTP.Enabled {
		if c.OTP.Length < 4 || c.OTP.Length > 10 {
			r.fail("OTP_LENGTH must be between 4 and 10, got %d", c.OTP.Length)
		}
		if c.OTP.MaxAttempts < 1 {
			r.fail("OTP_MAX_ATTEMPTS must be at least 1, got %d", c.OTP.MaxAttempts)
		}
		if !slices.Contains([]string{"log"}, c.OTP.Sender) {
			// Only the development sender ships today; see the README for how
			// to add an SMS adapter.
			r.fail("OTP_SENDER must be one of [log], got %q", c.OTP.Sender)
		}
	}

	if c.Postgres.MaxIdleConns > c.Postgres.MaxOpenConns {
		r.fail("DB_MAX_IDLE_CONNS (%d) cannot exceed DB_MAX_OPEN_CONNS (%d)",
			c.Postgres.MaxIdleConns, c.Postgres.MaxOpenConns)
	}

	// A wildcard origin cannot be combined with credentialed requests, and in
	// production it is almost always an oversight rather than an intent.
	if c.App.IsProduction() {
		if len(c.HTTP.CORSOrigins) == 0 {
			r.fail("CORS_ALLOWED_ORIGINS must list explicit origins in production")
		}
		if slices.Contains(c.HTTP.CORSOrigins, "*") {
			r.fail(`CORS_ALLOWED_ORIGINS must not contain "*" in production`)
		}
		if c.Postgres.AutoMigrate {
			r.fail("DB_AUTO_MIGRATE must be false in production; run migrations as a deliberate step")
		}
		if c.OTP.Enabled && c.OTP.Sender == "log" {
			// The log sender writes the code in plaintext to every log sink.
			r.fail("OTP_SENDER must not be \"log\" in production; configure a real SMS provider or set OTP_ENABLED=false")
		}
	}
}
