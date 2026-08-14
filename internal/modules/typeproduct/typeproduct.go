// Package typeproduct manages the catalogue's product categories.
//
// It is the reference implementation for every CRUD module: one package holds
// the response shape, the persistence port, the business rules and the HTTP
// handlers, in that order. The auth module (internal/modules/user) shows the
// same shape for a feature without uploads.
package typeproduct

import (
	"context"
	"errors"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("type product not found")

// Status values. The API models status as an integer for backwards
// compatibility with the clients already in the field.
const (
	StatusInactive = 0
	StatusActive   = 1
)

// TypeProduct is the response shape. The web client reads exactly these four
// fields, so they are fixed by contract rather than by convenience.
type TypeProduct struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Image  string    `json:"image"`
	Status int       `json:"status"`

	// ImagePublicID is the storage handle, never part of the client contract.
	// It exists so a replaced or deleted image can actually be removed.
	ImagePublicID string `json:"-"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is a create or update request, already parsed and validated.
type Input struct {
	Name   string
	Status int
	// Image is nil when editing without replacing the picture.
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
	List(ctx context.Context, query ListQuery) ([]TypeProduct, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (TypeProduct, error)
	ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error)
	Create(ctx context.Context, row TypeProduct, createdBy uuid.UUID) (TypeProduct, error)
	Update(ctx context.Context, id uuid.UUID, name, image, imagePublicID string) (TypeProduct, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status int) error
	Delete(ctx context.Context, id uuid.UUID) error
	// InUse reports whether any product still references this category, so the
	// delete can explain itself instead of surfacing a foreign key violation.
	InUse(ctx context.Context, id uuid.UUID) (bool, error)
}
