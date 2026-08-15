// Package point is the loyalty ledger and the tools to audit it.
//
// The central decision here is which number is true. The schema has two:
//
//	user_points.total_points      one integer per account
//	user_point_histories          one row per movement, direction 'in' or 'out'
//
// This module treats the ledger as the truth and the total as a cache. That is
// the only arrangement in which "poin pelanggan ini salah" is a detectable
// condition rather than a customer complaint: a cache can be recomputed from a
// ledger and compared, whereas two independent totals can only be argued about.
//
// Every read therefore reports both numbers and their difference, and the UI
// shows the difference rather than hiding it. A silent repair would destroy the
// evidence of whatever wrote the wrong value.
package point

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is returned when the account itself does not exist.
//
// An account with no points is NOT this error: it is a zero balance with an
// empty ledger, which is the normal state of somebody who has never earned one.
var ErrNotFound = errors.New("account not found")

// Ledger directions, as constrained by chk_user_point_histories_direction.
const (
	DirectionIn  = "in"
	DirectionOut = "out"
)

// SourceAdjustment marks a movement an operator entered by hand.
//
// It is distinct from the sources the system writes (an order, a voucher
// exchange) so that "how much of this balance was granted manually?" stays
// answerable -- the question that matters when a balance looks wrong.
const SourceAdjustment = "MANUAL_ADJUSTMENT"

// MaxReasonRunes bounds the free-text reason on an adjustment.
const MaxReasonRunes = 500

// MaxAdjustment caps a single manual movement.
//
// Not a business rule so much as a guard against a typo: an operator meaning to
// grant 100 and typing 100000 should be stopped by the form, not discovered in
// the financial report. Raise it deliberately if a real case needs more.
const MaxAdjustment = 1_000_000

// Balance is the answer to "how many points does this account have?", with the
// evidence needed to decide whether to believe it.
type Balance struct {
	UserID uuid.UUID `json:"user_id"`

	// Cached is user_points.total_points, the number every other screen reads.
	Cached int64 `json:"cached_balance"`

	// Ledger is SUM(in) - SUM(out) over user_point_histories: the truth.
	Ledger int64 `json:"ledger_balance"`

	// Drift is Cached - Ledger. Non-zero means something wrote a balance
	// without a matching ledger row, or a ledger row without updating the
	// balance -- the two halves of the bug this module exists to surface.
	Drift int64 `json:"drift"`

	// EntryCount is how many movements the ledger holds.
	EntryCount int64 `json:"entry_count"`

	// ExpiredInflow is the total of 'in' rows whose expired_at is in the past.
	//
	// This is a diagnostic, not a balance component. Expiry is only correctly
	// modelled by writing a matching 'out' row when points lapse; if no such job
	// runs, this number grows while Ledger never shrinks, and a customer keeps
	// points they should have lost. A non-zero value with no corresponding
	// outflow is the signature of a missing expiry job.
	ExpiredInflow int64 `json:"expired_inflow"`

	UpdatedAt *time.Time `json:"updated_at"`
}

// IsReconciled reports whether the cache agrees with the ledger.
func (b Balance) IsReconciled() bool { return b.Drift == 0 }

// Entry is one movement in the ledger.
type Entry struct {
	ID          uuid.UUID  `json:"id"`
	Direction   string     `json:"direction"`
	Points      int64      `json:"points"`
	PointType   string     `json:"point_type"`
	Source      string     `json:"source"`
	SourceID    string     `json:"source_id"`
	Description string     `json:"description"`
	ExpiredAt   *time.Time `json:"expired_at"`
	CreatedAt   time.Time  `json:"created_at"`

	// IsExpired is derived rather than stored, so the client does not have to
	// compare timestamps against a clock that may differ from the server's.
	IsExpired bool `json:"is_expired"`
}

// HistoryQuery is one page of the ledger.
type HistoryQuery struct {
	UserID  uuid.UUID
	Page    int
	PerPage int
	// Direction filters to 'in' or 'out'; empty means both.
	Direction string
}

// Adjustment is a validated manual correction, ready to persist.
type Adjustment struct {
	UserID uuid.UUID
	// Points is always positive; Direction says which way it moves.
	Points    int64
	Direction string
	Reason    string
	ActorID   uuid.UUID
}

// ClaimedVoucher is one voucher an account holds.
type ClaimedVoucher struct {
	ID          uuid.UUID  `json:"id"`
	VoucherID   uuid.UUID  `json:"voucher_id"`
	Code        string     `json:"code"`
	Description string     `json:"description"`
	IsUsed      bool       `json:"is_used"`
	ClaimedAt   time.Time  `json:"claimed_at"`
	RedeemedAt  *time.Time `json:"redeemed_at"`
	ValidUntil  *time.Time `json:"valid_until"`
}

// Claimant is one account that claimed a given voucher.
type Claimant struct {
	UserID     uuid.UUID  `json:"user_id"`
	Name       string     `json:"name"`
	Email      string     `json:"email"`
	Phone      string     `json:"phone"`
	IsUsed     bool       `json:"is_used"`
	ClaimedAt  time.Time  `json:"claimed_at"`
	RedeemedAt *time.Time `json:"redeemed_at"`
}

// Repository is the persistence port.
type Repository interface {
	// AccountExists separates "no such account" from "an account with no
	// points", which are the same empty result but different answers.
	AccountExists(ctx context.Context, userID uuid.UUID) (bool, error)

	Balance(ctx context.Context, userID uuid.UUID) (Balance, error)
	History(ctx context.Context, query HistoryQuery) ([]Entry, int64, error)

	// Adjust writes the ledger row and moves the cached total in ONE
	// transaction. Doing them separately is precisely how the two drift apart,
	// which is the defect this module was built to make visible.
	Adjust(ctx context.Context, adjustment Adjustment) error

	ClaimedVouchers(ctx context.Context, userID uuid.UUID) ([]ClaimedVoucher, error)
	Claimants(ctx context.Context, voucherID uuid.UUID, page, perPage int) ([]Claimant, int64, error)
}
