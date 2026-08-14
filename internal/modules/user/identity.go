package user

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Identity is one sign-in method attached to an account.
type Identity struct {
	ID       uuid.UUID
	UserID   uuid.UUID
	Provider string
	Subject  string
	Email    string
}

// IdentityRepository stores the link between a provider account and a local one.
type IdentityRepository interface {
	// FindBySubject resolves a provider identity to the account it belongs to.
	FindBySubject(ctx context.Context, provider, subject string) (Identity, error)
	// Link attaches a new sign-in method to an existing account.
	Link(ctx context.Context, identity Identity) (Identity, error)
	// TouchLogin records that this identity was just used.
	TouchLogin(ctx context.Context, id uuid.UUID, at time.Time) error
	// ListForUser returns every method an account can sign in with, so a client
	// can show "you signed up with Google" and warn before unlinking the last one.
	ListForUser(ctx context.Context, userID uuid.UUID) ([]Identity, error)
}

// SocialProfile is what a verified provider token tells us about a person.
type SocialProfile struct {
	Provider      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
}
