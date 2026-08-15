package point_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
	"service_nusantara/internal/modules/point"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	exists  bool
	balance point.Balance

	lastAdjustment point.Adjustment
	adjustCalls    int
	adjustErr      error
}

func (f *fakeRepo) AccountExists(context.Context, uuid.UUID) (bool, error) {
	return f.exists, nil
}

func (f *fakeRepo) Balance(context.Context, uuid.UUID) (point.Balance, error) {
	return f.balance, nil
}

func (f *fakeRepo) History(context.Context, point.HistoryQuery) ([]point.Entry, int64, error) {
	return []point.Entry{}, 0, nil
}

func (f *fakeRepo) Adjust(_ context.Context, adjustment point.Adjustment) error {
	f.adjustCalls++
	f.lastAdjustment = adjustment
	if f.adjustErr != nil {
		return f.adjustErr
	}

	delta := adjustment.Points
	if adjustment.Direction == point.DirectionOut {
		delta = -delta
	}
	// A correct implementation moves both halves together, which is what the
	// tests below rely on to observe the result.
	f.balance.Cached += delta
	f.balance.Ledger += delta
	f.balance.Drift = f.balance.Cached - f.balance.Ledger
	return nil
}

func (f *fakeRepo) ClaimedVouchers(context.Context, uuid.UUID) ([]point.ClaimedVoucher, error) {
	return []point.ClaimedVoucher{}, nil
}

func (f *fakeRepo) Claimants(context.Context, uuid.UUID, int, int) ([]point.Claimant, int64, error) {
	return []point.Claimant{}, 0, nil
}

// --- helpers -----------------------------------------------------------

