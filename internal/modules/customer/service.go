package customer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Service holds the moderation rules.
type Service struct {
	repo      Repository
	sessions  SessionRevoker
	accessTTL time.Duration
	log       *slog.Logger
}

// NewService wires the module.
//
// accessTTL is the access-token lifetime, needed because revocation blacklists
// a jti only until it would have expired anyway -- that is what bounds how
// large the blacklist can grow.
func NewService(repo Repository, sessions SessionRevoker, accessTTL time.Duration, log *slog.Logger) *Service {
	return &Service{repo: repo, sessions: sessions, accessTTL: accessTTL, log: log}
}

// List returns one page of accounts.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Summary, int64, error) {
	rows, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load accounts").WithCause(err)
	}
	return rows, total, nil
}

// Get returns one account in full.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Detail, error) {
	detail, err := s.repo.FindByID(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detail{}, httpx.NotFound("account not found")
		}
		return Detail{}, httpx.Internal("failed to load account").WithCause(err)
	}
	return detail, nil
}

// Roles lists the role names the filter offers.
func (s *Service) Roles(ctx context.Context) ([]string, error) {
	names, err := s.repo.RoleNames(ctx)
	if err != nil {
		return nil, httpx.Internal("failed to load roles").WithCause(err)
	}
	return names, nil
}

// SetStatus blocks or unblocks an account.
//
// Three things happen, in this order, and the order matters:
//
//  1. The decision is validated -- including that the actor is not blocking
//     themselves, which would lock the operator out mid-action.
//  2. The status and its audit row are written in one transaction.
//  3. Every session the account holds is revoked.
//
// Step 3 is not optional and cannot be skipped when it fails. Password, social
// and phone sign-in all re-check users.status, but internal/modules/user.Refresh
// does not: it mints a new access token straight from a valid refresh token. An
// account blocked without revocation therefore keeps working for the full
// lifetime of its refresh token, which is exactly the window an abuser needs.
func (s *Service) SetStatus(
	ctx context.Context,
	actorID, targetID uuid.UUID,
	status int,
	rawReason string,
) (Detail, error) {
	if status != StatusActive && status != StatusBlocked {
		return Detail{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "status", Message: "must be 0 (blocked) or 1 (active)"},
		})
	}

	if actorID == targetID {
		// Not merely unhelpful: an operator who blocks themselves has their own
		// sessions revoked by step 3 and cannot sign back in to undo it.
		return Detail{}, httpx.Conflict("you cannot change the status of your own account")
	}

	reason := strings.TrimSpace(rawReason)
	if status == StatusBlocked && reason == "" {
		return Detail{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "reason", Message: "is required when blocking an account"},
		})
	}
	if utf8.RuneCountInString(reason) > MaxReasonRunes {
		return Detail{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "reason", Message: "must not exceed 500 characters"},
		})
	}

	current, err := s.repo.FindByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detail{}, httpx.NotFound("account not found")
		}
		return Detail{}, httpx.Internal("failed to load account").WithCause(err)
	}
	if current.Status == status {
		return Detail{}, httpx.Conflict("the account is already in this status")
	}

	change := StatusChange{
		TargetID: targetID,
		ActorID:  actorID,
		Status:   status,
		Reason:   reason,
	}

	if err := s.repo.ApplyStatus(ctx, change); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detail{}, httpx.Conflict("the account changed while you were working on it; reload and try again")
		}
		return Detail{}, httpx.Internal("failed to update the account status").WithCause(err)
	}

	if status == StatusBlocked {
		if err := s.sessions.RevokeAllForUser(ctx, targetID.String(), s.accessTTL); err != nil {
			// Loud, not silent. The row now says "blocked" while the account may
			// still hold a working refresh token, and an operator told
			// "berhasil" would never know to check. Retrying is safe and is the
			// right response: ApplyStatus becomes a no-op (the status already
			// matches) while the revocation is what actually needs repeating.
			s.log.Error("account blocked but sessions were not revoked",
				slog.String("target_id", targetID.String()),
				slog.String("actor_id", actorID.String()),
				slog.String("error", err.Error()))

			return Detail{}, httpx.Internal(
				"the account was blocked but its active sessions could not be ended; retry to force them out",
			).WithCause(err)
		}
	}

	s.log.Info("account status changed",
		slog.String("target_id", targetID.String()),
		slog.String("actor_id", actorID.String()),
		slog.String("action", change.Action()))

	// Re-read so the response carries the new status and the audit row just
	// written, rather than the caller needing a second request to see it.
	return s.Get(ctx, targetID)
}
