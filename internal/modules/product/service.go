package product

import (
	"context"
	"errors"
	"log/slog"
	"mime/multipart"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/storage"
)

// imageFolder groups this module's uploads in the storage provider.
const imageFolder = "products"

// uploader is the slice of the storage port this module needs.
type uploader interface {
	Upload(ctx context.Context, file *multipart.FileHeader, folder string) (storage.Uploaded, error)
}

// Service holds the business rules.
type Service struct {
	repo   Repository
	images uploader
	log    *slog.Logger
}

func NewService(repo Repository, images uploader, log *slog.Logger) *Service {
	return &Service{repo: repo, images: images, log: log}
}

// List returns one page of products.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Product, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load products").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single product.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Product, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Product{}, s.translate(err, "failed to load product")
	}
	return item, nil
}

// Create adds a product. Both the cover and the gallery are uploaded before
// anything is written, so a storage failure cannot leave a row pointing at an
// image that was never stored.
func (s *Service) Create(ctx context.Context, input Input) (Product, error) {
	if err := s.checkName(ctx, input.Name, uuid.Nil); err != nil {
		return Product{}, err
	}
	if err := s.checkTypeProduct(ctx, input.TypeProduct); err != nil {
		return Product{}, err
	}

	if input.Cover == nil {
		return Product{}, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "cover", Message: "is required"}})
	}

	cover, err := s.upload(ctx, input.Cover)
	if err != nil {
		return Product{}, err
	}
	gallery, err := s.uploadAll(ctx, input.Gallery)
	if err != nil {
		return Product{}, err
	}

	created, err := s.repo.Create(ctx, Write{
		Name:        input.Name,
		Code:        input.Code,
		Price:       input.Price,
		Unit:        input.Unit,
		Description: input.Description,
		Status:      normalizeStatus(input.Status),
		TypeProduct: input.TypeProduct,
		CreatedBy:   input.CreatedBy,
		CoverURL:    cover,
		GalleryURLs: gallery,
	})
	if err != nil {
		return Product{}, s.translate(err, "failed to create product")
	}
	return created, nil
}

// Update edits a product. Sending no cover keeps the existing one, and the
// gallery is only touched when the client asks for it to be replaced.
//
// Status is deliberately not part of an edit: the list screen toggles it
// through the dedicated edit-status endpoint, and accepting it here would let
// a form that omits the field silently deactivate a product.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (Product, error) {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		return Product{}, s.translate(err, "failed to load product")
	}
	if err := s.checkName(ctx, input.Name, id); err != nil {
		return Product{}, err
	}
	if err := s.checkTypeProduct(ctx, input.TypeProduct); err != nil {
		return Product{}, err
	}

	cover := ""
	if input.Cover != nil {
		var err error
		if cover, err = s.upload(ctx, input.Cover); err != nil {
			return Product{}, err
		}
	}

	gallery, err := s.uploadAll(ctx, input.Gallery)
	if err != nil {
		return Product{}, err
	}

	updated, err := s.repo.Update(ctx, id, Write{
		Name:           input.Name,
		Code:           input.Code,
		Price:          input.Price,
		Unit:           input.Unit,
		Description:    input.Description,
		TypeProduct:    input.TypeProduct,
		CoverURL:       cover,
		GalleryURLs:    gallery,
		ReplaceGallery: input.ReplaceGallery,
	})
	if err != nil {
		return Product{}, s.translate(err, "failed to update product")
	}
	return updated, nil
}

// SetStatus activates or deactivates a product.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status int) error {
	if err := s.repo.UpdateStatus(ctx, id, normalizeStatus(status)); err != nil {
		return s.translate(err, "failed to update status")
	}
	return nil
}

// Delete removes a product together with its gallery links.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete product")
	}
	return nil
}

// checkName refuses a name another product already uses.
func (s *Service) checkName(ctx context.Context, name string, excludeID uuid.UUID) error {
	taken, err := s.repo.ExistsByName(ctx, name, excludeID)
	if err != nil {
		return httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return httpx.Conflict("a product with this name already exists")
	}
	return nil
}

// checkTypeProduct turns an unknown category into a 404 rather than letting
// the insert fail on a foreign key.
func (s *Service) checkTypeProduct(ctx context.Context, id uuid.UUID) error {
	exists, err := s.repo.TypeProductExists(ctx, id)
	if err != nil {
		return httpx.Internal("failed to verify the product type").WithCause(err)
	}
	if !exists {
		return httpx.NotFound("type product not found")
	}
	return nil
}

func (s *Service) upload(ctx context.Context, file *multipart.FileHeader) (string, error) {
	uploaded, err := s.images.Upload(ctx, file, imageFolder)
	if err != nil {
		if errors.Is(err, storage.ErrNotConfigured) {
			return "", httpx.Unavailable("image uploads are not configured on this server").WithCause(err)
		}
		// A rejected file is the caller's problem; anything else is ours.
		return "", httpx.BadRequest(err.Error()).WithCause(err)
	}
	return uploaded.URL, nil
}

// uploadAll stores a gallery in order. It stops at the first failure: the
// caller has not written anything yet, so nothing has to be undone.
func (s *Service) uploadAll(ctx context.Context, files []*multipart.FileHeader) ([]string, error) {
	if len(files) == 0 {
		return nil, nil
	}

	urls := make([]string, 0, len(files))
	for _, file := range files {
		url, err := s.upload(ctx, file)
		if err != nil {
			return nil, err
		}
		urls = append(urls, url)
	}
	return urls, nil
}

// translate turns a repository error into the right HTTP status.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("product not found")
	}
	return httpx.Internal(message).WithCause(err)
}

// normalizeStatus collapses anything that is not "active" to inactive, so an
// out-of-range integer cannot reach the database.
func normalizeStatus(status int) int {
	if status == StatusActive {
		return StatusActive
	}
	return StatusInactive
}
