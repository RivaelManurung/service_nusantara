// Command backfill-images recovers the storage handle for rows written before
// it was stored.
//
//	go run ./cmd/backfill-images              # report only, change nothing
//	go run ./cmd/backfill-images -apply       # write the verified handles
//
// Every candidate is derived from the stored URL and then CONFIRMED against the
// provider before it is written. A derived id that the provider does not
// recognise is reported, never saved: replacing an empty handle with a
// plausible-but-wrong one would be worse than leaving it empty, because a later
// delete would silently succeed while removing nothing.
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
	"service_nusantara/internal/platform/storage"
)

const startupTimeout = 60 * time.Second

// target describes one column pair to reconcile.
type target struct {
	table     string
	urlColumn string
	idColumn  string
}

// targets covers every place an image URL is stored. Adding a column here is
// the only change a new image-bearing table needs.
var targets = []target{
	{"images", "image_path", "public_id"},
	{"type_products", "image", "image_public_id"},
	{"banners", "photo", "photo_public_id"},
	{"shops", "cover", "cover_public_id"},
	{"events", "cover", "cover_public_id"},
	{"users", "photo", "photo_public_id"},
}

type row struct {
	ID  string
	URL string
}

type verifier interface {
	Exists(ctx context.Context, publicID string) (bool, error)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	apply := flag.Bool("apply", false, "write the verified handles; without it nothing is changed")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logging.New(cfg.Log.Level, cfg.Log.Format).With(slog.String("command", "backfill-images"))

	if !cfg.Storage.Configured() {
		return fmt.Errorf("CLOUDINARY_* is not configured; a handle cannot be verified without it")
	}

	uploader, err := storage.NewCloudinary(storage.CloudinaryConfig{
		CloudName:  cfg.Storage.CloudinaryCloudName,
		APIKey:     cfg.Storage.CloudinaryAPIKey,
		APISecret:  cfg.Storage.CloudinaryAPISecret,
		RootFolder: cfg.Storage.RootFolder,
		Timeout:    cfg.Storage.Timeout,
	})
	if err != nil {
		return err
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

	if !*apply {
		fmt.Println("DRY RUN — nothing will be written. Re-run with -apply to save.")
	}
	fmt.Println()

	var totals struct{ candidates, verified, unresolved, written int }

	for _, t := range targets {
		result, err := reconcile(ctx, db, uploader, t, *apply)
		if err != nil {
			return fmt.Errorf("%s: %w", t.table, err)
		}

		totals.candidates += result.candidates
		totals.verified += result.verified
		totals.unresolved += result.unresolved
		totals.written += result.written
	}

	fmt.Printf("\nTotal: %d row(s) missing a handle, %d verified, %d unresolved, %d written\n",
		totals.candidates, totals.verified, totals.unresolved, totals.written)

	if totals.unresolved > 0 {
		fmt.Println("\nUnresolved rows keep an empty handle. Their images cannot be deleted")
		fmt.Println("automatically; reconcile them by hand against the provider.")
	}
	return nil
}

type outcome struct{ candidates, verified, unresolved, written int }

// reconcile derives, verifies and optionally writes handles for one table.
func reconcile(ctx context.Context, db *gorm.DB, assets verifier, t target, apply bool) (outcome, error) {
	var rows []row

	// Only rows that have a URL but no handle are candidates; anything else is
	// either already correct or has no image at all.
	query := fmt.Sprintf(
		`SELECT id::text AS id, %s AS url FROM %s
		 WHERE %s IS NOT NULL AND %s <> '' AND (%s IS NULL OR %s = '')`,
		t.urlColumn, t.table, t.urlColumn, t.urlColumn, t.idColumn, t.idColumn)

	if err := db.WithContext(ctx).Raw(query).Scan(&rows).Error; err != nil {
		return outcome{}, fmt.Errorf("read candidates: %w", err)
	}

	result := outcome{candidates: len(rows)}
	fmt.Printf("%-16s %d row(s) missing a handle\n", t.table, len(rows))

	for _, r := range rows {
		publicID, ok := storage.PublicIDFromURL(r.URL)
		if !ok {
			result.unresolved++
			fmt.Printf("  ? %s  not a provider URL: %s\n", r.ID, r.URL)
			continue
		}

		exists, err := assets.Exists(ctx, publicID)
		if err != nil {
			// A lookup failure is not proof of absence, so nothing is written.
			result.unresolved++
			fmt.Printf("  ! %s  could not verify %q: %v\n", r.ID, publicID, err)
			continue
		}
		if !exists {
			result.unresolved++
			fmt.Printf("  ? %s  derived %q but the provider has no such asset\n", r.ID, publicID)
			continue
		}

		result.verified++
		if !apply {
			fmt.Printf("  = %s  would set %s\n", r.ID, publicID)
			continue
		}

		update := fmt.Sprintf(`UPDATE %s SET %s = ? WHERE id = ?::uuid`, t.table, t.idColumn)
		if err := db.WithContext(ctx).Exec(update, publicID, r.ID).Error; err != nil {
			return result, fmt.Errorf("write handle for %s: %w", r.ID, err)
		}
		result.written++
		fmt.Printf("  + %s  %s\n", r.ID, publicID)
	}

	return result, nil
}
