package database

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"gorm.io/gorm"
)

// migrationFiles holds the schema history. Go's embed cannot reach outside the
// package directory, which is why they live here rather than at the repo root.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migration is one versioned change.
type Migration struct {
	Version  string
	Name     string
	SQL      string
	Checksum string
}

// AppliedMigration is a row of the ledger.
type AppliedMigration struct {
	Version   string
	Checksum  string
	AppliedAt string
}

// ErrNeedsBaseline means the schema already exists but nothing records which
// migrations produced it -- the state of a database built by AutoMigrate before
// the SQL history was introduced.
//
// Applying 0001 would fail on the first CREATE TABLE, and applying it silently
// would be worse: nothing here can prove the existing schema matches what the
// migration describes. The operator has to say so, once, with -baseline.
var ErrNeedsBaseline = errors.New("the schema already exists but no migration is recorded")

// ErrChecksumMismatch means a migration was edited after it had been applied.
//
// Silently re-running or ignoring it would let two environments diverge while
// both claim to be at the same version, which is the failure that makes a
// schema history worthless.
var ErrChecksumMismatch = errors.New("a migration changed after it was applied")

// ledgerDDL creates the tracking table. It is written by hand rather than
// migrated, because it has to exist before any migration can be recorded.
const ledgerDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version     text        PRIMARY KEY,
	checksum    text        NOT NULL,
	applied_at  timestamptz NOT NULL DEFAULT now()
)`

// LoadMigrations reads and orders the embedded files.
func LoadMigrations() ([]Migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	migrations := make([]Migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", entry.Name(), err)
		}

		version, name, _ := strings.Cut(strings.TrimSuffix(entry.Name(), ".sql"), "_")
		sum := sha256.Sum256(contents)

		migrations = append(migrations, Migration{
			Version:  version,
			Name:     name,
			SQL:      string(contents),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	// Lexical order is the applied order, which is why versions are zero padded.
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// Applied returns the ledger, creating it when absent.
func Applied(ctx context.Context, db *gorm.DB) ([]AppliedMigration, error) {
	if err := db.WithContext(ctx).Exec(ledgerDDL).Error; err != nil {
		return nil, fmt.Errorf("create migration ledger: %w", err)
	}

	var rows []AppliedMigration
	err := db.WithContext(ctx).
		Raw(`SELECT version, checksum, applied_at::text AS applied_at
		     FROM schema_migrations ORDER BY version`).
		Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	return rows, nil
}

// Migrate applies every pending migration, in order.
//
// Each runs inside its own transaction together with its ledger row, so a
// failure leaves the database at the last complete version rather than halfway
// through one. PostgreSQL supports transactional DDL, which is what makes this
// safe; on a database that did not, this design would need rethinking.
func Migrate(ctx context.Context, db *gorm.DB, log *slog.Logger) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}

	applied, err := Applied(ctx, db)
	if err != nil {
		return err
	}

	seen := make(map[string]string, len(applied))
	for _, row := range applied {
		seen[row.Version] = row.Checksum
	}

	if len(applied) == 0 && len(migrations) > 0 {
		populated, err := schemaExists(ctx, db)
		if err != nil {
			return err
		}
		if populated {
			return fmt.Errorf(
				"%w: run `migrate -baseline` once to record the current schema as version %s, "+
					"after confirming it matches that migration",
				ErrNeedsBaseline, migrations[0].Version)
		}
	}

	pending := 0
	for _, migration := range migrations {
		if checksum, ok := seen[migration.Version]; ok {
			if checksum != migration.Checksum {
				return fmt.Errorf("%w: %s_%s", ErrChecksumMismatch, migration.Version, migration.Name)
			}
			continue
		}

		log.Info("applying migration",
			slog.String("version", migration.Version),
			slog.String("name", migration.Name))

		if err := applyOne(ctx, db, migration); err != nil {
			return err
		}
		pending++
	}

	if pending == 0 {
		log.Info("schema is up to date", slog.Int("applied", len(applied)))
	} else {
		log.Info("migrations applied", slog.Int("count", pending))
	}
	return nil
}

// Pending lists migrations that have not run yet, for a status command.
func Pending(ctx context.Context, db *gorm.DB) ([]Migration, error) {
	migrations, err := LoadMigrations()
	if err != nil {
		return nil, err
	}

	applied, err := Applied(ctx, db)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool, len(applied))
	for _, row := range applied {
		seen[row.Version] = true
	}

	var pending []Migration
	for _, migration := range migrations {
		if !seen[migration.Version] {
			pending = append(pending, migration)
		}
	}
	return pending, nil
}

// applyOne runs a migration and records it, in one transaction.
//
// It goes through the raw *sql.DB rather than GORM because the pool is opened
// with PrepareStmt, and PostgreSQL refuses a prepared statement containing more
// than one command -- which every migration file is. QueryExecModeSimpleProtocol
// tells pgx to send the file as a single simple query instead of preparing it.
func applyOne(ctx context.Context, db *gorm.DB, migration Migration) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("access sql.DB handle: %w", err)
	}

	tx, err := sqlDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin %s: %w", migration.Version, err)
	}
	// Rollback after a successful commit is a no-op, so this is safe as the
	// single cleanup path.
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, migration.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("apply %s: %w", migration.Version, err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`,
		migration.Version, migration.Checksum)
	if err != nil {
		return fmt.Errorf("record %s: %w", migration.Version, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", migration.Version, err)
	}
	return nil
}

// schemaExists reports whether the application's tables are already present.
//
// `users` is the probe because every deployment has it and nothing else creates
// a table by that name; the migration ledger itself is excluded, since Applied
// creates it before this runs.
func schemaExists(ctx context.Context, db *gorm.DB) (bool, error) {
	var exists bool
	err := db.WithContext(ctx).
		Raw(`SELECT EXISTS (
		       SELECT 1 FROM information_schema.tables
		       WHERE table_schema = current_schema() AND table_name = 'users'
		     )`).
		Scan(&exists).Error
	if err != nil {
		return false, fmt.Errorf("inspect schema: %w", err)
	}
	return exists, nil
}

// Baseline records every known migration as applied WITHOUT running it.
//
// This adopts a database whose schema predates the SQL history. It is
// deliberately manual and refuses once anything is recorded, so it cannot
// quietly mask a genuinely pending migration.
func Baseline(ctx context.Context, db *gorm.DB, log *slog.Logger) error {
	migrations, err := LoadMigrations()
	if err != nil {
		return err
	}

	applied, err := Applied(ctx, db)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		return fmt.Errorf("refusing to baseline: %d migration(s) are already recorded", len(applied))
	}

	populated, err := schemaExists(ctx, db)
	if err != nil {
		return err
	}
	if !populated {
		return errors.New("refusing to baseline an empty database; run the migrations instead")
	}

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("access sql.DB handle: %w", err)
	}

	for _, migration := range migrations {
		_, err := sqlDB.ExecContext(ctx,
			`INSERT INTO schema_migrations (version, checksum) VALUES ($1, $2)`,
			migration.Version, migration.Checksum)
		if err != nil {
			return fmt.Errorf("record %s: %w", migration.Version, err)
		}
		log.Warn("recorded as applied without running it",
			slog.String("version", migration.Version),
			slog.String("name", migration.Name))
	}

	log.Info("baseline complete", slog.Int("recorded", len(migrations)))
	return nil
}
