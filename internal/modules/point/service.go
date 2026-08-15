package point

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Service holds the ledger rules.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Balance returns the cached total, the ledger truth and their difference.
func (s *Service) Balance(ctx context.Context, userID uuid.UUID) (Balance, error) {
	if err := s.assertAccount(ctx, userID); err != nil {
		return Balance{}, err
	}

	balance, err := s.repo.Balance(ctx, userID)
	if err != nil {
		return Balance{}, httpx.Internal("failed to load the point balance").WithCause(err)
	}

	if !balance.IsReconciled() {
		// Logged at warn, not error: the request succeeded and the answer is
		// correct -- it says the two numbers disagree, which is exactly what the
		// caller asked. But an operator should not be the only one who notices.
		s.log.Warn("point balance does not match its ledger",
			slog.String("user_id", userID.String()),
			slog.Int64("cached", balance.Cached),
			slog.Int64("ledger", balance.Ledger),
			slog.Int64("drift", balance.Drift))
	}

	return balance, nil
}

// History returns one page of the ledger.
func (s *Service) History(ctx context.Context, query HistoryQuery) ([]Entry, int64, error) {
	if err := s.assertAccount(ctx, query.UserID); err != nil {
		return nil, 0, err
	}

	if query.Direction != "" &&
		query.Direction != DirectionIn && query.Direction != DirectionOut {
		return nil, 0, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "direction", Message: "must be 'in' or 'out'"},
		})
	}

	rows, total, err := s.repo.History(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load the point history").WithCause(err)
	}
	return rows, total, nil
}

// Adjust applies a manual correction.
//
// Note what this deliberately does NOT do: it never silently rewrites the cached
// total to match the ledger. A correction is itself a ledger movement, recorded
// with who made it and why, so the balance and its explanation stay in step. An
// operator "fixing" a drift by overwriting the total would destroy the evidence
// of whatever caused it, and the same bug would simply recur next week.
func (s *Service) Adjust(
	ctx context.Context,
	actorID, userID uuid.UUID,
	points int64,
	rawDirection, rawReason string,
) (Balance, error) {
	if err := s.assertAccount(ctx, userID); err != nil {
		return Balance{}, err
	}

	var fields []httpx.FieldError

	direction := strings.TrimSpace(strings.ToLower(rawDirection))
	if direction != DirectionIn && direction != DirectionOut {
		fields = append(fields, httpx.FieldError{
			Field:   "direction",
			Message: "must be 'in' (menambah) or 'out' (mengurangi)",
		})
	}

	switch {
	case points <= 0:
		fields = append(fields, httpx.FieldError{
			Field:   "points",
			Message: "must be greater than zero; use direction to subtract",
		})
	case points > MaxAdjustment:
		fields = append(fields, httpx.FieldError{
			Field:   "points",
			Message: "is larger than a single adjustment may be",
		})
	}

	reason := strings.TrimSpace(rawReason)
	if reason == "" {
		// Always required, in both directions. A grant needs justifying as much
		// as a deduction -- more, arguably, since nobody complains about it.
		fields = append(fields, httpx.FieldError{
			Field:   "reason",
			Message: "is required for every manual adjustment",
		})
	}
	if utf8.RuneCountInString(reason) > MaxReasonRunes {
		fields = append(fields, httpx.FieldError{
			Field:   "reason",
			Message: "must not exceed 500 characters",
		})
	}

	if len(fields) > 0 {
		return Balance{}, httpx.Validation("request validation failed").WithDetails(fields)
	}

	current, err := s.repo.Balance(ctx, userID)
	if err != nil {
		return Balance{}, httpx.Internal("failed to load the point balance").WithCause(err)
	}

	// Guard against a negative balance, judged against the ledger rather than
	// the cache: the ledger is the truth, and letting a drifted cache authorise
	// a deduction would deepen the very inconsistency being investigated.
	if direction == DirectionOut && current.Ledger-points < 0 {
		return Balance{}, httpx.Conflict(
			"the deduction is larger than the account's ledger balance",
		)
	}

	adjustment := Adjustment{
		UserID:    userID,
		Points:    points,
		Direction: direction,
		Reason:    reason,
		ActorID:   actorID,
	}

	if err := s.repo.Adjust(ctx, adjustment); err != nil {
		return Balance{}, httpx.Internal("failed to record the adjustment").WithCause(err)
	}

	s.log.Info("points adjusted manually",
		slog.String("user_id", userID.String()),
		slog.String("actor_id", actorID.String()),
		slog.String("direction", direction),
		slog.Int64("points", points))

	return s.Balance(ctx, userID)
}

// ClaimedVouchers lists the vouchers an account holds.
func (s *Service) ClaimedVouchers(ctx context.Context, userID uuid.UUID) ([]ClaimedVoucher, error) {
	if err := s.assertAccount(ctx, userID); err != nil {
		return nil, err
	}

	rows, err := s.repo.ClaimedVouchers(ctx, userID)
	if err != nil {
		return nil, httpx.Internal("failed to load the claimed vouchers").WithCause(err)
	}
	return rows, nil
}

// Claimants lists who claimed a given voucher.
func (s *Service) Claimants(
	ctx context.Context,
	voucherID uuid.UUID,
	page, perPage int,
) ([]Claimant, int64, error) {
	rows, total, err := s.repo.Claimants(ctx, voucherID, page, perPage)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load the voucher claims").WithCause(err)
	}
	return rows, total, nil
}

// assertAccount turns a missing account into a 404, so an empty ledger and a
// non-existent user are not reported the same way.
func (s *Service) assertAccount(ctx context.Context, userID uuid.UUID) error {
	exists, err := s.repo.AccountExists(ctx, userID)
	if err != nil {
		return httpx.Internal("failed to load account").WithCause(err)
	}
	if !exists {
		return httpx.NotFound("account not found")
	}
	return nil
}
