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

// reaper removes assets a record no longer points at. It is an interface so a
// test can assert that a replaced image is actually cleaned up.
type reaper interface {
	Discard(ctx context.Context, publicID string)
}

// Service holds the business rules.
type Service struct {
	repo   Repository
	images uploader
	reaper reaper
	log    *slog.Logger
}

func NewService(repo Repository, images uploader, reaper reaper, log *slog.Logger) *Service {
	return &Service{repo: repo, images: images, reaper: reaper, log: log}
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
		ID:            uuid.New(),
		Name:          input.Name,
		Image:         uploaded.URL,
		ImagePublicID: uploaded.PublicID,
		Status:        normalizeStatus(input.Status),
	}, input.CreatedBy)
	if err != nil {
		// The upload already happened, so a failed insert would strand it.
		s.reaper.Discard(ctx, uploaded.PublicID)
		return TypeProduct{}, httpx.Internal("failed to create type product").WithCause(err)
	}
	return created, nil
}

// Update edits a category. Sending no file keeps the existing image.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (TypeProduct, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return TypeProduct{}, s.translate(err, "failed to load type product")
	}

	taken, err := s.repo.ExistsByName(ctx, input.Name, id)
	if err != nil {
		return TypeProduct{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return TypeProduct{}, httpx.Conflict("another type product already uses this name")
	}

	var uploaded storage.Uploaded
	if input.Image != nil {
		if uploaded, err = s.uploadImage(ctx, input.Image); err != nil {
			return TypeProduct{}, err
		}
	}

	updated, err := s.repo.Update(ctx, id, input.Name, uploaded.URL, uploaded.PublicID)
	if err != nil {
		// The row still points at the old image, so the new upload is stranded.
		s.reaper.Discard(ctx, uploaded.PublicID)
		return TypeProduct{}, s.translate(err, "failed to update type product")
	}

	// Only once the row commits to the new image is the old one safe to drop.
	if uploaded.PublicID != "" {
		s.reaper.Discard(ctx, existing.ImagePublicID)
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

	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return s.translate(err, "failed to load type product")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete type product")
	}

	s.reaper.Discard(ctx, existing.ImagePublicID)
	return nil
}

func (s *Service) uploadImage(ctx context.Context, file *multipart.FileHeader) (storage.Uploaded, error) {
	if file == nil {
		return storage.Uploaded{}, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "image", Message: "is required"}})
	}

	uploaded, err := s.images.Upload(ctx, file, imageFolder)
	if err != nil {
		if errors.Is(err, storage.ErrNotConfigured) {
			return storage.Uploaded{}, httpx.Unavailable("image uploads are not configured on this server").WithCause(err)
		}
		// A rejected file is the caller's problem; anything else is ours.
		return storage.Uploaded{}, httpx.BadRequest(err.Error()).WithCause(err)
	}
	return uploaded, nil
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
