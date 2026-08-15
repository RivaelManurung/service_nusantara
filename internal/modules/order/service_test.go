package order_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/model"
	"service_nusantara/internal/modules/order"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	detail   order.Detail
	rows     []order.Summary
	total    int64
	timeline []order.TimelineEntry
	assigned []uuid.UUID

	findErr  error
	applyErr error
	listErr  error

	// Captured arguments, so a test can assert what the service asked for.
	lastQuery  order.ListQuery
	lastChange order.StatusChange
	applyCalls int
}

func (f *fakeRepo) List(_ context.Context, query order.ListQuery) ([]order.Summary, int64, error) {
	f.lastQuery = query
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	return f.rows, f.total, nil
}

func (f *fakeRepo) FindByID(context.Context, uuid.UUID) (order.Detail, error) {
	if f.findErr != nil {
		return order.Detail{}, f.findErr
	}
	return f.detail, nil
}

func (f *fakeRepo) Timeline(context.Context, uuid.UUID) ([]order.TimelineEntry, error) {
	return f.timeline, nil
}

func (f *fakeRepo) ApplyStatus(_ context.Context, change order.StatusChange) error {
	f.applyCalls++
	f.lastChange = change
	if f.applyErr != nil {
		return f.applyErr
	}
	// Reflect the write so the service's re-read sees the new status.
	f.detail.Status = string(change.To)
	return nil
}

func (f *fakeRepo) AssignedShopIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return f.assigned, nil
}

// --- helpers -----------------------------------------------------------

func newService(repo order.Repository) *order.Service {
	return order.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func detailWith(status model.OrderStatus, orderType model.OrderType, shopID uuid.UUID) order.Detail {
	return order.Detail{
		Summary: order.Summary{
			ID:        uuid.New(),
			Code:      "ORD-1",
			Status:    string(status),
			OrderType: string(orderType),
			ShopID:    shopID,
		},
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- the state machine -------------------------------------------------

func TestCanTransitionFollowsTheLifecycle(t *testing.T) {
	cases := []struct {
		name      string
		from, to  model.OrderStatus
		orderType model.OrderType
		want      bool
	}{
		{"paid moves to store confirmation", model.OrderPaid, model.OrderWaitingStore, model.Delivery, true},
		{"paid cannot skip to delivered", model.OrderPaid, model.OrderDelivered, model.Delivery, false},
		{"a delivery looks for a driver", model.OrderStoreAccepted, model.OrderSearchingDriver, model.Delivery, true},
		{"a take-away never looks for a driver", model.OrderStoreAccepted, model.OrderSearchingDriver, model.TakeAway, false},
		{"a take-away completes at the counter", model.OrderStoreAccepted, model.OrderCompleted, model.TakeAway, true},
		{"a delivery must be delivered before it completes", model.OrderStoreAccepted, model.OrderCompleted, model.Delivery, false},
		{"a rejection only follows store confirmation", model.OrderWaitingStore, model.OrderStoreRejected, model.Delivery, true},
		{"an accepted order cannot be rejected", model.OrderStoreAccepted, model.OrderStoreRejected, model.Delivery, false},
		{"cancelling stops once a driver is assigned", model.OrderDriverAssigned, model.OrderCanceled, model.Delivery, false},
		{"cancelling is allowed while searching", model.OrderSearchingDriver, model.OrderCanceled, model.Delivery, true},
		{"completed is terminal", model.OrderCompleted, model.OrderCanceled, model.Delivery, false},
		{"canceled is terminal", model.OrderCanceled, model.OrderPaid, model.Delivery, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, order.CanTransition(tc.from, tc.to, tc.orderType))
		})
	}
}

func TestNextStatusesIsFilteredByOrderType(t *testing.T) {
	delivery := order.NextStatuses(model.OrderStoreAccepted, model.Delivery)
	assert.Contains(t, delivery, model.OrderSearchingDriver)
	assert.NotContains(t, delivery, model.OrderCompleted)

	takeAway := order.NextStatuses(model.OrderStoreAccepted, model.TakeAway)
	assert.Contains(t, takeAway, model.OrderCompleted)
	assert.NotContains(t, takeAway, model.OrderSearchingDriver)
}

func TestNextStatusesIsEmptyForTerminalStates(t *testing.T) {
	for _, status := range []model.OrderStatus{
		model.OrderCompleted, model.OrderCanceled, model.OrderStoreRejected,
	} {
		assert.Empty(t, order.NextStatuses(status, model.Delivery), "%s should be terminal", status)
	}
}

// --- ChangeStatus ------------------------------------------------------

func TestChangeStatusAppliesALegalTransition(t *testing.T) {
	repo := &fakeRepo{detail: detailWith(model.OrderPaid, model.Delivery, uuid.New())}
	actor := uuid.New()

	updated, err := newService(repo).ChangeStatus(
		context.Background(),
		order.Caller{UserID: actor, Role: order.RoleSuperAdmin},
		repo.detail.ID,
		string(model.OrderWaitingStore),
		"",
	)

	require.NoError(t, err)
	assert.Equal(t, string(model.OrderWaitingStore), updated.Status)
	assert.Equal(t, model.OrderPaid, repo.lastChange.From)
	assert.Equal(t, model.OrderWaitingStore, repo.lastChange.To)
	assert.Equal(t, actor, repo.lastChange.ActorID, "the actor comes from the token, not the body")
}

func TestChangeStatusRejectsAnIllegalTransition(t *testing.T) {
	repo := &fakeRepo{detail: detailWith(model.OrderPaid, model.Delivery, uuid.New())}

	_, err := newService(repo).ChangeStatus(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		repo.detail.ID,
		string(model.OrderDelivered),
		"",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, statusOf(t, err))
	assert.Zero(t, repo.applyCalls, "nothing may be written when the transition is refused")
}

func TestChangeStatusRejectsAnUnknownStatus(t *testing.T) {
	repo := &fakeRepo{detail: detailWith(model.OrderPaid, model.Delivery, uuid.New())}

	_, err := newService(repo).ChangeStatus(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		repo.detail.ID,
		"DEFINITELY_NOT_A_STATUS",
		"",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.applyCalls)
}

func TestChangeStatusDemandsAReasonForCancellation(t *testing.T) {
	repo := &fakeRepo{detail: detailWith(model.OrderPaid, model.Delivery, uuid.New())}
	svc := newService(repo)
	caller := order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin}

	_, err := svc.ChangeStatus(context.Background(), caller, repo.detail.ID, string(model.OrderCanceled), "   ")
	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.applyCalls)

	_, err = svc.ChangeStatus(context.Background(), caller, repo.detail.ID, string(model.OrderCanceled), "stok habis")
	require.NoError(t, err)
	assert.Equal(t, "stok habis", repo.lastChange.Reason)
}

