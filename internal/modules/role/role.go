// Package role manages the account roles the auth module assigns to users.
//
// It follows the shape of internal/modules/typeproduct: one package holds the
// response struct, the persistence port, the business rules and the HTTP
// handlers. Roles carry no image and no status column, so the payloads are
// JSON rather than multipart and there is no /edit-status route.
package role

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("role not found")

// Role is the response shape. The previous service returned the persisted
// entity directly, which exposed exactly these two fields, so clients already
// in the field keep reading the same keys.
type Role struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// Input is a create or update request, already parsed and validated.
type Input struct {
	Name string
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
	List(ctx context.Context, query ListQuery) ([]Role, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Role, error)
	ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error)
	Create(ctx context.Context, row Role) (Role, error)
	Update(ctx context.Context, id uuid.UUID, name string) (Role, error)
	Delete(ctx context.Context, id uuid.UUID) error
	// InUse reports whether any user still references this role, so the delete
	// can explain itself instead of surfacing a foreign key violation.
	InUse(ctx context.Context, id uuid.UUID) (bool, error)
}
