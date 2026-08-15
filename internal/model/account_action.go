package model

import (
	"time"

	"github.com/google/uuid"
)

// AccountAction values.
const (
	AccountBlocked   = "BLOCKED"
	AccountUnblocked = "UNBLOCKED"
)

// AccountAction records a moderation decision taken against an account.
//
// It exists so "kenapa akun ini diblokir?" has an answer six months later.
// users.status alone cannot answer it: it is a single integer that the next
// write overwrites, so an account blocked for fraud and one blocked by mistake
// are indistinguishable afterwards, and an unblock erases the fact that a block
// ever happened.
//
// Append-only, like OrderStatusHistory: nothing updates or deletes a row, and
// there is no DeletedAt. A moderation log that can be quietly rewritten is
// worth less than no log at all, because it invites being trusted.
type AccountAction struct {
	ID uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey" json:"id"`

	// TargetUserID is the account acted upon.
	TargetUserID uuid.UUID `gorm:"type:uuid;index;not null" json:"target_user_id"`
	Target       User      `gorm:"foreignKey:TargetUserID;OnDelete:CASCADE" json:"-"`

	// ActorID is the member of staff who decided. Not a pointer: every row here
	// is written by a person through the admin API. A future automated
	// suspension would need this relaxed, and that change should be deliberate.
	ActorID uuid.UUID `gorm:"type:uuid;index;not null" json:"actor_id"`
	Actor   User      `gorm:"foreignKey:ActorID" json:"-"`

	// Action is AccountBlocked or AccountUnblocked.
	Action string `gorm:"type:varchar(50);not null" json:"action"`

	// Reason is mandatory for a block and optional for an unblock. The service
	// enforces that rather than the column, which would otherwise also reject
	// the legitimate empty reason on an unblock.
	Reason string `gorm:"type:text" json:"reason"`

	CreatedAt time.Time `gorm:"autoCreateTime;index" json:"created_at"`
}