func TestChangeStatusBoundsTheReason(t *testing.T) {
	repo := &fakeRepo{detail: detailWith(model.OrderPaid, model.Delivery, uuid.New())}

	_, err := newService(repo).ChangeStatus(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		repo.detail.ID,
		string(model.OrderCanceled),
		strings.Repeat("a", order.MaxReasonRunes+1),
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.applyCalls)
}

func TestChangeStatusReportsALostRaceAsConflict(t *testing.T) {
	repo := &fakeRepo{
		detail:   detailWith(model.OrderPaid, model.Delivery, uuid.New()),
		applyErr: order.ErrNotFound,
	}

	_, err := newService(repo).ChangeStatus(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		repo.detail.ID,
		string(model.OrderWaitingStore),
		"",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, statusOf(t, err),
		"a guarded UPDATE that matched nothing means somebody else moved the order")
}

// --- scoping -----------------------------------------------------------

func TestSuperAdminIsNotScopedToShops(t *testing.T) {
	repo := &fakeRepo{rows: []order.Summary{}, total: 0}

	_, _, err := newService(repo).List(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		order.ListQuery{Page: 1, PerPage: 20},
	)

	require.NoError(t, err)
	assert.Nil(t, repo.lastQuery.ScopedShopIDs, "nil means unrestricted")
}

func TestStaffAreScopedToTheirAssignedShops(t *testing.T) {
	assigned := []uuid.UUID{uuid.New(), uuid.New()}
	repo := &fakeRepo{rows: []order.Summary{}, assigned: assigned}

	_, _, err := newService(repo).List(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: "admin"},
		order.ListQuery{Page: 1, PerPage: 20},
	)

	require.NoError(t, err)
	assert.Equal(t, assigned, repo.lastQuery.ScopedShopIDs)
}

func TestStaffWithNoAssignmentSeeNothingRatherThanEverything(t *testing.T) {
	repo := &fakeRepo{rows: []order.Summary{}, assigned: nil}

	_, _, err := newService(repo).List(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: "admin"},
		order.ListQuery{Page: 1, PerPage: 20},
	)

	require.NoError(t, err)
	require.NotNil(t, repo.lastQuery.ScopedShopIDs,
		"nil would mean unrestricted, which is the opposite of what no assignment means")
	assert.Empty(t, repo.lastQuery.ScopedShopIDs)
}

func TestGetHidesAnOrderFromAnotherShop(t *testing.T) {
	repo := &fakeRepo{
		detail:   detailWith(model.OrderPaid, model.Delivery, uuid.New()),
		assigned: []uuid.UUID{uuid.New()}, // a different shop
	}

	_, err := newService(repo).Get(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: "admin"},
		repo.detail.ID,
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err),
		"404 rather than 403, so ids cannot be probed for existence")
}

