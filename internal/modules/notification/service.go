package notification

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Service holds the business rules.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// List returns one page of the caller's inbox.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Notification, int64, error) {
	if query.UserID == uuid.Nil {
		// A zero owner would match nothing, but silently returning an empty
		// page would hide a wiring mistake in the handler.
		return nil, 0, httpx.Unauthorized("authentication required")
	}

	channel, err := normalizeChannel(query.Channel)
	if err != nil {
		return nil, 0, err
	}
	query.Channel = channel

	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load notifications").WithCause(err)
	}
	return items, total, nil
}

// UnreadCount returns the badge totals for the caller's two inbox tabs.
func (s *Service) UnreadCount(ctx context.Context, userID uuid.UUID) (UnreadCount, error) {
	if userID == uuid.Nil {
		return UnreadCount{}, httpx.Unauthorized("authentication required")
	}

	counts, err := s.repo.UnreadByChannel(ctx, userID)
	if err != nil {
		return UnreadCount{}, httpx.Internal("failed to count unread notifications").WithCause(err)
	}

	transaksi := counts[ChannelTransaksi]
	promo := counts[ChannelPromo]

	return UnreadCount{
		Transaksi: transaksi,
		Promo:     promo,
		Total:     transaksi + promo,
	}, nil
}

// MarkRead acknowledges one message. A message owned by somebody else is
// reported as not found, exactly like one that does not exist: the endpoint
// must not become a way to discover other people's notification ids.
func (s *Service) MarkRead(ctx context.Context, userID, id uuid.UUID) error {
	if userID == uuid.Nil {
		return httpx.Unauthorized("authentication required")
	}

	if _, err := s.repo.FindByIDForUser(ctx, id, userID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return httpx.NotFound("notification not found")
		}
		return httpx.Internal("failed to load notification").WithCause(err)
	}

	if err := s.repo.MarkRead(ctx, id, userID); err != nil {
		return httpx.Internal("failed to mark notification as read").WithCause(err)
	}
	return nil
}

// MarkAllRead acknowledges every unread message, optionally within one tab, and
// reports how many were updated.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID, channel string) (int64, error) {
	if userID == uuid.Nil {
		return 0, httpx.Unauthorized("authentication required")
	}

	normalized, err := normalizeChannel(channel)
	if err != nil {
		return 0, err
	}

	updated, err := s.repo.MarkAllRead(ctx, userID, normalized)
	if err != nil {
		return 0, httpx.Internal("failed to mark notifications as read").WithCause(err)
	}
	return updated, nil
}

// normalizeChannel upper-cases the filter and rejects anything that is not a
// known tab, so an arbitrary string never reaches the WHERE clause.
func normalizeChannel(channel string) (string, error) {
	channel = strings.ToUpper(strings.TrimSpace(channel))
	if channel == "" {
		return "", nil
	}
	if !IsValidChannel(channel) {
		return "", httpx.BadRequest("invalid channel, allowed: TRANSAKSI, PROMO")
	}
	return channel, nil
}
