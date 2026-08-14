package event

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/storage"
)

// coverFolder groups this module's uploads in the storage provider.
const coverFolder = "events"

// A percentage discount outside this range is either pointless or free money.
const (
	minDiscountPercent = 1
	maxDiscountPercent = 100
)

// uploader is the slice of the storage port this module needs.
type uploader interface {
	Upload(ctx context.Context, file *multipart.FileHeader, folder string) (storage.Uploaded, error)
}

// reaper removes assets a record no longer points at. It is an interface so a
// test can assert that a replaced cover is actually cleaned up.
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

// List returns one page of events.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Event, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load events").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single event with its child rows.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Event, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Event{}, s.translate(err, "failed to load event")
	}
	return item, nil
}

// Create adds an event together with its child rows. The cover is uploaded
// first: a storage failure must not leave a row pointing at a picture that was
// never stored.
func (s *Service) Create(ctx context.Context, input Input) (Event, error) {
	if err := validate(input); err != nil {
		return Event{}, err
	}

	taken, err := s.repo.ExistsByName(ctx, input.Name, uuid.Nil)
	if err != nil {
		return Event{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return Event{}, httpx.Conflict("an event with this name already exists")
	}

	children, err := s.resolveChildren(ctx, input)
	if err != nil {
		return Event{}, err
	}

	cover, err := s.uploadCover(ctx, input.Cover)
	if err != nil {
		return Event{}, err
	}

	created, err := s.repo.Create(ctx, Event{
		ID:            uuid.New(),
		Name:          input.Name,
		TypeEvent:     input.TypeEvent,
		StartDate:     input.StartDate,
		EndDate:       input.EndDate,
		Cover:         cover.URL,
		CoverPublicID: cover.PublicID,
		Status:        normalizeStatus(input.Status),
	}, children, input.CreatedBy)
	if err != nil {
		// The upload already happened, so a failed insert would strand it.
		s.reaper.Discard(ctx, cover.PublicID)
		return Event{}, httpx.Internal("failed to create event").WithCause(err)
	}
	return created, nil
}

// Update edits an event and replaces its child rows. Sending no file keeps the
// existing cover; the status is left alone, since /edit-status owns it.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (Event, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Event{}, s.translate(err, "failed to load event")
	}

	if err := validate(input); err != nil {
		return Event{}, err
	}

	taken, err := s.repo.ExistsByName(ctx, input.Name, id)
	if err != nil {
		return Event{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return Event{}, httpx.Conflict("another event already uses this name")
	}

	children, err := s.resolveChildren(ctx, input)
	if err != nil {
		return Event{}, err
	}

	var cover storage.Uploaded
	if input.Cover != nil {
		if cover, err = s.uploadCover(ctx, input.Cover); err != nil {
			return Event{}, err
		}
	}

	updated, err := s.repo.Update(ctx, id, Event{
		Name:          input.Name,
		TypeEvent:     input.TypeEvent,
		StartDate:     input.StartDate,
		EndDate:       input.EndDate,
		Cover:         cover.URL,
		CoverPublicID: cover.PublicID,
	}, children)
	if err != nil {
		// The row still points at the old cover, so the new upload is stranded.
		s.reaper.Discard(ctx, cover.PublicID)
		return Event{}, s.translate(err, "failed to update event")
	}

	// Only once the row commits to the new cover is the old one safe to drop.
	if cover.PublicID != "" {
		s.reaper.Discard(ctx, existing.CoverPublicID)
	}
	return updated, nil
}

// SetStatus publishes or hides an event.
func (s *Service) SetStatus(ctx context.Context, id uuid.UUID, status int) error {
	if err := s.repo.UpdateStatus(ctx, id, normalizeStatus(status)); err != nil {
		return s.translate(err, "failed to update status")
	}
	return nil
}

// Delete removes an event and its child rows.
//
// The row is read first because deleting it is what makes its cover
// unreachable: nothing else in the system records the handle, so the only
// moment the asset can still be found is before the row goes.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return s.translate(err, "failed to load event")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete event")
	}

	// The delete is a soft delete, but no endpoint restores an event, so the
	// cover is unreachable from here on and keeping it only leaks storage.
	s.reaper.Discard(ctx, existing.CoverPublicID)
	return nil
}

// validate enforces the rules that cannot be expressed on the form: the event
// type decides which child rows are allowed, and rows of the wrong kind are
// rejected rather than stored. Mixed rows would leave two contradictory
// descriptions of the same promotion in the database.
func validate(input Input) error {
	var fields []httpx.FieldError

	if !input.TypeEvent.Valid() {
		fields = append(fields, httpx.FieldError{
			Field:   "type_event",
			Message: "must be one of: DISKON, BUNDLE",
		})
	}

	if !input.EndDate.After(input.StartDate) {
		fields = append(fields, httpx.FieldError{
			Field:   "end_date",
			Message: "must be after start_date",
		})
	}

	switch input.TypeEvent {
	case TypeDiskon:
		fields = append(fields, validateDiscounts(input.Products)...)
		if len(input.BundleBuys) > 0 {
			fields = append(fields, httpx.FieldError{
				Field:   "event_bundle_buys",
				Message: "must be empty for a DISKON event",
			})
		}
		if len(input.BundleRewards) > 0 {
			fields = append(fields, httpx.FieldError{
				Field:   "event_bundle_rewards",
				Message: "must be empty for a DISKON event",
			})
		}

	case TypeBundle:
		fields = append(fields, validateBundle("event_bundle_buys", input.BundleBuys)...)
		fields = append(fields, validateBundle("event_bundle_rewards", input.BundleRewards)...)
		if len(input.Products) > 0 {
			fields = append(fields, httpx.FieldError{
				Field:   "event_products",
				Message: "must be empty for a BUNDLE event",
			})
		}
	}

	if len(fields) == 0 {
		return nil
	}
	return httpx.Validation("request validation failed").WithDetails(fields)
}

