// Package seed populates a database with a coherent demo dataset.
//
// Two properties shape the design:
//
//   - Idempotent. Every row gets a UUID derived from its business key, so a
//     second run updates rather than duplicates. There is no "did I already
//     seed this?" state to keep.
//   - Ordered but independent. Stages run in dependency order, and each one may
//     be run on its own with -only, which is what makes the seeder usable for
//     topping up a single table during development.
package seed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"service_nusantara/internal/model"
)

// Options controls one seeding run.
type Options struct {
	// Only restricts the run to the named stages. Empty means every stage.
	Only []string
	// Skip removes stages from the run.
	Skip []string
	// Truncate empties the seeded tables first. Destructive; the caller is
	// responsible for confirming it.
	Truncate bool
	// Scale multiplies the generated customers and orders. 1 keeps the
	// hand-written dataset only.
	Scale int
	// BcryptCost is the cost used for seeded passwords. Tests and local runs
	// lower it; it should otherwise match the application's setting.
	BcryptCost int
}

// Hasher is the password hashing dependency, so the seeder produces hashes the
// running service accepts rather than reimplementing bcrypt.
type Hasher interface {
	Hash(password string) (string, error)
}

// Seeder writes the demo dataset.
type Seeder struct {
	db     *gorm.DB
	hasher Hasher
	log    *slog.Logger
	// now anchors every relative timestamp in one run, so an event that starts
	// "three days ago" is consistent across all its rows.
	now time.Time
}

func New(db *gorm.DB, hasher Hasher, log *slog.Logger) *Seeder {
	return &Seeder{db: db, hasher: hasher, log: log, now: time.Now().UTC()}
}

// stage is one named unit of work.
type stage struct {
	name string
	run  func(*Seeder, context.Context, Options) error
}

// stages run in this order; later ones depend on earlier ones.
var stages = []stage{
	{"roles", (*Seeder).seedRoles},
	{"permissions", (*Seeder).seedPermissions},
	{"users", (*Seeder).seedUsers},
	{"identities", (*Seeder).seedIdentities},
	{"images", (*Seeder).seedImages},
	{"catalog", (*Seeder).seedCatalog},
	{"shops", (*Seeder).seedShops},
	{"addresses", (*Seeder).seedAddresses},
	{"banners", (*Seeder).seedBanners},
	{"vouchers", (*Seeder).seedVouchers},
	{"points", (*Seeder).seedPoints},
	{"events", (*Seeder).seedEvents},
	{"carts", (*Seeder).seedCarts},
	{"favorites", (*Seeder).seedFavorites},
	{"orders", (*Seeder).seedOrders},
}

// StageNames lists every stage, for the command's help text.
func StageNames() []string {
	names := make([]string, 0, len(stages))
	for _, s := range stages {
		names = append(names, s.name)
	}
	return names
}

// Run executes the selected stages.
func (s *Seeder) Run(ctx context.Context, opts Options) error {
	if opts.Scale < 1 {
		opts.Scale = 1
	}
	if err := validateStageNames(opts); err != nil {
		return err
	}

	if opts.Truncate {
		if err := s.truncate(ctx); err != nil {
			return err
		}
	}

	for _, st := range stages {
		if !selected(st.name, opts) {
			continue
		}

		start := time.Now()
		if err := st.run(s, ctx, opts); err != nil {
			return fmt.Errorf("stage %q: %w", st.name, err)
		}
		s.log.Info("stage complete",
			slog.String("stage", st.name),
			slog.Duration("took", time.Since(start)))
	}

	return nil
}

func selected(name string, opts Options) bool {
	if slices.Contains(opts.Skip, name) {
		return false
	}
	return len(opts.Only) == 0 || slices.Contains(opts.Only, name)
}

func validateStageNames(opts Options) error {
	known := StageNames()
	var unknown []string
	for _, name := range slices.Concat(opts.Only, opts.Skip) {
		if !slices.Contains(known, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown stage(s) %s; valid stages are: %s",
			strings.Join(unknown, ", "), strings.Join(known, ", "))
	}
	return nil
}

// upsert writes rows, replacing any that already exist by primary key. This is
// what lets the seeder converge on the dataset instead of failing on the second
// run with a duplicate key.
func upsert[T any](ctx context.Context, db *gorm.DB, rows []T) error {
	if len(rows) == 0 {
		return nil
	}
	return db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "id"}},
			UpdateAll: true,
		}).
		CreateInBatches(rows, 200).Error
}

// truncate empties every seeded table.
//
// Order is the reverse of insertion and RESTART IDENTITY CASCADE clears
// dependents, so foreign keys never block the wipe.
func (s *Seeder) truncate(ctx context.Context) error {
	tables, err := s.tableNames()
	if err != nil {
		return err
	}
	slices.Reverse(tables)

	s.log.Warn("truncating seeded tables", slog.Int("count", len(tables)))

	statement := fmt.Sprintf("TRUNCATE TABLE %s RESTART IDENTITY CASCADE",
		strings.Join(tables, ", "))
	if err := s.db.WithContext(ctx).Exec(statement).Error; err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	return nil
}

// tableNames resolves the physical table name of every model through GORM's
// naming strategy, rather than hardcoding a list that silently drifts.
func (s *Seeder) tableNames() ([]string, error) {
	models := model.All()
	names := make([]string, 0, len(models))

	for _, m := range models {
		statement := &gorm.Statement{DB: s.db}
		if err := statement.Parse(m); err != nil {
			return nil, fmt.Errorf("resolve table name for %T: %w", m, err)
		}
		names = append(names, statement.Schema.Table)
	}

	if len(names) == 0 {
		return nil, errors.New("no models registered")
	}
	return names, nil
}
