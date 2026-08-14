// Package event manages promotional events.
//
// An event is one of two kinds. A DISKON event cuts a percentage off each
// listed product; a BUNDLE event gives the reward products away once every buy
// product has been purchased. The two are mutually exclusive: the child rows of
// one kind are meaningless for the other, so a request carrying both is
// rejected rather than silently half-applied.
//
// The package follows internal/modules/typeproduct, the reference CRUD module.
package event

import (
	"context"
	"errors"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("event not found")

// Status values. The API models status as an integer for backwards
// compatibility with the clients already in the field.
const (
	StatusInactive = 0
	StatusActive   = 1
)

// Type is the kind of promotion an event runs.
type Type string

const (
	TypeDiskon Type = "DISKON"
	TypeBundle Type = "BUNDLE"
)

// Valid reports whether the type is one the service knows how to apply.
func (t Type) Valid() bool { return t == TypeDiskon || t == TypeBundle }

// Product is the slice of a product an event row carries. The web client reads
// exactly these keys, and accepts `image` as a bare URL.
type Product struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Code  string    `json:"code"`
	Price int       `json:"price"`
	Unit  string    `json:"unit"`
	Image string    `json:"image"`
}

// ProductDiscount is one line of a DISKON event.
type ProductDiscount struct {
	ID              uuid.UUID `json:"id"`
	Product         Product   `json:"product"`
	DiscountPercent int       `json:"discount_percent"`
	// DiscountAmount is the rupiah value of the percentage at the price the
	// product had when the event was saved, kept so a later price change cannot
	// silently rewrite a running promotion.
	DiscountAmount float64 `json:"discount_amount"`
}

// BundleItem is one line of a BUNDLE event, on either the buy or reward side.
type BundleItem struct {
	ID       uuid.UUID `json:"id"`
	Product  Product   `json:"product"`
	Quantity int       `json:"quantity"`
}

// Event is the response shape. The singular child keys (`event_product`,
// `event_bundle_buy`, `event_bundle_reward`) are the ones the web client reads,
// so they are fixed by contract rather than by convenience.
type Event struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	TypeEvent Type      `json:"type_event"`
	StartDate time.Time `json:"start_date"`
	EndDate   time.Time `json:"end_date"`
	Cover     string    `json:"cover"`
	Status    int       `json:"status"`

	// CoverPublicID is the storage handle, never part of the client contract.
	// It exists so a replaced or deleted cover can actually be removed: the URL
	// alone cannot address the file in the provider.
	CoverPublicID string `json:"-"`

	EventProducts      []ProductDiscount `json:"event_product"`
	EventBundleBuys    []BundleItem      `json:"event_bundle_buy"`
	EventBundleRewards []BundleItem      `json:"event_bundle_reward"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProductDiscountInput is one entry of the `event_products` JSON array.
type ProductDiscountInput struct {
	ProductID       uuid.UUID `json:"product_id"`
	DiscountPercent int       `json:"discount_percent"`
}

// BundleItemInput is one entry of `event_bundle_buys` or `event_bundle_rewards`.
type BundleItemInput struct {
	ProductID uuid.UUID `json:"product_id"`
	Quantity  int       `json:"quantity"`
}

// Input is a create or update request, already parsed.
type Input struct {
	Name      string
	TypeEvent Type
	StartDate time.Time
	EndDate   time.Time
	Status    int
	// Cover is nil when editing without replacing the picture.
	Cover *multipart.FileHeader

	Products      []ProductDiscountInput
	BundleBuys    []BundleItemInput
	BundleRewards []BundleItemInput

	// CreatedBy is the acting user, taken from the token rather than the body.
	CreatedBy uuid.UUID
}

// ListQuery is a page of results.
type ListQuery struct {
	Page    int
	PerPage int
	Search  string
}

// DiscountRow is a resolved DISKON line ready to persist.
type DiscountRow struct {
	ProductID       uuid.UUID
	DiscountPercent int
	DiscountAmount  float64
}

// BundleRow is a resolved BUNDLE line ready to persist.
type BundleRow struct {
	ProductID uuid.UUID
	Quantity  int
}

// Children is the complete set of child rows an event owns. It is written in
// the same transaction as the event, and replaces -- never appends to -- what
// was there before, so a DISKON event cannot keep stale bundle rows after being
// edited.
type Children struct {
	Products      []DiscountRow
	BundleBuys    []BundleRow
	BundleRewards []BundleRow
}

// Repository is the persistence port, defined next to its consumer and small
// enough to fake in a test without a database.
type Repository interface {
	List(ctx context.Context, query ListQuery) ([]Event, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Event, error)
	ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error)
	// ProductPrices returns the price of every product that exists; an id
	// missing from the result is an id that does not exist.
	ProductPrices(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error)
	// Create writes the event and its children in one transaction.
	Create(ctx context.Context, row Event, children Children, createdBy uuid.UUID) (Event, error)
	// Update rewrites the event and replaces every child row, in one
	// transaction. An empty Cover keeps the stored one, and the public id moves
	// with it so the handle can never point at an asset the row no longer shows.
	Update(ctx context.Context, id uuid.UUID, row Event, children Children) (Event, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status int) error
	Delete(ctx context.Context, id uuid.UUID) error
}
