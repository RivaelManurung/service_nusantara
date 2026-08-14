// Command api runs the Nusantara HTTP service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"service_nusantara/internal/config"
	"service_nusantara/internal/model"
	"service_nusantara/internal/platform/cache"
	"service_nusantara/internal/platform/database"
	"service_nusantara/internal/platform/logging"
	"service_nusantara/internal/server"
)

// startupTimeout bounds dependency dialling so a wedged database turns into a
// clear failure instead of a container that hangs in "starting" forever.
const startupTimeout = 30 * time.Second

func main() {
	// run returns the error; main owns the exit code. Keeping os.Exit in one
	// place means every deferred Close still runs.
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg.Log.Level, cfg.Log.Format).With(
		slog.String("service", cfg.App.Name),
		slog.String("version", cfg.App.Version))
	slog.SetDefault(log)

	// Cancelled on SIGINT/SIGTERM; this context drives the shutdown sequence.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startupCtx, cancelStartup := context.WithTimeout(ctx, startupTimeout)
	defer cancelStartup()

	db, err := database.Open(startupCtx, cfg.Postgres, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(db); err != nil {
			log.Error("close postgres", slog.String("error", err.Error()))
		}
	}()

	rdb, err := cache.Open(startupCtx, cfg.Redis, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := rdb.Close(); err != nil {
			log.Error("close redis", slog.String("error", err.Error()))
		}
	}()

	if cfg.Postgres.AutoMigrate {
		log.Warn("running AutoMigrate; use reviewed SQL migrations outside development")
		if err := model.AutoMigrate(startupCtx, db); err != nil {
			// The previous main discarded this error, so the service would come
			// up and then fail on the first query against a missing column.
			return err
		}
		log.Info("schema migrated")
	}

	return server.New(cfg, db, rdb, log).Run(ctx)
}
