package customeraddress

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Service holds the business rules.
//
// Every method takes the owner as its first argument after the context. The
// handler resolves that from the verified token and nothing else, so there is
// no path through this package where the caller's identity comes from the
// request body or the URL.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// List returns every address the customer has saved, default first.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Address, error) {
	items, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return nil, httpx.Internal("failed to load addresses").WithCause(err)
	}
	return items, nil
}

// Get returns one of the customer's own addresses.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (Address, error) {
	item, err := s.repo.FindByID(ctx, id, userID)
	if err != nil {
		return Address{}, s.translate(err, "failed to load address")
	}
	return item, nil
}

// GetDefault returns the address checkout should preselect.
func (s *Service) GetDefault(ctx context.Context, userID uuid.UUID) (Address, error) {
	item, err := s.repo.FindDefault(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// A customer who has saved nothing yet is an ordinary state, not a
			// server fault; 404 is what the clients already branch on.
			return Address{}, httpx.NotFound("no default address has been set")
		}
		return Address{}, httpx.Internal("failed to load default address").WithCause(err)
	}
	return item, nil
}

// Create saves a new address. The first one a customer saves becomes their
// default; the repository decides that inside the insert's transaction.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, input Input) (Address, error) {
	created, err := s.repo.Create(ctx, userID, input)
	if err != nil {
		if errors.Is(err, ErrTooMany) {
			return Address{}, httpx.Conflict("you have reached the maximum number of saved addresses")
		}
		return Address{}, httpx.Internal("failed to create address").WithCause(err)
	}
	return created, nil
}

// Update edits one of the customer's own addresses.
//
// The stored row is read first, scoped to the owner, so a partial edit merges
// onto the caller's own values -- and so an address belonging to somebody else
// is a 404 before any write is attempted.
func (s *Service) Update(ctx context.Context, userID, id uuid.UUID, patch Patch) (Address, error) {
	current, err := s.repo.FindByID(ctx, id, userID)
	if err != nil {
		return Address{}, s.translate(err, "failed to load address")
	}

	updated, err := s.repo.Update(ctx, id, userID, patch.Apply(current))
	if err != nil {
		return Address{}, s.translate(err, "failed to update address")
	}
	return updated, nil
}

// Delete removes one of the customer's own addresses, promoting a replacement
// default when the deleted row held the flag.
func (s *Service) Delete(ctx context.Context, userID, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id, userID); err != nil {
		return s.translate(err, "failed to delete address")
	}
	return nil
}

// SetDefault promotes one address and demotes the others.
func (s *Service) SetDefault(ctx context.Context, userID, id uuid.UUID) error {
	if err := s.repo.SetDefault(ctx, id, userID); err != nil {
		return s.translate(err, "failed to set the default address")
	}
	return nil
}

// NearbyShops ranks active shops around a point.
//
// The origin is resolved by the caller: an explicit lat/lng pair when the
// client knows where the device is, otherwise the customer's default address.
func (s *Service) NearbyShops(ctx context.Context, origin Point) ([]NearbyShop, error) {
	if !origin.Valid() {
		return nil, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{
				{Field: "lat", Message: "must be between -90 and 90"},
				{Field: "lng", Message: "must be between -180 and 180"},
			})
	}

	shops, err := s.repo.NearbyShops(ctx, origin, DefaultRadiusKM, MaxNearbyShops)
	if err != nil {
		return nil, httpx.Internal("failed to load nearby shops").WithCause(err)
	}
	return shops, nil
}

// OriginForUser falls back to the customer's default address when the client
// did not send a coordinate.
func (s *Service) OriginForUser(ctx context.Context, userID uuid.UUID) (Point, error) {
	address, err := s.GetDefault(ctx, userID)
	if err != nil {
		return Point{}, err
	}
	return Point{Lat: address.Lat, Lng: address.Lng}, nil
}

// translate turns a repository error into the right HTTP status. ErrNotFound
// covers "no such address" and "not yours" alike, so the response cannot be
// used to probe which ids exist.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("address not found")
	}
	return httpx.Internal(message).WithCause(err)
}
