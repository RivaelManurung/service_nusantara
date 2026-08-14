// Package cashier manages the shop-floor accounts the back office creates.
//
// A cashier is not a table of its own: it is a row in `users` carrying the
// cashier role, which is why this package writes through model.User and looks
// its role up by name instead of hardcoding an identifier.
//
// It follows the shape of internal/modules/typeproduct: response struct,
// persistence port, business rules, HTTP handlers.
package cashier

import (
	"context"
	"errors"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("cashier not found")

// ErrRoleMissing is returned when the cashier role has not been seeded. It is
// a deployment fault, not a client one, so the service reports it as a 503.
var ErrRoleMissing = errors.New("cashier role does not exist")

// RoleName is the role a cashier account is created with, and the role the
// listing filters on. It is resolved to an id at runtime.
const RoleName = "cashier"

// Status values. The API models status as an integer for backwards
// compatibility with the clients already in the field.
const (
	StatusInactive = 0
	StatusActive   = 1
)

// Cashier is the response shape.
//
// The web client (web_nusantara/src/features/cashier/types.ts) reads id, name,
// username, email, photo, status, role and created_at, so those key names are
// fixed by contract. There is deliberately no password field: the legacy
// UserEntity carried a Password string tagged json:"password", which meant the
// bcrypt hash was serialised into every cached and embedded payload.
type Cashier struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Photo    string    `json:"photo"`
	Status   int       `json:"status"`
	Role     string    `json:"role"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateInput is a parsed POST /cashier/create body.
type CreateInput struct {
	Name     string
	Username string
	Email    string
	Password string
	Status   int
	Image    *multipart.FileHeader
}

// UpdateInput is a parsed PUT /cashier/{id}/edit body.
//
// Every field is optional because the same endpoint carries both the edit form
// (name, username, image) and the status toggle, which sends status alone.
// A nil field means "leave it as it is".
//
// Email is absent on purpose: the legacy handler accepted it, but the edit
// modal disables the input and the web client never sends it, so honouring it
// would silently allow a rename the UI cannot show.
type UpdateInput struct {
	Name     *string
	Username *string
	Status   *int
	Image    *multipart.FileHeader
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
	List(ctx context.Context, query ListQuery) ([]Cashier, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Cashier, error)
	ExistsByUsername(ctx context.Context, username string, excludeID uuid.UUID) (bool, error)
	ExistsByEmail(ctx context.Context, email string, excludeID uuid.UUID) (bool, error)
	// FindRoleIDByName resolves the role at runtime; the legacy code depended
	// on a role row it never verified.
	FindRoleIDByName(ctx context.Context, name string) (uuid.UUID, error)
	Create(ctx context.Context, row Cashier, passwordHash string, roleID uuid.UUID) (Cashier, error)
	// Update writes only the non-empty values; an empty name, username or photo
	// and a nil status all mean "keep what is stored".
	Update(ctx context.Context, id uuid.UUID, name, username, photo string, status *int) (Cashier, error)
	Delete(ctx context.Context, id uuid.UUID) error
}
