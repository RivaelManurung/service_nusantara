// Package review exposes the admin-facing side of customer product reviews:
// listing them, reading one, moderating them and deleting them.
//
// It follows internal/modules/typeproduct, the reference CRUD module, minus the
// create and edit halves: writing a review is the customer app's job and that
// endpoint does not exist yet. Nothing here lets an administrator author or
// reword someone else's opinion -- moderation only ever flips visibility or
// removes the row.
package review

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("review not found")

// Status values. The API models status as an integer for consistency with every
// other module; here it reads as hidden/visible rather than inactive/active.
const (
	StatusHidden  = 0
	StatusVisible = 1
)

// Rating bounds, enforced by the service and by a CHECK constraint on the table.
const (
	MinRating = 1
	MaxRating = 5
)

// Review is the response shape. The web client
// (web_nusantara/src/features/review/types.ts) reads exactly these keys, so they
// are fixed by contract rather than by convenience.
type Review struct {
	ID uuid.UUID `json:"id"`

	ProductID uuid.UUID `json:"product_id"`
	// ProductName is joined in: the admin table lists reviews across the whole
	// catalogue, and a column of bare product ids is unreadable.
	ProductName string `json:"product_name"`

	UserID uuid.UUID `json:"user_id"`
	// ReviewerName is the account name, joined from users for the same reason.
	ReviewerName string `json:"reviewer_name"`

	// OrderID is null for a review not tied to a purchase.
	OrderID *uuid.UUID `json:"order_id"`

	Rating  int    `json:"rating"`
	Comment string `json:"comment"`
	Status  int    `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ListQuery is a page of results.
//
// Rating and Status are pointers so "not filtered" is distinguishable from
// "filtered to 0": a plain int would make the hidden-only filter unreachable,
// because its value is the same as the zero value meaning "no filter".
type ListQuery struct {
	Page    int
	PerPage int
	Search  string
	Rating  *int
	Status  *int
}

// Repository is the persistence port, defined next to its consumer and small
// enough to fake in a test without a database.
type Repository interface {
	List(ctx context.Context, query ListQuery) ([]Review, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Review, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status int) error
	Delete(ctx context.Context, id uuid.UUID) error
}