func newService(repo point.Repository) *point.Service {
	return point.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func repoWith(cached, ledger int64) *fakeRepo {
	return &fakeRepo{
		exists: true,
		balance: point.Balance{
			Cached: cached,
			Ledger: ledger,
			Drift:  cached - ledger,
		},
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- reconciliation ----------------------------------------------------

func TestBalanceReportsDriftRatherThanHidingIt(t *testing.T) {
	// The reported symptom: the screen says 500, the history adds up to 800.
	repo := repoWith(500, 800)

	balance, err := newService(repo).Balance(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, int64(500), balance.Cached)
	assert.Equal(t, int64(800), balance.Ledger)
	assert.Equal(t, int64(-300), balance.Drift)
	assert.False(t, balance.IsReconciled(),
		"the whole point of carrying both numbers is that a mismatch is detectable")
}

func TestBalanceIsReconciledWhenTheTwoAgree(t *testing.T) {
	repo := repoWith(800, 800)

	balance, err := newService(repo).Balance(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Zero(t, balance.Drift)
	assert.True(t, balance.IsReconciled())
}

func TestBalanceRejectsAnUnknownAccount(t *testing.T) {
	repo := &fakeRepo{exists: false}

	_, err := newService(repo).Balance(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err),
		"an account with no points is a zero balance; a missing account is a 404")
}

// --- adjustments -------------------------------------------------------

func TestAdjustRecordsAMovementRatherThanOverwritingTheTotal(t *testing.T) {
	repo := repoWith(500, 500)
	actor := uuid.New()

	updated, err := newService(repo).Adjust(
		context.Background(), actor, uuid.New(), 300, "in", "kompensasi keluhan #4821",
	)

	require.NoError(t, err)
	assert.Equal(t, int64(800), updated.Ledger)
	assert.Equal(t, int64(800), updated.Cached)
	assert.Equal(t, "kompensasi keluhan #4821", repo.lastAdjustment.Reason)
	assert.Equal(t, actor, repo.lastAdjustment.ActorID,
		"the actor comes from the token, so every correction is attributable")
	assert.Equal(t, int64(300), repo.lastAdjustment.Points,
		"points are stored positive; direction carries the sign")
}

func TestAdjustAlwaysDemandsAReason(t *testing.T) {
	for _, direction := range []string{"in", "out"} {
		t.Run(direction, func(t *testing.T) {
			repo := repoWith(500, 500)

			_, err := newService(repo).Adjust(
				context.Background(), uuid.New(), uuid.New(), 100, direction, "   ",
			)

			require.Error(t, err)
			assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
			assert.Zero(t, repo.adjustCalls,
				"a grant needs justifying as much as a deduction")
		})
	}
}

func TestAdjustRefusesADeductionLargerThanTheLedger(t *testing.T) {
	// The cache says there is plenty; the ledger -- the truth -- says otherwise.
	repo := repoWith(5000, 100)

	_, err := newService(repo).Adjust(
		context.Background(), uuid.New(), uuid.New(), 500, "out", "koreksi",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, statusOf(t, err))
	assert.Zero(t, repo.adjustCalls,
		"authorising against a drifted cache would deepen the inconsistency")
}

func TestAdjustRejectsANonPositiveAmount(t *testing.T) {
	for _, points := range []int64{0, -50} {
		repo := repoWith(500, 500)

		_, err := newService(repo).Adjust(
			context.Background(), uuid.New(), uuid.New(), points, "in", "apa pun",
		)

		require.Error(t, err)
		assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
		assert.Zero(t, repo.adjustCalls)
	}
}

func TestAdjustRejectsAnImplausiblyLargeAmount(t *testing.T) {
	repo := repoWith(500, 500)

	_, err := newService(repo).Adjust(
		context.Background(), uuid.New(), uuid.New(),
		point.MaxAdjustment+1, "in", "salah ketik nol",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.adjustCalls)
}

func TestAdjustRejectsAnUnknownDirection(t *testing.T) {
	repo := repoWith(500, 500)

	_, err := newService(repo).Adjust(
		context.Background(), uuid.New(), uuid.New(), 100, "sideways", "apa pun",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.adjustCalls)
}

func TestAdjustBoundsTheReason(t *testing.T) {
	repo := repoWith(500, 500)

	_, err := newService(repo).Adjust(
		context.Background(), uuid.New(), uuid.New(), 100, "in",
		strings.Repeat("a", point.MaxReasonRunes+1),
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.adjustCalls)
}

func TestHistoryRejectsAnUnknownDirectionFilter(t *testing.T) {
	repo := repoWith(0, 0)

	_, _, err := newService(repo).History(context.Background(), point.HistoryQuery{
		UserID:    uuid.New(),
		Page:      1,
		PerPage:   20,
		Direction: "inward",
	})

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
}

// --- routing -----------------------------------------------------------

func passthrough(next http.Handler) http.Handler { return next }

// TestRegisterDoesNotCollideWithTheAccountRoutes guards the arrangement the
// routes.go comment describes: the point paths hang off /user/{id}, which
// already carries the customer module's own patterns.
func TestRegisterDoesNotCollideWithTheAccountRoutes(t *testing.T) {
	mux := http.NewServeMux()

	// The customer module's patterns, registered first, as the server does.
	mux.Handle("GET /api/v1/user", http.NotFoundHandler())
	mux.Handle("GET /api/v1/user/roles", http.NotFoundHandler())
	mux.Handle("GET /api/v1/user/{id}", http.NotFoundHandler())
	mux.Handle("PUT /api/v1/user/{id}/status", http.NotFoundHandler())

	require.NotPanics(t, func() {
		point.Register(
			mux, "/api/v1", point.NewHandler(nil),
			middleware.Middleware(passthrough), middleware.Middleware(passthrough),
			middleware.Middleware(passthrough), middleware.Middleware(passthrough),
			middleware.Middleware(passthrough),
		)
	})

	const id = "0f7d1e2a-0000-0000-0000-000000000000"
	cases := []struct{ method, path, want string }{
		{"GET", "/api/v1/user/roles", "GET /api/v1/user/roles"},
		{"GET", "/api/v1/user/" + id, "GET /api/v1/user/{id}"},
		{"GET", "/api/v1/user/" + id + "/point", "GET /api/v1/user/{id}/point"},
		{"GET", "/api/v1/user/" + id + "/point/history", "GET /api/v1/user/{id}/point/history"},
		{"GET", "/api/v1/user/" + id + "/voucher", "GET /api/v1/user/{id}/voucher"},
		{"POST", "/api/v1/user/" + id + "/point/adjust", "POST /api/v1/user/{id}/point/adjust"},
		{"GET", "/api/v1/voucher/" + id + "/claims", "GET /api/v1/voucher/{id}/claims"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request, err := http.NewRequest(tc.method, "http://example.test"+tc.path, nil)
			require.NoError(t, err)

			_, pattern := mux.Handler(request)
			assert.Equal(t, tc.want, pattern)
		})
	}
}
