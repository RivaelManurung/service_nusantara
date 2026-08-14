package typeproduct

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
const imageFolder = "type-products"

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

// List returns one page of categories.
func (s *Service) List(ctx context.Context, query ListQuery) ([]TypeProduct, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load type products").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single category.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (TypeProduct, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return TypeProduct{}, s.translate(err, "failed to load type product")
	}
	return item, nil
}

// Create adds a category. The image is uploaded first: a storage failure must
// not leave a row pointing at a picture that was never stored.
func (s *Service) Create(ctx context.Context, input Input) (TypeProduct, error) {
	taken, err := s.repo.ExistsByName(ctx, input.Name, uuid.Nil)
	if err != nil {
		return TypeProduct{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return TypeProduct{}, httpx.Conflict("a type product with this name already exists")
	}

	uploaded, err := s.uploadImage(ctx, input.Image)
	if err != nil {
		return TypeProduct{}, err
	}

	created, err := s.repo.Create(ctx, TypeProduct{
		ID:     uuid.New(),
		Name:   input.Name,
		Image:  uploaded,
		Status: normalizeStatus(input.Status),
	}, input.CreatedBy)
	if err != nil {
		return TypeProduct{}, httpx.Internal("failed to create type product").WithCause(err)
	}
	return created, nil
}

// Update edits a category. Sending no file keeps the existing image.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (TypeProduct, error) {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		return TypeProduct{}, s.translate(err, "failed to load type product")
	}

	taken, err := s.repo.ExistsByName(ctx, input.Name, id)
	if err != nil {
		return TypeProduct{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return TypeProduct{}, httpx.Conflict("another type product already uses this name")
	}

	image := ""
	if input.Image != nil {
		if image, err = s.uploadImage(ctx, input.Image); err != nil {
			return TypeProduct{}, err
		}
	}

	updated, err := s.repo.Update(ctx, id, input.Name, image)
	if err != nil {
		return TypeProduct{}, s.translate(err, "failed to update type product")
	}
	return updated, nil
}

// SetStatus activates or deactivates a category.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status int) error {
	if err := s.repo.UpdateStatus(ctx, id, normalizeStatus(status)); err != nil {
		return s.translate(err, "failed to update status")
	}
	return nil
}

// Delete removes a category, refusing while products still reference it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	inUse, err := s.repo.InUse(ctx, id)
	if err != nil {
		return httpx.Internal("failed to check whether the type is in use").WithCause(err)
	}
	if inUse {
		// A foreign key violation would surface as a 500; this explains what to
		// do instead.
		return httpx.Conflict("this type still has products; move or remove them first")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete type product")
	}
	return nil
}

func (s *Service) uploadImage(ctx context.Context, file *multipart.FileHeader) (string, error) {
	if file == nil {
		return "", httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "image", Message: "is required"}})
	}

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

// translate turns a repository error into the right HTTP status.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("type product not found")
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
