// Package dashboard answers the two questions an operator opens the admin
// panel with: "how are we doing today?" and "what needs attention?".
//
// It is distinct from internal/modules/report, which answers "what happened
// over a period" for accounting. The difference is not the data but the
// deadline: a report is read once a month and must reconcile exactly, whereas
// this is read every morning and must surface the handful of rows worth acting
// on. That is why the summary is scoped to today and the signals below are
// deliberately coarse.
package dashboard

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Summary is the headline block: what has happened today, and what is stuck.
type Summary struct {
	// Date is the day these figures cover, YYYY-MM-DD, so a screen left open
	// overnight cannot silently claim yesterday's numbers are today's.
	Date string `json:"date"`

	OrdersToday  int64   `json:"orders_today"`
	RevenueToday float64 `json:"revenue_today"`
	NewCustomers int64   `json:"new_customers_today"`

	// StalledOrders counts orders sitting in a non-terminal status longer than
	// StalledAfter. This is the number that makes the dashboard a worklist
	// rather than a scoreboard.
	StalledOrders int64 `json:"stalled_orders"`

	// AwaitingAction counts orders waiting on the shop specifically -- paid, or
	// waiting for confirmation. An operator can clear these; they cannot clear
	// "on the way".
	AwaitingAction int64 `json:"awaiting_action"`

	// Comparisons against the same figures yesterday. Nil when there is no
	// yesterday to compare against, which is honest for a new deployment --
	// "+100%" from zero is noise dressed as insight.
	OrdersYesterday  *int64   `json:"orders_yesterday"`
	RevenueYesterday *float64 `json:"revenue_yesterday"`
}

// StalledAfter is how long an order may sit in one status before it counts as
// stuck.
//
// Two hours is a starting point rather than a measured SLA: long enough that a
// busy shop does not light up, short enough that a forgotten order surfaces the
// same working day. It matches the threshold the orders screen uses, and both
// should move together.
const StalledAfter = 2 * time.Hour

// TrendPoint is one day of the sales series.
type TrendPoint struct {
	Date    string  `json:"date"`
	Orders  int64   `json:"orders"`
	Revenue float64 `json:"revenue"`
}

// Trend bounds.
const (
	MaxTrendDays     = 90
	DefaultTrendDays = 14
)

// AnomalySeverity ranks how much a signal deserves attention.
type AnomalySeverity string

const (
	SeverityInfo AnomalySeverity = "INFO"
	SeverityWarn AnomalySeverity = "WARNING"
)

// Anomaly is one account worth a human look.
//
// Every field exists to make the finding checkable. A signal that says only
// "suspicious" cannot be acted on: the operator needs to know which rule fired,
// on what evidence, and against which account -- so they can disagree with it.
//
// Nothing here blocks anybody. These rules are deliberately coarse, and coarse
// rules applied automatically punish real customers. The output is a queue for
// a person; the block button lives on the account screen, where the mandatory
// reason and the audit trail are.
type Anomaly struct {
	UserID   uuid.UUID       `json:"user_id"`
	Name     string          `json:"name"`
	Email    string          `json:"email"`
	Phone    string          `json:"phone"`
	Rule     string          `json:"rule"`
	Severity AnomalySeverity `json:"severity"`
	// Detail is the evidence, in the operator's language.
	Detail string `json:"detail"`
	// Metric is the number that tripped the rule, so the list can be sorted by
	// how badly rather than merely by what.
	Metric int64 `json:"metric"`
}

// Rule names. Exported so the client can label them without matching on prose.
const (
	RuleVoucherHoarding = "VOUCHER_HOARDING"
	RuleCancelRate      = "HIGH_CANCEL_RATE"
	RulePointDrift      = "POINT_DRIFT"
)

// Thresholds for the rules above.
//
// Round numbers on purpose: they are explainable to a shop owner, which matters
// more than statistical elegance for a queue a human triages. Tune them once
// there is real throughput to tune against.
const (
	// An account holding this many unused vouchers and never ordering.
	VoucherHoardMin = 3
	// Cancelled share of orders, judged only once there are enough orders for
	// the ratio to mean anything.
	CancelRateMinOrders = 5
	CancelRatePercent   = 50
	// How many findings one request may return.
	MaxAnomalies     = 50
	DefaultAnomalies = 20
)

// Repository is the persistence port.
type Repository interface {
	Summary(ctx context.Context, day time.Time, stalledBefore time.Time) (Summary, error)
	Trend(ctx context.Context, days int) ([]TrendPoint, error)
	Anomalies(ctx context.Context, limit int) ([]Anomaly, error)
}
