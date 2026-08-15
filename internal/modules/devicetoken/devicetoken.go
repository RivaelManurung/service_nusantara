// Package devicetoken records where a customer's notifications can be
// delivered: one row per installation of the app, holding the FCM registration
// that installation was issued.
//
// It follows the shape of internal/modules/notification, with the same
// ownership rule: the owner comes from the verified token in the handler and
// never from the request, so nobody can register a device against somebody
// else's account -- which would mean receiving their order updates.
package devicetoken

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no registration matches for this owner.
var ErrNotFound = errors.New("device token not found")

// Platforms are the clients that can register.
const (
	PlatformAndroid = "ANDROID"
	PlatformIOS     = "IOS"
	PlatformWeb     = "WEB"
)

// MaxTokenLength bounds what is accepted. FCM registrations sit around 160
// characters; Google documents no maximum, so the limit is generous but
// present -- without one, the column is an unbounded write from an
// authenticated client.
const MaxTokenLength = 4096

// DeviceToken is the response shape.
//
// The registration itself is deliberately absent: the client already has it,
// and echoing it back would put a credential-shaped value into logs and
// caches for no gain.
type DeviceToken struct {
	ID         uuid.UUID `json:"id"`
	Platform   string    `json:"platform"`
	AppVersion string    `json:"app_version"`
	LastSeenAt time.Time `json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// Registration is one device announcing itself.
type Registration struct {
	UserID     uuid.UUID
	Token      string
	Platform   string
	AppVersion string
}

// Repository is the persistence port.
type Repository interface {
	// Save creates the registration or moves it to this owner, refreshing
	// LastSeenAt either way. It is called on every app start, so it must be
	// idempotent rather than fail on a token it has seen before.
	Save(ctx context.Context, registration Registration, now time.Time) (DeviceToken, error)
	// Delete removes one registration owned by this user. A token belonging to
	// somebody else is reported as ErrNotFound, exactly like one that does not
	// exist, so the endpoint cannot be used to unsubscribe another account's
	// device.
	Delete(ctx context.Context, userID uuid.UUID, token string) error
	// ListForUser backs "signed in on 2 devices" and the tests.
	ListForUser(ctx context.Context, userID uuid.UUID) ([]DeviceToken, error)
}

// IsValidPlatform guards a client supplied value.
func IsValidPlatform(platform string) bool {
	switch platform {
	case PlatformAndroid, PlatformIOS, PlatformWeb:
		return true
	}
	return false
}
