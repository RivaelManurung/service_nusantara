// Package cache owns the Redis client used for sessions, token revocation and
// rate limiting.
package cache

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/redis/go-redis/v9"

	"service_nusantara/internal/config"
)

// Open dials Redis and verifies it answers before the server starts serving.
func Open(ctx context.Context, cfg config.Redis, log *slog.Logger) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Username: cfg.Username,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		// The previous service panicked here, which produced a stack trace
		// instead of an actionable startup message.
		return nil, fmt.Errorf("ping redis at %s: %w", cfg.Addr, err)
	}

	log.Info("redis connected", slog.String("addr", cfg.Addr), slog.Int("db", cfg.DB))

	return client, nil
}
