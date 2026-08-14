package user

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// LoginLimiter locks an email out after repeated failed credential checks.
//
// It is separate from the global rate limiter because the counter must survive
// a correct-but-slow client and reset only on success.
type LoginLimiter struct {
	rdb         *redis.Client
	maxAttempts int
	window      time.Duration
}

func NewLoginLimiter(rdb *redis.Client, maxAttempts int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{rdb: rdb, maxAttempts: maxAttempts, window: window}
}

// Allow records an attempt and reports whether it may proceed.
func (l *LoginLimiter) Allow(ctx context.Context, key string) (bool, time.Duration, error) {
	pipe := l.rdb.TxPipeline()
	countCmd := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, l.window)
	if _, err := pipe.Exec(ctx); err != nil {
		return false, 0, fmt.Errorf("record login attempt: %w", err)
	}

	if countCmd.Val() <= int64(l.maxAttempts) {
		return true, 0, nil
	}

	ttl, err := l.rdb.TTL(ctx, key).Result()
	if err != nil || ttl < 0 {
		ttl = l.window
	}
	return false, ttl, nil
}

// Reset clears the counter after a successful sign in.
func (l *LoginLimiter) Reset(ctx context.Context, key string) error {
	if err := l.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("reset login attempts: %w", err)
	}
	return nil
}
