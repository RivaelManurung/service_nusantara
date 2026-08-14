// Package database opens and tunes the PostgreSQL connection pool.
//
// Unlike the previous configs.InitDB, nothing here is stored in a package-level
// variable and nothing calls log.Fatal: the caller receives *gorm.DB plus an
// error and owns the lifecycle.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"service_nusantara/internal/config"
)

// Open connects, verifies the connection with a ping and applies pool limits.
func Open(ctx context.Context, cfg config.Postgres, log *slog.Logger) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger: newGormLogger(log, cfg.SlowQuery),
		// Prepared statements are cached, which is the main reason to use pgx
		// over the simple protocol; the previous service disabled both.
		PrepareStmt: true,
		// Timestamps are stored and compared in UTC to keep order history and
		// event windows unambiguous across regions.
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("access sql.DB handle: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	if err := sqlDB.PingContext(ctx); err != nil {
		// Close the half-open pool so a failed startup leaks no sockets.
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	log.Info("postgres connected",
		slog.Int("max_open_conns", cfg.MaxOpenConns),
		slog.Int("max_idle_conns", cfg.MaxIdleConns))

	return db, nil
}

// Close releases the pool. It is safe to call on a partially initialised DB.
func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// Ping reports whether the pool can still reach the database, for /health/ready.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}
