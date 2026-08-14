// Package product manages the catalogue's sellable items.
//
// It follows the shape of internal/modules/typeproduct: one package holds the
// response shape, the persistence port, the business rules and the HTTP
// handlers. What is different here is that a product carries two kinds of
// image -- one cover and a gallery -- so the repository writes several tables
// at once and does so inside a transaction.
package product

import (
	"context"
	"errors"
	"mime/multipart"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no row matches. Callers compare against this
// rather than gorm.ErrRecordNotFound, so the service layer never imports GORM.
var ErrNotFound = errors.New("product not found")

// Status values. The API models status as an integer for backwards
// compatibility with the clients already in the field.
const (
	StatusInactive = 0
	StatusActive   = 1
)

// MaxGalleryFiles bounds how many gallery images one request may carry, so a
// single upload cannot occupy the storage provider indefinitely.
const MaxGalleryFiles = 10

// Image is the nested image object. The web client reads `image.image_path`
// rather than a flat string, because the previous service serialised the GORM
// entity directly; keeping the nesting keeps the UI working.
type Image struct {
	ImagePath string `json:"image_path"`

	// PublicID is the storage handle, never part of the client contract. It
	// exists so a replaced or deleted image can actually be removed instead of
	// being orphaned in the provider forever.
	PublicID string `json:"-"`
}

// TypeProductRef is the category as embedded in a product response.
type TypeProductRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// GalleryImage is one row of the product_images join, shaped as the client
// reads it: `product_images[].image.image_path`.
type GalleryImage struct {
	Image *Image `json:"image"`
}

// Creator is the acting user as embedded in a product response; only the name
// is exposed, because that is all the UI shows and everything else about an
// account is nobody else's business.
type Creator struct {
	Name string `json:"name"`
}

// Product is the response shape. Every key here is fixed by
// web_nusantara/src/features/product/types.ts -- renaming one silently breaks
// the catalogue screen.
type Product struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	Code          string          `json:"code"`
	Price         int             `json:"price"`
	Unit          string          `json:"unit"`
	Description   string          `json:"description"`
	Status        int             `json:"status"`
	Image         *Image          `json:"image"`
	TypeProduct   *TypeProductRef `json:"type_product"`
	ProductImages []GalleryImage  `json:"product_images"`
	CreatedBy     *Creator        `json:"created_by"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Input is a create or update request, already parsed out of the multipart
// body but not yet uploaded or validated against the database.
type Input struct {
	Name        string
	Code        string
	Price       int
	Unit        string
	Description string
	Status      int
	TypeProduct uuid.UUID

	// Cover is nil when editing without replacing the cover image.
	Cover *multipart.FileHeader
	// Gallery are the newly uploaded gallery files.
	Gallery []*multipart.FileHeader
	// ReplaceGallery drops the stored gallery before linking Gallery. It is
	// false on create, and on update means "here is the whole gallery again":
	// there is no per-image delete endpoint.
	ReplaceGallery bool

	// CreatedBy is the acting user, taken from the token rather than the body.
	CreatedBy uuid.UUID
}

// UploadedImage is an asset that already reached the storage provider: the URL
// the client renders plus the handle that is the only way to delete it later.
// Both travel together so a row can never store one without the other.
type UploadedImage struct {
	URL      string
	PublicID string
}

// Write is what the repository persists: the scalar columns plus the images the
// service has already uploaded. Uploading first means a storage failure never
// leaves a row pointing at an image that was never stored.
type Write struct {
	Name        string
	Code        string
	Price       int
	Unit        string
	Description string
	Status      int
	TypeProduct uuid.UUID
	CreatedBy   uuid.UUID

	// Cover is zero on update when the cover is unchanged.
	Cover UploadedImage
	// Gallery are new images to link to the product.
	Gallery []UploadedImage
	// ReplaceGallery unlinks the stored gallery before linking Gallery.
	ReplaceGallery bool
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
	List(ctx context.Context, query ListQuery) ([]Product, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Product, error)
	ExistsByName(ctx context.Context, name string, excludeID uuid.UUID) (bool, error)
	// TypeProductExists reports whether the referenced category is present, so
	// a bad type_product_id becomes a 404 instead of a foreign key violation.
	TypeProductExists(ctx context.Context, id uuid.UUID) (bool, error)
	Create(ctx context.Context, write Write) (Product, error)
	Update(ctx context.Context, id uuid.UUID, write Write) (Product, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status int) error
	// Delete removes the product together with its gallery links, so no
	// product_images row is left pointing at a product that is gone.
	Delete(ctx context.Context, id uuid.UUID) error
}
