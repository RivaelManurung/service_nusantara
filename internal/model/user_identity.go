package model

import (
	"time"

	"github.com/google/uuid"
)

// Sign-in providers. Provider is stored as text rather than an enum so adding
// one is a code change, not a migration.
const (
	ProviderPassword = "password"
	ProviderPhone    = "phone"
	ProviderGoogle   = "google"
	ProviderApple    = "apple"
)

// UserIdentity links one sign-in method to one account.
//
// Modelling identities as rows rather than columns on User is what lets a
// single person sign in with Google today, add a password tomorrow, and keep
// one order history throughout.
type UserIdentity struct {
	ID     uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`
	UserID uuid.UUID `gorm:"type:uuid;not null;index" json:"user_id"`
	User   User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE" json:"-"`

	// Provider plus Subject is the natural key. Subject is whatever the
	// provider considers stable: the `sub` claim for Google and Apple, the
	// E.164 number for phone, the email address for password sign-in.
	Provider string `gorm:"type:varchar(32);not null;uniqueIndex:idx_identity_provider_subject" json:"provider"`
	Subject  string `gorm:"type:varchar(255);not null;uniqueIndex:idx_identity_provider_subject" json:"subject"`

	// Email as the provider reported it, which may differ from User.Email (an
	// Apple private relay address, for example).
	Email string `gorm:"type:varchar(255)" json:"email,omitempty"`

	LastLoginAt *time.Time `json:"last_login_at,omitempty"`
	CreatedAt   time.Time  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"autoUpdateTime" json:"updated_at"`
}
