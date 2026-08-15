// Package customer is the back office's view of accounts: who they are, what
// they have bought, and whether they may still sign in.
//
// It is separate from internal/modules/user, which owns authentication -- an
// account acting on itself (login, profile, password). This module is staff
// acting on somebody else's account, which is a different trust boundary and a
// different set of rules: every read is permission-guarded, and every block is
// recorded with a reason and an actor.
package customer

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when no account matches.
var ErrNotFound = errors.New("account not found")

// Status values, matching internal/modules/user.StatusActive.
const (
	StatusBlocked = 0
	StatusActive  = 1
)

// MaxReasonRunes bounds the moderation reason, as the order module bounds its
// cancellation reason.
const MaxReasonRunes = 500

// Summary is one row of the account list.
type Summary struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Phone    string    `json:"phone"`
	Photo    string    `json:"photo"`
	Role     string    `json:"role"`
	Status   int       `json:"status"`

	EmailVerified bool `json:"email_verified"`
	PhoneVerified bool `json:"phone_verified"`

	// OrderCount and TotalSpend answer "is this account worth anything?" at a
	// glance, which is the question an operator is actually asking when they
	// look at a customer list. Spend counts only revenue statuses, so a
	// cancelled order never inflates it.
	OrderCount int64   `json:"order_count"`
	TotalSpend float64 `json:"total_spend"`

	CreatedAt time.Time `json:"created_at"`
}

// Detail is one account in full.
type Detail struct {
	Summary

	Gender      string     `json:"gender"`
	DateOfBirth *time.Time `json:"date_of_birth"`

	// PointBalance is the cached total from user_points. It is reported as-is
	// and deliberately NOT reconciled here -- the point module owns that, and
	// two places computing a balance is how they come to disagree.
	PointBalance int64 `json:"point_balance"`

	// VoucherClaimed and VoucherUsed make voucher abuse visible without opening
	// a second screen: a large gap between them is the shape of hoarding.
	VoucherClaimed int64 `json:"voucher_claimed"`
	VoucherUsed    int64 `json:"voucher_used"`

	// LastOrderAt is nil for an account that has never ordered.
	LastOrderAt *time.Time `json:"last_order_at"`

	// Moderation is the account's block history, newest first. Empty for an
	// account nobody has ever acted on.
	Moderation []ModerationEntry `json:"moderation"`
}

// ModerationEntry is one recorded block or unblock.
type ModerationEntry struct {
	ID        uuid.UUID `json:"id"`
	Action    string    `json:"action"`
	Reason    string    `json:"reason"`
	ActorID   uuid.UUID `json:"actor_id"`
	ActorName string    `json:"actor_name"`
	CreatedAt time.Time `json:"created_at"`
}

// Filters narrow the account list.
type Filters struct {
	Search string
	Role   string
	// Status is a pointer because 0 (blocked) is a meaningful filter value that
	// a plain int cannot distinguish from "not filtering".
	Status *int
}

// ListQuery is one page of accounts.
type ListQuery struct {
	Filters
	Page    int
	PerPage int
}

// StatusChange is a validated moderation decision, ready to persist.
type StatusChange struct {
	TargetID uuid.UUID
	ActorID  uuid.UUID
	Status   int
	Reason   string
}

// Action names the audit row a StatusChange produces.
func (c StatusChange) Action() string {
	if c.Status == StatusActive {
		return "UNBLOCKED"
	}
	return "BLOCKED"
}

// Repository is the persistence port.
type Repository interface {
	List(ctx context.Context, query ListQuery) ([]Summary, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (Detail, error)

	// ApplyStatus writes the new status and its audit row in one transaction.
	// Splitting them would allow a block with no recorded reason, which is the
	// exact gap this module exists to close.
	ApplyStatus(ctx context.Context, change StatusChange) error

	// RoleNames lists the roles that exist, so the list screen's filter is
	// built from the database rather than from a hardcoded array that drifts
	// when somebody adds a role.
	RoleNames(ctx context.Context) ([]string, error)
}

// SessionRevoker ends every session an account holds.
//
// The interface is declared here, next to its consumer, so this module does not
// import the auth package's concrete store; auth.SessionStore satisfies it.
//
// This is not optional decoration on a block. internal/modules/user.Refresh
// mints a fresh access token from a refresh token WITHOUT re-checking
// users.status, so an account blocked but not revoked keeps working for the
// full lifetime of its refresh token. Revocation is what makes a block real.
type SessionRevoker interface {
	RevokeAllForUser(ctx context.Context, userID string, accessTTL time.Duration) error
}
