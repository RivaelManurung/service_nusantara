package user

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no record matches the lookup. Callers compare
// against this rather than against gorm.ErrRecordNotFound, so the service layer
// stays free of persistence details.
var ErrNotFound = errors.New("record not found")

// Account is the domain view of a user row.
//
// Username, Email, Phone and PasswordHash are empty when absent: a customer who
// signed up by phone has no email, and one who signed in with Google has no
// password. The repository maps empty to NULL.
type Account struct {
	ID            uuid.UUID
	Name          string
	Username      string
	Email         string
	EmailVerified bool
	Phone         string
	PhoneVerified bool
	Photo         string
	PasswordHash  string
	RoleID        uuid.UUID
	RoleName      string
	Status        int
}

// CanSignInWithPassword reports whether this account has a password set. A
// social-only account has none, and must not be able to "log in" with an empty
// one.
func (a Account) CanSignInWithPassword() bool { return a.PasswordHash != "" }

// Repository is the persistence port. It is defined here, next to its consumer,
// and kept small enough to fake in a unit test without a database.
type Repository interface {
	Create(ctx context.Context, account Account) (Account, error)
	FindByID(ctx context.Context, id uuid.UUID) (Account, error)
	FindByEmail(ctx context.Context, email string) (Account, error)
	FindByPhone(ctx context.Context, phone string) (Account, error)
	ExistsByUsernameOrEmail(ctx context.Context, username, email string) (bool, error)
	RoleExists(ctx context.Context, roleID uuid.UUID) (bool, error)
	FindRoleIDByName(ctx context.Context, name string) (uuid.UUID, error)
	UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error
	MarkEmailVerified(ctx context.Context, id uuid.UUID) error
}

// toProfile converts an account into the client-facing read model.
func (a Account) toProfile(createdAt time.Time) Profile {
	return Profile{
		ID:            a.ID.String(),
		Name:          a.Name,
		Username:      a.Username,
		Email:         a.Email,
		EmailVerified: a.EmailVerified,
		Phone:         a.Phone,
		PhoneVerified: a.PhoneVerified,
		Photo:         a.Photo,
		Role:          a.RoleName,
		Status:        a.Status,
		CreatedAt:     createdAt,
	}
}
