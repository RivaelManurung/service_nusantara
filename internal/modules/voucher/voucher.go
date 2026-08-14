// Package voucher manages redeemable discount codes.
//
// Unlike the catalogue modules a voucher carries no image, so its endpoints
// take plain JSON rather than multipart. It otherwise follows
// internal/modules/typeproduct, the reference CRUD module.
package voucher

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("voucher not found")

// Status values. The API models status as an integer for backwards
// compatibility with the clients already in the field.
const (
	StatusInactive = 0
	StatusActive   = 1
)

// Discount kinds. A voucher cuts either a fixed rupiah amount or a percentage,
// never both.
const (
	DiscountAmount  = "amount"
	DiscountPercent = "percent"
)

// Voucher is the response shape. The web client reads exactly these keys, so
// they are fixed by contract rather than by convenience.
type Voucher struct {
	ID              uuid.UUID `json:"id"`
	Code            string    `json:"code"`
	DiscountType    string    `json:"discount_type"`
	DiscountAmount  int       `json:"discount_amount"`
	DiscountPercent int       `json:"discount_percent"`
	MinimumSpend    int       `json:"minimum_spend"`
	PointCost       int       `json:"point_cost"`
	StartDate       time.Time `json:"start_date"`
	EndDate         time.Time `json:"end_date"`
	Quota           int       `json:"quota"`
	// ClaimedCount is maintained by the redemption flow, not by this module.
	ClaimedCount int    `json:"claimed_count"`
	Description  string `json:"description"`
	Status       int    `json:"status"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is a create or update request, already parsed and validated.
type Input struct {
	Code            string
	DiscountType    string
	DiscountAmount  int
	DiscountPercent int
	MinimumSpend    int
	PointCost       int
	Quota           int
	StartDate       time.Time
	EndDate         time.Time
	Description     string
	Status          int
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
	List(ctx context.Context, query ListQuery) ([]Voucher, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Voucher, error)
	// ExistsByCode reports whether the code is taken, so a duplicate is a 409
	// rather than a unique-constraint error from the driver.
	ExistsByCode(ctx context.Context, code string, excludeID uuid.UUID) (bool, error)
	Create(ctx context.Context, row Voucher, createdBy uuid.UUID) (Voucher, error)
	Update(ctx context.Context, id uuid.UUID, row Voucher) (Voucher, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status int) error
	Delete(ctx context.Context, id uuid.UUID) error
	// Claimed reports whether anyone already holds this voucher, so deleting it
	// out from under them can be refused with an explanation.
	Claimed(ctx context.Context, id uuid.UUID) (bool, error)
}
