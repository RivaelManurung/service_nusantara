package order

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/model"
)

// RoleSuperAdmin sees every shop. Everyone else sees the shops they are
// assigned to in shop_cashiers, which is the same rule the shop module applies
// to its /cashier group.
const RoleSuperAdmin = "superadmin"

// Service holds the business rules: who may see which orders, which transitions
// are legal, and which of them must be justified.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// Caller is the acting user, taken from the token rather than the request body.
type Caller struct {
	UserID uuid.UUID
	Role   string
}

// List returns one page of orders the caller is allowed to see.
func (s *Service) List(ctx context.Context, caller Caller, query ListQuery) ([]Summary, int64, error) {
	scope, err := s.scopeFor(ctx, caller)
	if err != nil {
		return nil, 0, err
	}
	query.ScopedShopIDs = scope

	rows, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load orders").WithCause(err)
	}
	return rows, total, nil
}

// Get returns one order, refusing orders outside the caller's shops.
func (s *Service) Get(ctx context.Context, caller Caller, id uuid.UUID) (Detail, error) {
	detail, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Detail{}, s.translate(err, "failed to load order")
	}

	if err := s.assertInScope(ctx, caller, detail.ShopID); err != nil {
		return Detail{}, err
	}

	current := model.OrderStatus(detail.Status)
	orderType := model.OrderType(detail.OrderType)

	for _, next := range NextStatuses(current, orderType) {
		detail.NextStatuses = append(detail.NextStatuses, string(next))
		if ReasonRequired(next) {
			detail.ReasonRequiredFor = append(detail.ReasonRequiredFor, string(next))
		}
	}
	// Non-nil empty slices: a terminal order should serialise "next_statuses":
	// [] rather than null, so the client renders "no further action" instead of
	// having to treat null as a separate case.
	if detail.NextStatuses == nil {
		detail.NextStatuses = []string{}
	}
	if detail.ReasonRequiredFor == nil {
		detail.ReasonRequiredFor = []string{}
	}

	return detail, nil
}

// Timeline returns how the order reached its current status.
func (s *Service) Timeline(ctx context.Context, caller Caller, id uuid.UUID) ([]TimelineEntry, error) {
	detail, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, s.translate(err, "failed to load order")
	}
	if err := s.assertInScope(ctx, caller, detail.ShopID); err != nil {
		return nil, err
	}

	rows, err := s.repo.Timeline(ctx, id)
	if err != nil {
		return nil, httpx.Internal("failed to load order timeline").WithCause(err)
	}
	return rows, nil
}

// ChangeStatus validates and applies a transition.
//
// Every rejection is a 4xx with a specific message, because the operator on the
// other side is deciding what to do next: "tidak bisa" without a reason turns
// into a support ticket.
func (s *Service) ChangeStatus(
	ctx context.Context,
	caller Caller,
	id uuid.UUID,
	rawStatus, rawReason string,
) (Detail, error) {
	target := model.OrderStatus(strings.TrimSpace(rawStatus))
	if !IsKnownStatus(string(target)) {
		return Detail{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "status", Message: "is not a known order status"},
		})
	}

	detail, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Detail{}, s.translate(err, "failed to load order")
	}
	if err := s.assertInScope(ctx, caller, detail.ShopID); err != nil {
		return Detail{}, err
	}

	current := model.OrderStatus(detail.Status)
	orderType := model.OrderType(detail.OrderType)

	if target == current {
		return Detail{}, httpx.Conflict("the order is already in this status")
	}
	if !CanTransition(current, target, orderType) {
		return Detail{}, httpx.Conflict(
			"an order in status " + string(current) + " cannot move to " + string(target),
		)
	}

	reason := strings.TrimSpace(rawReason)
	if ReasonRequired(target) && reason == "" {
		return Detail{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "reason", Message: "is required when cancelling or rejecting an order"},
		})
	}
	if utf8.RuneCountInString(reason) > MaxReasonRunes {
		return Detail{}, httpx.Validation("request validation failed").WithDetails([]httpx.FieldError{
			{Field: "reason", Message: "must not exceed 500 characters"},
		})
	}

	change := StatusChange{
		OrderID: id,
		From:    current,
		To:      target,
		Reason:  reason,
		ActorID: caller.UserID,
	}

	if err := s.repo.ApplyStatus(ctx, change); err != nil {
		if errors.Is(err, ErrNotFound) {
			// The guarded UPDATE matched nothing, which here means somebody else
			// moved the order between the read and the write.
			return Detail{}, httpx.Conflict("the order changed while you were working on it; reload and try again")
		}
		return Detail{}, httpx.Internal("failed to update the order status").WithCause(err)
	}

	s.log.Info("order status changed",
		slog.String("order_id", id.String()),
		slog.String("from", string(current)),
		slog.String("to", string(target)),
		slog.String("actor_id", caller.UserID.String()))

	// Re-read so the response carries the new status and its new set of legal
	// next steps, rather than the caller having to issue a second request to
	// find out what it may do now.
	return s.Get(ctx, caller, id)
}