func validateDiscounts(items []ProductDiscountInput) []httpx.FieldError {
	const field = "event_products"
	if len(items) == 0 {
		return []httpx.FieldError{{Field: field, Message: "is required for a DISKON event"}}
	}

	var fields []httpx.FieldError
	seen := map[uuid.UUID]bool{}

	for i, item := range items {
		if item.ProductID == uuid.Nil {
			fields = append(fields, indexed(field, i, "product_id", "is required"))
		} else if seen[item.ProductID] {
			// Two discounts on one product make the price ambiguous.
			fields = append(fields, indexed(field, i, "product_id", "is listed more than once"))
		}
		seen[item.ProductID] = true

		if item.DiscountPercent < minDiscountPercent || item.DiscountPercent > maxDiscountPercent {
			fields = append(fields, indexed(field, i, "discount_percent",
				fmt.Sprintf("must be between %d and %d", minDiscountPercent, maxDiscountPercent)))
		}
	}
	return fields
}

func validateBundle(field string, items []BundleItemInput) []httpx.FieldError {
	if len(items) == 0 {
		return []httpx.FieldError{{Field: field, Message: "is required for a BUNDLE event"}}
	}

	var fields []httpx.FieldError
	seen := map[uuid.UUID]bool{}

	for i, item := range items {
		if item.ProductID == uuid.Nil {
			fields = append(fields, indexed(field, i, "product_id", "is required"))
		} else if seen[item.ProductID] {
			fields = append(fields, indexed(field, i, "product_id", "is listed more than once"))
		}
		seen[item.ProductID] = true

		if item.Quantity < 1 {
			fields = append(fields, indexed(field, i, "quantity", "must be at least 1"))
		}
	}
	return fields
}

func indexed(field string, index int, key, message string) httpx.FieldError {
	return httpx.FieldError{
		Field:   fmt.Sprintf("%s[%d].%s", field, index, key),
		Message: message,
	}
}

// resolveChildren turns the request's product ids into rows to persist, and
// fails when one of them does not exist. The discount amount is derived from
// the product's current price so the stored promotion stays readable even if
// the price later changes.
func (s *Service) resolveChildren(ctx context.Context, input Input) (Children, error) {
	ids := make([]uuid.UUID, 0, len(input.Products)+len(input.BundleBuys)+len(input.BundleRewards))
	for _, item := range input.Products {
		ids = append(ids, item.ProductID)
	}
	for _, item := range input.BundleBuys {
		ids = append(ids, item.ProductID)
	}
	for _, item := range input.BundleRewards {
		ids = append(ids, item.ProductID)
	}

	prices, err := s.repo.ProductPrices(ctx, ids)
	if err != nil {
		return Children{}, httpx.Internal("failed to load products").WithCause(err)
	}

	var missing []httpx.FieldError
	check := func(field string, index int, id uuid.UUID) {
		if _, ok := prices[id]; !ok {
			missing = append(missing, indexed(field, index, "product_id", "does not match a product"))
		}
	}

	children := Children{}

	switch input.TypeEvent {
	case TypeDiskon:
		for i, item := range input.Products {
			check("event_products", i, item.ProductID)
			children.Products = append(children.Products, DiscountRow{
				ProductID:       item.ProductID,
				DiscountPercent: item.DiscountPercent,
				DiscountAmount:  float64(item.DiscountPercent*prices[item.ProductID]) / 100,
			})
		}

	case TypeBundle:
		for i, item := range input.BundleBuys {
			check("event_bundle_buys", i, item.ProductID)
			children.BundleBuys = append(children.BundleBuys, BundleRow{
				ProductID: item.ProductID,
				Quantity:  item.Quantity,
			})
		}
		for i, item := range input.BundleRewards {
			check("event_bundle_rewards", i, item.ProductID)
			children.BundleRewards = append(children.BundleRewards, BundleRow{
				ProductID: item.ProductID,
				Quantity:  item.Quantity,
			})
		}
	}

	if len(missing) > 0 {
		return Children{}, httpx.Validation("request validation failed").WithDetails(missing)
	}
	return children, nil
}

func (s *Service) uploadCover(ctx context.Context, file *multipart.FileHeader) (storage.Uploaded, error) {
	if file == nil {
		return storage.Uploaded{}, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "cover", Message: "is required"}})
	}

	uploaded, err := s.images.Upload(ctx, file, coverFolder)
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
		return httpx.NotFound("event not found")
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
