// Package banner manages the promotional banners shown on the storefront.
//
// It follows the shape of internal/modules/typeproduct: one package holds the
// response struct, the persistence port, the business rules and the HTTP
// handlers, replacing the six legacy layers with four files.
package banner

import (
	"context"
	"errors"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("banner not found")

// Status values. The API models status as an integer for backwards
// compatibility with the clients already in the field.
const (
	StatusInactive = 0
	StatusActive   = 1
)

// Banner is the response shape.
//
// The web client (web_nusantara/src/features/banner/types.ts) reads id, name,
// photo, description and status, so those key names are fixed by contract. The
// timestamps are additive and safe for a client that ignores them.
type Banner struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Photo       string    `json:"photo"`
	Description string    `json:"description"`
	Status      int       `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is a create or update request, already parsed and validated.
type Input struct {
	Name        string
	Description string
	Status      int
	// Image is nil when editing without replacing the artwork.
	Image *multipart.FileHeader
	// CreatedBy is the acting user, taken from the token rather than the body.
	CreatedBy uuid.UUID
}

// ListQuery is a page of results.
type ListQuery struct {
	Page    int
	PerPage int
	Search  string
}

// Repository is the persistence port, defined next to its consumer and small
// enough to fake in a test without a database.
type Repository interface {
	List(ctx context.Context, query ListQuery) ([]Banner, int64, error)
	// ListActive backs the public storefront feed, which only ever shows
	// banners that are switched on.
	ListActive(ctx context.Context) ([]Banner, error)
	FindByID(ctx context.Context, id uuid.UUID) (Banner, error)
	FindActiveByID(ctx context.Context, id uuid.UUID) (Banner, error)
	ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error)
	Create(ctx context.Context, row Banner, createdBy uuid.UUID) (Banner, error)
	Update(ctx context.Context, id uuid.UUID, name, description, photo string) (Banner, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status int) error
	Delete(ctx context.Context, id uuid.UUID) error
}