func TestGetAllowsAnOrderFromAnAssignedShop(t *testing.T) {
	shopID := uuid.New()
	repo := &fakeRepo{
		detail:   detailWith(model.OrderStoreAccepted, model.TakeAway, shopID),
		assigned: []uuid.UUID{shopID},
	}

	detail, err := newService(repo).Get(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: "admin"},
		repo.detail.ID,
	)

	require.NoError(t, err)
	assert.Contains(t, detail.NextStatuses, string(model.OrderCompleted))
	assert.NotContains(t, detail.NextStatuses, string(model.OrderSearchingDriver),
		"a take-away order must not offer the courier branch")
	assert.Contains(t, detail.ReasonRequiredFor, string(model.OrderCanceled))
}

func TestGetSerialisesTerminalOrdersWithEmptySlices(t *testing.T) {
	repo := &fakeRepo{detail: detailWith(model.OrderCompleted, model.Delivery, uuid.New())}

	detail, err := newService(repo).Get(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		repo.detail.ID,
	)

	require.NoError(t, err)
	assert.NotNil(t, detail.NextStatuses, "must serialise as [] rather than null")
	assert.Empty(t, detail.NextStatuses)
	assert.NotNil(t, detail.ReasonRequiredFor)
}

func TestGetTranslatesAMissingOrder(t *testing.T) {
	repo := &fakeRepo{findErr: order.ErrNotFound}

	_, err := newService(repo).Get(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		uuid.New(),
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

// --- the customer's own orders -----------------------------------------

func TestMyOrdersIgnoresACustomerIdFromTheQueryString(t *testing.T) {
	caller := uuid.New()
	someoneElse := uuid.New()
	repo := &fakeRepo{rows: []order.Summary{}}

	_, _, err := newService(repo).MyOrders(
		context.Background(),
		caller,
		// A hand-crafted request trying to read another account's history.
		order.ListQuery{
			Filters: order.Filters{CustomerID: someoneElse},
			Page:    1,
			PerPage: 20,
		},
	)

	require.NoError(t, err)
	assert.Equal(t, caller, repo.lastQuery.CustomerID,
		"the scope must be overwritten by the token, never merely defaulted")
	assert.Nil(t, repo.lastQuery.ScopedShopIDs,
		"a customer is scoped by account, not by shop")
}

func TestMyOrderRefusesSomebodyElsesOrder(t *testing.T) {
	detail := detailWith(model.OrderPaid, model.Delivery, uuid.New())
	detail.CustomerID = uuid.New()
	repo := &fakeRepo{detail: detail}

	_, err := newService(repo).MyOrder(context.Background(), uuid.New(), detail.ID)

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err),
		"404 rather than 403, so order ids cannot be enumerated")
}

func TestMyOrderReturnsTheCallersOwnOrder(t *testing.T) {
	customer := uuid.New()
	detail := detailWith(model.OrderStoreAccepted, model.Delivery, uuid.New())
	detail.CustomerID = customer
	repo := &fakeRepo{detail: detail}

	got, err := newService(repo).MyOrder(context.Background(), customer, detail.ID)

	require.NoError(t, err)
	assert.Equal(t, customer, got.CustomerID)
	assert.Contains(t, got.NextStatuses, string(model.OrderSearchingDriver))
	assert.Empty(t, got.ReasonRequiredFor,
		"a customer never gets the transition buttons, so nothing demands a reason")
}

func TestMyOrderTimelineHidesTheStaffMemberWhoActed(t *testing.T) {
	customer := uuid.New()
	actor := uuid.New()
	detail := detailWith(model.OrderCanceled, model.Delivery, uuid.New())
	detail.CustomerID = customer

	repo := &fakeRepo{
		detail: detail,
		timeline: []order.TimelineEntry{
			{
				ID:        uuid.New(),
				ToStatus:  string(model.OrderCanceled),
				Reason:    "stok habis",
				ActorID:   &actor,
				ActorName: "Budi dari Toko Pusat",
			},
		},
	}

	rows, err := newService(repo).MyOrderTimeline(context.Background(), customer, detail.ID)

	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "stok habis", rows[0].Reason, "the customer keeps the why")
	assert.Empty(t, rows[0].ActorName, "but not the name of the employee")
	assert.Nil(t, rows[0].ActorID)
}

func TestMyOrderTimelineRefusesSomebodyElsesOrder(t *testing.T) {
	detail := detailWith(model.OrderPaid, model.Delivery, uuid.New())
	detail.CustomerID = uuid.New()
	repo := &fakeRepo{detail: detail}

	_, err := newService(repo).MyOrderTimeline(context.Background(), uuid.New(), detail.ID)

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestListHidesTheDriverErrorFromTheClient(t *testing.T) {
	repo := &fakeRepo{listErr: errors.New("pq: relation \"orders\" does not exist")}

	_, _, err := newService(repo).List(
		context.Background(),
		order.Caller{UserID: uuid.New(), Role: order.RoleSuperAdmin},
		order.ListQuery{Page: 1, PerPage: 20},
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err))

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "pq:", "driver text must never reach the client")
}