// --- the customer's own orders -----------------------------------------
//
// These three are what the Flutter app calls. They are NOT the admin reads with
// a filter bolted on: the admin endpoints are scoped to the shops a member of
// staff works in and guarded by order.read, a permission no customer holds.
// Handing the app those endpoints would mean either a 403 for every customer
// or, if the guard were loosened, one customer reading another's orders.
//
// The scope here is the caller's own user id, forced from the token, and it is
// applied to every method. There is no path by which a customer can widen it.

// MyOrders returns one page of the caller's own orders.
func (s *Service) MyOrders(ctx context.Context, customerID uuid.UUID, query ListQuery) ([]Summary, int64, error) {
	// Overwritten, not defaulted: a customer_id in the query string must never
	// be able to point this at somebody else's history.
	query.CustomerID = customerID
	query.ScopedShopIDs = nil

	rows, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load your orders").WithCause(err)
	}
	return rows, total, nil
}

// MyOrder returns one of the caller's own orders.
func (s *Service) MyOrder(ctx context.Context, customerID, id uuid.UUID) (Detail, error) {
	detail, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Detail{}, s.translate(err, "failed to load your order")
	}
	if detail.CustomerID != customerID {
		// 404 rather than 403, for the same reason the admin reads do it: an
		// answer that distinguishes "not yours" from "does not exist" lets
		// anyone enumerate order ids.
		return Detail{}, httpx.NotFound("order not found")
	}

	current := model.OrderStatus(detail.Status)
	orderType := model.OrderType(detail.OrderType)

	// The customer sees where the order can go, but never gets the buttons: the
	// transition endpoint is admin-only. This is what drives the tracking
	// screen's progress indicator.
	for _, next := range NextStatuses(current, orderType) {
		detail.NextStatuses = append(detail.NextStatuses, string(next))
	}
	if detail.NextStatuses == nil {
		detail.NextStatuses = []string{}
	}
	detail.ReasonRequiredFor = []string{}

	return detail, nil
}

// MyOrderTimeline returns the tracking history of one of the caller's orders.
func (s *Service) MyOrderTimeline(ctx context.Context, customerID, id uuid.UUID) ([]TimelineEntry, error) {
	detail, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return nil, s.translate(err, "failed to load your order")
	}
	if detail.CustomerID != customerID {
		return nil, httpx.NotFound("order not found")
	}

	rows, err := s.repo.Timeline(ctx, id)
	if err != nil {
		return nil, httpx.Internal("failed to load the order timeline").WithCause(err)
	}

	// The staff member's name is stripped. A customer is entitled to know that
	// their order was rejected and why; who at the shop pressed the button is
	// internal, and putting a named employee in a customer-facing screen is how
	// a complaint becomes personal.
	for index := range rows {
		rows[index].ActorName = ""
		rows[index].ActorID = nil
	}

	return rows, nil
}

// scopeFor returns the shop ids the caller may read, or nil for unrestricted.
func (s *Service) scopeFor(ctx context.Context, caller Caller) ([]uuid.UUID, error) {
	if caller.Role == RoleSuperAdmin {
		return nil, nil
	}

	ids, err := s.repo.AssignedShopIDs(ctx, caller.UserID)
	if err != nil {
		return nil, httpx.Internal("failed to resolve your shop assignments").WithCause(err)
	}
	// A non-nil empty slice is meaningful: it says "assigned to nothing", which
	// the repository turns into an empty result rather than a full table.
	if ids == nil {
		ids = []uuid.UUID{}
	}
	return ids, nil
}

// assertInScope refuses an order belonging to a shop the caller does not work in.
//
// It answers 404 rather than 403 on purpose: an operator who may not see a shop
// should not be able to confirm that one of its orders exists by probing ids.
func (s *Service) assertInScope(ctx context.Context, caller Caller, shopID uuid.UUID) error {
	scope, err := s.scopeFor(ctx, caller)
	if err != nil {
		return err
	}
	if scope == nil {
		return nil
	}

	for _, id := range scope {
		if id == shopID {
			return nil
		}
	}
	return httpx.NotFound("order not found")
}

// translate maps a repository error onto the client-facing taxonomy.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("order not found")
	}
	return httpx.Internal(message).WithCause(err)
}
