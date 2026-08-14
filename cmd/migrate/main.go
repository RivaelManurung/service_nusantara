// Command migrate applies the SQL schema history.
//
//	go run ./cmd/migrate            # apply everything pending
//	go run ./cmd/migrate -status    # show what would run, change nothing
//	go run ./cmd/migrate -baseline  # adopt an existing schema, once
//
// This replaces DB_AUTO_MIGRATE as the mechanism for changing the schema.
// AutoMigrate never drops or renames a column, so it drifts from the models
// silently; a checked-in history says exactly what the database contains.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gorm.io/gorm"

	"service_nusantara/internal/config"
	"service_nusantara/internal/platform/database"
	"service_nusantara/internal/platform/logging"
)

const startupTimeout = 60 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	statusOnly := flag.Bool("status", false,
		"list applied and pending migrations without changing anything")
	baseline := flag.Bool("baseline", false,
		"adopt a database whose schema predates this history: record every migration as applied WITHOUT running it")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg.Log.Level, cfg.Log.Format).With(slog.String("command", "migrate"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()

	db, err := database.Open(startupCtx, cfg.Postgres, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(db); err != nil {
			log.Error("close postgres", slog.String("error", err.Error()))
		}
	}()

	switch {
	case *statusOnly:
		return printStatus(ctx, db)
	case *baseline:
		return database.Baseline(ctx, db, log)
	default:
		return database.Migrate(ctx, db, log)
	}
}

// printStatus reports the ledger and what is still outstanding.
func printStatus(ctx context.Context, db *gorm.DB) error {
	applied, err := database.Applied(ctx, db)
	if err != nil {
		return err
	}

	pending, err := database.Pending(ctx, db)
	if err != nil {
		return err
	}

	fmt.Printf("Applied (%d)\n", len(applied))
	for _, row := range applied {
		fmt.Printf("  %-6s %s\n", row.Version, row.AppliedAt)
	}
	if len(applied) == 0 {
		fmt.Println("  (none)")
	}

	fmt.Printf("\nPending (%d)\n", len(pending))
	for _, migration := range pending {
		fmt.Printf("  %-6s %s\n", migration.Version, migration.Name)
	}
	if len(pending) == 0 {
		fmt.Println("  (none)")
	}

	return nil
}
