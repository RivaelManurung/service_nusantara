// Command seed-assets lists and uploads the fixture's placeholder images.
//
//	go run ./cmd/seed-assets -list          # the target list, as JSON
//	go run ./cmd/seed-assets                # upload images/ to the provider
//
// The images themselves are drawn by tools/generate-seed-images.py, which reads
// the same list. Handles are deterministic (nusantara/seed/<folder>/<key>), so
// the seeder can build a delivery URL without a manifest and re-uploading
// simply replaces the asset in place.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"service_nusantara/internal/config"
	"service_nusantara/internal/platform/logging"
	"service_nusantara/internal/platform/storage"
	"service_nusantara/internal/seed"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	list := flag.Bool("list", false, "print the target list as JSON and exit")
	dir := flag.String("dir", "images", "directory holding the rendered images")
	flag.Parse()

	targets := seed.ImageTargets()

	if *list {
		// Printed before any configuration is read, so the generator can run
		// without image credentials.
		return json.NewEncoder(os.Stdout).Encode(targets)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.Storage.Configured() {
		return fmt.Errorf("CLOUDINARY_* is not configured; nothing can be uploaded")
	}

	log := logging.New(cfg.Log.Level, cfg.Log.Format).With(slog.String("command", "seed-assets"))

	uploader, err := storage.NewCloudinary(storage.CloudinaryConfig{
		CloudName: cfg.Storage.CloudinaryCloudName,
		APIKey:    cfg.Storage.CloudinaryAPIKey,
		APISecret: cfg.Storage.CloudinaryAPISecret,
		// Handles are absolute: the fixture computes them itself, so the root
		// folder must not be prepended a second time.
		RootFolder: "",
		Timeout:    cfg.Storage.Timeout,
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var uploaded, missing int
	start := time.Now()

	for _, target := range targets {
		path := filepath.Join(*dir, target.Folder, target.Key+".png")

		if _, err := os.Stat(path); err != nil {
			missing++
			fmt.Printf("  ? %-40s not rendered yet\n", target.PublicID())
			continue
		}

		if err := uploader.UploadFile(ctx, path, target.PublicID()); err != nil {
			return fmt.Errorf("upload %s: %w", path, err)
		}

		uploaded++
		fmt.Printf("  + %s\n", target.PublicID())
	}

	log.Info("assets uploaded",
		slog.Int("uploaded", uploaded),
		slog.Int("missing", missing),
		slog.Duration("took", time.Since(start)))

	if missing > 0 {
		fmt.Printf("\n%d image(s) are missing. Render them first:\n", missing)
		fmt.Println("  python3 tools/generate-seed-images.py")
	}
	return nil
}
