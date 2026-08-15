package devicetoken

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Service holds the business rules.
type Service struct {
	repo Repository
	log  *slog.Logger
	// now is injected so a test can assert LastSeenAt without sleeping.
	now func() time.Time
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// Register records where this account can be reached.
//
// It is deliberately an upsert rather than a create: the app calls it on every
// launch and after every token rotation, and a repeated registration is the
// normal case, not an error worth showing the customer.
func (s *Service) Register(ctx context.Context, registration Registration) (DeviceToken, error) {
	if registration.UserID == uuid.Nil {
		return DeviceToken{}, httpx.Unauthorized("authentication required")
	}

	registration.Token = strings.TrimSpace(registration.Token)
	if registration.Token == "" {
		return DeviceToken{}, httpx.BadRequest("token is required")
	}
	if len(registration.Token) > MaxTokenLength {
		return DeviceToken{}, httpx.BadRequest("token is too long")
	}

	registration.Platform = strings.ToUpper(strings.TrimSpace(registration.Platform))
	if !IsValidPlatform(registration.Platform) {
		return DeviceToken{}, httpx.BadRequest("invalid platform, allowed: ANDROID, IOS, WEB")
	}

	registration.AppVersion = strings.TrimSpace(registration.AppVersion)

	saved, err := s.repo.Save(ctx, registration, s.now())
	if err != nil {
		return DeviceToken{}, httpx.Internal("failed to register device").WithCause(err)
	}

	return saved, nil
}

// Unregister stops delivery to one device. The app calls it on sign-out, so
// whoever holds the phone next does not receive the previous owner's order
// updates.
func (s *Service) Unregister(ctx context.Context, userID uuid.UUID, token string) error {
	if userID == uuid.Nil {
		return httpx.Unauthorized("authentication required")
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return httpx.BadRequest("token is required")
	}

	if err := s.repo.Delete(ctx, userID, token); err != nil {
		if errors.Is(err, ErrNotFound) {
			// Signing out twice, or from a device whose registration was
			// already reassigned, is not a failure worth blocking sign-out
			// for: the desired state -- this account is not reachable at this
			// token -- already holds.
			s.log.Debug("device token already unregistered",
				slog.String("user_id", userID.String()))
			return nil
		}
		return httpx.Internal("failed to unregister device").WithCause(err)
	}

	return nil
}

// List returns the caller's own registrations.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]DeviceToken, error) {
	if userID == uuid.Nil {
		return nil, httpx.Unauthorized("authentication required")
	}

	items, err := s.repo.ListForUser(ctx, userID)
	if err != nil {
		return nil, httpx.Internal("failed to load devices").WithCause(err)
	}
	return items, nil
}
