package banner

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
const imageFolder = "banners"

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

// List returns one page of banners for the back office.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Banner, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load banners").WithCause(err)
	}
	return items, total, nil
}

// ListPublic returns the active banners for the storefront carousel.
func (s *Service) ListPublic(ctx context.Context) ([]Banner, error) {
	items, err := s.repo.ListActive(ctx)
	if err != nil {
		return nil, httpx.Internal("failed to load banners").WithCause(err)
	}
	return items, nil
}

// Get returns a single banner.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Banner, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Banner{}, s.translate(err, "failed to load banner")
	}
	return item, nil
}

// GetPublic returns a single banner to an anonymous caller. An inactive banner
// is reported as missing rather than as forbidden, so switching one off also
// hides that it ever existed.
func (s *Service) GetPublic(ctx context.Context, id uuid.UUID) (Banner, error) {
	item, err := s.repo.FindActiveByID(ctx, id)
	if err != nil {
		return Banner{}, s.translate(err, "failed to load banner")
	}
	return item, nil
}

// Create adds a banner. The image is uploaded first: a storage failure must not
// leave a row pointing at artwork that was never stored.
func (s *Service) Create(ctx context.Context, input Input) (Banner, error) {
	taken, err := s.repo.ExistsByName(ctx, input.Name, uuid.Nil)
	if err != nil {
		return Banner{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return Banner{}, httpx.Conflict("a banner with this name already exists")
	}

	uploaded, err := s.uploadImage(ctx, input.Image)
	if err != nil {
		return Banner{}, err
	}

	created, err := s.repo.Create(ctx, Banner{
		ID:            uuid.New(),
		Name:          input.Name,
		Photo:         uploaded.URL,
		PhotoPublicID: uploaded.PublicID,
		Description:   input.Description,
		Status:        normalizeStatus(input.Status),
	}, input.CreatedBy)
	if err != nil {
		// The upload already happened, so a failed insert would strand it.
		s.reaper.Discard(ctx, uploaded.PublicID)
		return Banner{}, httpx.Internal("failed to create banner").WithCause(err)
	}
	return created, nil
}

// Update edits a banner. Sending no file keeps the existing artwork, and the
// status is deliberately untouched here: it moves only through SetStatus, which
// is what /banner/{id}/edit-status exists for.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (Banner, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Banner{}, s.translate(err, "failed to load banner")
	}

	taken, err := s.repo.ExistsByName(ctx, input.Name, id)
	if err != nil {
		return Banner{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return Banner{}, httpx.Conflict("another banner already uses this name")
	}

	var uploaded storage.Uploaded
	if input.Image != nil {
		if uploaded, err = s.uploadImage(ctx, input.Image); err != nil {
			return Banner{}, err
		}
	}

	updated, err := s.repo.Update(ctx, id, input.Name, input.Description, uploaded.URL, uploaded.PublicID)
	if err != nil {
		// The row still points at the old image, so the new upload is stranded.
		s.reaper.Discard(ctx, uploaded.PublicID)
		return Banner{}, s.translate(err, "failed to update banner")
	}

	// Only once the row commits to the new image is the old one safe to drop.
	if uploaded.PublicID != "" {
		s.reaper.Discard(ctx, existing.PhotoPublicID)
	}
	return updated, nil
}

// SetStatus shows or hides a banner.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status int) error {
	if err := s.repo.UpdateStatus(ctx, id, normalizeStatus(status)); err != nil {
		return s.translate(err, "failed to update status")
	}
	return nil
}

// Delete removes a banner. Nothing references a banner, so there is no
// foreign key to guard here.
//
// The row is read before it is removed, because afterwards there is nothing
// left to learn the storage handle from.
//
// model.Banner carries gorm.DeletedAt, so this is a soft delete and the row
// technically survives. The artwork is still discarded: no read path in this
// module is Unscoped and there is no restore endpoint anywhere in the service,
// so keeping the file would mean paying to store an image no request can ever
// reach again. This matches typeproduct, which soft-deletes and discards too.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return s.translate(err, "failed to load banner")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete banner")
	}

	s.reaper.Discard(ctx, existing.PhotoPublicID)
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
		return httpx.NotFound("banner not found")
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
