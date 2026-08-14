package review

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Service holds the business rules. There is no uploader here: a review carries
// no image, so the module needs nothing but its repository.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// List returns one page of reviews.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Review, int64, error) {
	// A filter outside the range every review can hold would silently return an
	// empty page and look like "no reviews yet", so it is refused instead.
	if query.Rating != nil && (*query.Rating < MinRating || *query.Rating > MaxRating) {
		return nil, 0, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "rating", Message: "must be between 1 and 5"}})
	}
	if query.Status != nil && *query.Status != StatusHidden && *query.Status != StatusVisible {
		return nil, 0, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "status", Message: "must be one of: 0, 1"}})
	}

	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load reviews").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single review.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Review, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Review{}, s.translate(err, "failed to load review")
	}
	return item, nil
}

// SetStatus hides or shows a review.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status int) error {
	if err := s.repo.UpdateStatus(ctx, id, normalizeStatus(status)); err != nil {
		return s.translate(err, "failed to update review status")
	}
	return nil
}

// Delete removes a review.
//
// Nothing references a review, so unlike the catalogue modules there is no
// foreign key to guard against; the row goes away and the aggregate counts
// recompute from what is left.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete review")
	}
	return nil
}

// translate turns a repository error into the right HTTP status.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("review not found")
	}
	return httpx.Internal(message).WithCause(err)
}

// normalizeStatus collapses anything that is not "visible" to hidden, so an
// out-of-range integer cannot reach the database.
func normalizeStatus(status int) int {
	if status == StatusVisible {
		return StatusVisible
	}
	return StatusHidden
}
