package middleware

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// RateLimiter applies a fixed-window budget per client, backed by Redis so the
// limit holds across every replica rather than per process.
type RateLimiter struct {
	rdb    *redis.Client
	limit  int
	window time.Duration
}

func NewRateLimiter(rdb *redis.Client, limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{rdb: rdb, limit: limit, window: window}
}

// Allow increments the counter for key and reports whether the caller is still
// within budget, along with the time until the window resets.
func (l *RateLimiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	redisKey := "ratelimit:" + key

	pipe := l.rdb.TxPipeline()
	countCmd := pipe.Incr(ctx, redisKey)
	// Setting the TTL only when absent keeps the window fixed rather than
	// sliding forward on every request, which would never let it reset.
	pipe.ExpireNX(ctx, redisKey, l.window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, err
	}

	count := countCmd.Val()
	if count <= int64(l.limit) {
		return true, 0, nil
	}

	ttl, err := l.rdb.TTL(ctx, redisKey).Result()
	if err != nil || ttl < 0 {
		ttl = l.window
	}
	return false, ttl, nil
}

// budget is the slice of RateLimiter the middleware needs, which lets tests
// observe the key that was chosen without a live Redis.
type budget interface {
	Allow(ctx context.Context, key string) (bool, time.Duration, error)
}

// Limit throttles requests keyed by authenticated user when available and by
// client IP otherwise, so one noisy user cannot consume a shared IP budget.
//
// It must be mounted *inside* Authenticate; see internal/modules/user/routes.go.
func (l *RateLimiter) Limit(trustProxyHeaders bool) Middleware {
	return LimitWith(l, trustProxyHeaders)
}

// LimitWith is Limit against any budget implementation.
func LimitWith(l budget, trustProxyHeaders bool) Middleware {
	return func(next http.Handler) http.Handler {
		return httpx.Handler(func(w http.ResponseWriter, r *http.Request) error {
			key := "ip:" + ClientIP(r, trustProxyHeaders)
			if identity, ok := auth.IdentityFrom(r.Context()); ok {
				key = "user:" + identity.UserID
			}

			allowed, retryAfter, err := l.Allow(r.Context(), key)
			if err != nil {
				// A limiter outage should not take the API down with it; log
				// and let the request through rather than failing closed here.
				logWarn(r, "rate limiter unavailable", err)
				next.ServeHTTP(w, r)
				return nil
			}

			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
				return httpx.RateLimited("too many requests, please slow down").
					WithDetails(map[string]any{"retry_after_seconds": int(retryAfter.Seconds())})
			}

			next.ServeHTTP(w, r)
			return nil
		})
	}
}
