// Command seed populates the database with the demo dataset.
//
//	go run ./cmd/seed                          # everything, idempotent
//	go run ./cmd/seed -only=roles,users        # one or more stages
//	go run ./cmd/seed -skip=orders             # everything except orders
//	go run ./cmd/seed -scale=10                # 180 extra generated customers
//	go run ./cmd/seed -truncate -yes           # reset first, then seed
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/config"
	"service_nusantara/internal/platform/database"
	"service_nusantara/internal/platform/logging"
	"service_nusantara/internal/seed"
)

// startupTimeout bounds dialling the database.
const startupTimeout = 30 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		only            = flag.String("only", "", "comma separated stages to run (default: all)")
		skip            = flag.String("skip", "", "comma separated stages to skip")
		reset           = flag.Bool("truncate", false, "reset the seeded tables before seeding (destructive)")
		confirm         = flag.Bool("yes", false, "confirm a destructive run; required with -truncate")
		scale           = flag.Int("scale", 1, "multiplier for generated customers (1 = fixtures only)")
		migrate         = flag.Bool("migrate", false, "apply pending SQL migrations before seeding")
		allowProduction = flag.Bool("allow-production", false, "permit seeding when APP_ENV=production")
	)
	flag.Usage = usage
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg.Log.Level, cfg.Log.Format).With(slog.String("command", "seed"))

	if err := guardProduction(cfg, *allowProduction, *reset); err != nil {
		return err
	}
	if *reset && !*confirm {
		return errors.New("-truncate discards every row in the seeded tables; re-run with -yes to confirm")
	}

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

	if *migrate {
		// The SQL history, not AutoMigrate. The runtime image is distroless and
		// has no shell, so a pre-deploy step cannot chain `migrate && seed`;
		// folding the migration in here keeps it one binary invocation.
		if err := database.Migrate(ctx, db, log); err != nil {
			return err
		}
	}

	seeder := seed.New(db, auth.NewHasher(cfg.Auth.BcryptCost), log)

	start := time.Now()
	if err := seeder.Run(ctx, seed.Options{
		Only:       splitList(*only),
		Skip:       splitList(*skip),
		Truncate:   *reset,
		Scale:      *scale,
		BcryptCost: cfg.Auth.BcryptCost,
	}); err != nil {
		return err
	}

	log.Info("seeding complete", slog.Duration("took", time.Since(start)))

	fmt.Printf("\nDemo accounts (password: %s)\n", seed.DemoPassword())
	for _, line := range seed.AccountSummary() {
		fmt.Println("  " + line)
	}

	return nil
}

// guardProduction refuses the run unless the operator has been explicit.
//
// Seeding writes fixture accounts with a published password; doing that to a
// production database is almost always an accident, and resetting one is not
// recoverable from here.
func guardProduction(cfg config.Config, allowProduction, reset bool) error {
	if !cfg.App.IsProduction() {
		return nil
	}
	if reset {
		return errors.New("refusing to reset a production database")
	}
	if !allowProduction {
		return errors.New("APP_ENV is production; re-run with -allow-production if this is really intended")
	}
	return nil
}

func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func usage() {
	fmt.Fprintf(flag.CommandLine.Output(), `Seed the Nusantara database with demo data.

Usage:
  go run ./cmd/seed [flags]

Stages run in dependency order:
  %s

Flags:
`, strings.Join(seed.StageNames(), ", "))
	flag.PrintDefaults()
}
