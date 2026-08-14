// Package shop manages outlets: their details, gallery, assigned staff and
// per-shop stock, plus the read-only endpoints a signed-in cashier uses to see
// the shops they work at.
//
// It follows the shape set out by internal/modules/typeproduct: one package
// holds the response types, the persistence port, the business rules and the
// HTTP handlers.
package shop

import (
	"context"
	"errors"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches, so the service layer never has
// to import GORM to recognise a miss.
var ErrNotFound = errors.New("shop not found")

// Status values. The API models status as an integer for the clients already in
// the field; 1 means active.
const (
	StatusInactive = 0
	StatusActive   = 1
)

// Image is the nested picture the web client reads off a shop's product.
type Image struct {
	ImagePath string `json:"image_path"`
}

// ShopProduct is one product stocked by a shop.
//
// ID is the *product's* id, not the join row's: the edit form sends it straight
// back as `product_id`, so returning the join id would make an edit lose every
// product line.
type ShopProduct struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Code   string    `json:"code"`
	Price  float64   `json:"price"`
	Stock  int       `json:"stock"`
	Status int       `json:"status"`
	Image  *Image    `json:"image"`
}

// Cashier is a staff member assigned to a shop.
type Cashier struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Photo    string    `json:"photo"`
	Status   int       `json:"status"`
}

// Shop is the response shape for the admin CRUD endpoints. Every key here is
// fixed by web_nusantara/src/features/shop/types.ts.
type Shop struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Cover       string    `json:"cover"`
	Description string    `json:"description"`
	FullAddress string    `json:"full_address"`
	Lat         float64   `json:"lat"`
	// Lng is spelled "lang" on the wire. It is a typo in the original API that
	// every client now depends on, so it is preserved deliberately.
	Lng    float64 `json:"lang"`
	Status int     `json:"status"`
	// CreatedBy is the creator's display name, which is all the UI shows.
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	ShopImages   []string      `json:"shop_images"`
	ShopProducts []ShopProduct `json:"shop_product"`
	ShopCashiers []Cashier     `json:"shop_cashier"`
}

// AssignedShop is one entry of GET /cashier/shop-names.
type AssignedShop struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// CashierShop is GET /cashier/shop-cashier/{shop_id}: the same shop without the
// staff list, which a cashier has no business reading.
type CashierShop struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Cover       string    `json:"cover"`
	Description string    `json:"description"`
	FullAddress string    `json:"full_address"`
	Lat         float64   `json:"lat"`
	Lng         float64   `json:"lang"`
	Status      int       `json:"status"`
	ShopImages  []string  `json:"shop_images"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CashierProduct is one entry of GET /cashier/cashier-shop-product/{shop_id}:
// the catalogue product flattened together with this shop's price and stock.
type CashierProduct struct {
	ID            uuid.UUID `json:"id"`
	Name          string    `json:"name"`
	Image         string    `json:"image"`
	Code          string    `json:"code"`
	Price         float64   `json:"price"`
	Unit          string    `json:"unit"`
	Stock         int       `json:"stock"`
	Description   string    `json:"description"`
	Status        int       `json:"status"`
	TypeProduct   string    `json:"type_product"`
	ProductImages []string  `json:"product_images"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ProductAssignment is one element of the `products` JSON array sent in the
// multipart body. Price and Status are pointers because "not sent" means
// "inherit the catalogue's value", which is different from zero.
type ProductAssignment struct {
	ProductID uuid.UUID `json:"product_id"`
	Stock     int       `json:"stock"`
	Price     *float64  `json:"price,omitempty"`
	Status    *int      `json:"status,omitempty"`
}

// Input is a create or update request, already parsed out of the multipart
// body. The string fields are empty and the pointers nil when the caller did
// not send them, which on update means "leave this alone".
type Input struct {
	Name        string
	Description string
	FullAddress string
	Lat         *float64
	Lng         *float64
	Status      int

	// Cover is nil when editing without replacing the picture.
	Cover   *multipart.FileHeader
	Gallery []*multipart.FileHeader
	// ReplaceGallery discards the existing gallery instead of appending.
	ReplaceGallery bool

	// CashierIDs/Products are only applied when their Set* flag is true, so
	// omitting the field leaves the existing assignment untouched while sending
	// an empty array clears it.
	CashierIDs  []uuid.UUID
	SetCashiers bool
	Products    []ProductAssignment
	SetProducts bool

	// CreatedBy is the acting user, taken from the token rather than the body.
	CreatedBy uuid.UUID
}

// ListQuery is a page of results.
type ListQuery struct {
	Page    int
	PerPage int
	Search  string
}

// Record is the shop row a write persists. Empty strings and nil pointers mean
// "keep what is already stored", which is how a partial edit is expressed
// without a second struct.
type Record struct {
	Name        string
	Description string
	FullAddress string
	Lat         *float64
	Lng         *float64
	Status      int
	Cover       string
	CreatedBy   uuid.UUID
}

// ProductLine is a fully resolved shop_products row: the caller's overrides
// have already been merged with the catalogue's defaults.
type ProductLine struct {
	ProductID uuid.UUID
	Price     float64
	Stock     int
	Status    int
}

// Assignments is everything a write replaces alongside the shop row. They travel
// together so the repository can commit them in one transaction: a shop whose
// staff were saved but whose stock was not is worse than a failed request.
type Assignments struct {
	Gallery        []string
	ReplaceGallery bool

	Cashiers    []uuid.UUID
	SetCashiers bool

	Products    []ProductLine
	SetProducts bool
}

// ProductDefaults is the catalogue price and status a shop inherits when the
// request does not override them.
type ProductDefaults struct {
	Price  float64
	Status int
}

// Repository is the persistence port, small enough to fake in a test without a
// database.
type Repository interface {
	List(ctx context.Context, query ListQuery) ([]Shop, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Shop, error)
	Create(ctx context.Context, record Record, assign Assignments) (Shop, error)
	Update(ctx context.Context, id uuid.UUID, record Record, assign Assignments) (Shop, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status int) error
	Delete(ctx context.Context, id uuid.UUID) error

	// HasOrders reports whether orders still point at this shop, so the delete
	// can explain itself instead of surfacing a foreign key violation.
	HasOrders(ctx context.Context, id uuid.UUID) (bool, error)

	// StaffIDs returns the subset of ids that are real, live accounts holding a
	// role allowed to run a till.
	StaffIDs(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error)
	// ProductDefaults returns the catalogue price and status per product id;
	// ids with no row are absent from the map.
	ProductDefaults(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]ProductDefaults, error)

	// AssignedShops lists the shops this staff member works at.
	AssignedShops(ctx context.Context, staffID uuid.UUID) ([]AssignedShop, error)
	// IsAssigned is the authorisation check behind every cashier-scoped read.
	IsAssigned(ctx context.Context, staffID, shopID uuid.UUID) (bool, error)
	AssignedShop(ctx context.Context, staffID, shopID uuid.UUID) (CashierShop, error)
	AssignedShopProducts(ctx context.Context, staffID, shopID uuid.UUID) ([]CashierProduct, error)
}
