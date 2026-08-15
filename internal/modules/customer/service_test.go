package customer_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/customer"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	detail   customer.Detail
	rows     []customer.Summary
	total    int64
	roles    []string
	findErr  error
	applyErr error
	listErr  error

	lastChange customer.StatusChange
	applyCalls int
}

func (f *fakeRepo) List(context.Context, customer.ListQuery) ([]customer.Summary, int64, error) {
	if f.listErr != nil {
		return nil, 0, f.listErr
	}
	return f.rows, f.total, nil
}

func (f *fakeRepo) FindByID(context.Context, uuid.UUID) (customer.Detail, error) {
	if f.findErr != nil {
		return customer.Detail{}, f.findErr
	}
	return f.detail, nil
}

func (f *fakeRepo) ApplyStatus(_ context.Context, change customer.StatusChange) error {
	f.applyCalls++
	f.lastChange = change
	if f.applyErr != nil {
		return f.applyErr
	}
	f.detail.Status = change.Status
	return nil
}

func (f *fakeRepo) RoleNames(context.Context) ([]string, error) { return f.roles, nil }

type fakeRevoker struct {
	calls    []string
	failWith error
}

func (f *fakeRevoker) RevokeAllForUser(_ context.Context, userID string, _ time.Duration) error {
	f.calls = append(f.calls, userID)
	return f.failWith
}

// --- helpers -----------------------------------------------------------

func newService(repo customer.Repository, revoker customer.SessionRevoker) *customer.Service {
	return customer.NewService(
		repo, revoker, 15*time.Minute,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
}

func accountWith(status int) customer.Detail {
	return customer.Detail{
		Summary: customer.Summary{ID: uuid.New(), Name: "Siti", Status: status},
	}
}

func statusOf(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- blocking ----------------------------------------------------------

func TestBlockingRevokesEverySession(t *testing.T) {
	target := accountWith(customer.StatusActive)
	repo := &fakeRepo{detail: target}
	revoker := &fakeRevoker{}
	actor := uuid.New()

	updated, err := newService(repo, revoker).SetStatus(
		context.Background(), actor, target.ID, customer.StatusBlocked, "pola order-batal berulang",
	)

	require.NoError(t, err)
	assert.Equal(t, customer.StatusBlocked, updated.Status)
	assert.Equal(t, []string{target.ID.String()}, revoker.calls,
		"a block that leaves sessions alive is not a block: user.Refresh does not re-check users.status")
	assert.Equal(t, "pola order-batal berulang", repo.lastChange.Reason)
	assert.Equal(t, actor, repo.lastChange.ActorID, "the actor comes from the token, not the body")
	assert.Equal(t, "BLOCKED", repo.lastChange.Action())
}

func TestBlockingDemandsAReason(t *testing.T) {
	target := accountWith(customer.StatusActive)
	repo := &fakeRepo{detail: target}
	revoker := &fakeRevoker{}

	_, err := newService(repo, revoker).SetStatus(
		context.Background(), uuid.New(), target.ID, customer.StatusBlocked, "   ",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.applyCalls, "nothing may be written without a recorded reason")
	assert.Empty(t, revoker.calls)
}

func TestUnblockingDoesNotRequireAReasonAndDoesNotRevoke(t *testing.T) {
	target := accountWith(customer.StatusBlocked)
	repo := &fakeRepo{detail: target}
	revoker := &fakeRevoker{}

	updated, err := newService(repo, revoker).SetStatus(
		context.Background(), uuid.New(), target.ID, customer.StatusActive, "",
	)

	require.NoError(t, err)
	assert.Equal(t, customer.StatusActive, updated.Status)
	assert.Equal(t, "UNBLOCKED", repo.lastChange.Action())
	assert.Empty(t, revoker.calls,
		"restoring access has no sessions to end -- they were already revoked by the block")
}

func TestAnOperatorCannotBlockThemselves(t *testing.T) {
	actor := uuid.New()
	target := accountWith(customer.StatusActive)
	target.ID = actor
	repo := &fakeRepo{detail: target}
	revoker := &fakeRevoker{}

	_, err := newService(repo, revoker).SetStatus(
		context.Background(), actor, actor, customer.StatusBlocked, "salah klik",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, statusOf(t, err))
	assert.Zero(t, repo.applyCalls)
	assert.Empty(t, revoker.calls,
		"self-revocation would lock the operator out with no way back in")
}

func TestAFailedRevocationIsReportedRatherThanSwallowed(t *testing.T) {
	target := accountWith(customer.StatusActive)
	repo := &fakeRepo{detail: target}
	revoker := &fakeRevoker{failWith: errors.New("redis: connection refused")}

	_, err := newService(repo, revoker).SetStatus(
		context.Background(), uuid.New(), target.ID, customer.StatusBlocked, "penipuan",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err),
		"the row says blocked but the account may still hold a working token; the operator must be told")

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "redis:", "driver text must never reach the client")
	assert.Equal(t, 1, repo.applyCalls, "the block itself was persisted, so a retry is safe")
}

func TestRejectsAStatusOutsideTheTwoItKnows(t *testing.T) {
	target := accountWith(customer.StatusActive)
	repo := &fakeRepo{detail: target}

	_, err := newService(repo, &fakeRevoker{}).SetStatus(
		context.Background(), uuid.New(), target.ID, 7, "apa pun",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.applyCalls)
}

func TestRejectsARedundantChange(t *testing.T) {
	target := accountWith(customer.StatusBlocked)
	repo := &fakeRepo{detail: target}

	_, err := newService(repo, &fakeRevoker{}).SetStatus(
		context.Background(), uuid.New(), target.ID, customer.StatusBlocked, "sudah diblokir",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, statusOf(t, err))
	assert.Zero(t, repo.applyCalls,
		"a second identical block would add a misleading audit row")
}

func TestBoundsTheReason(t *testing.T) {
	target := accountWith(customer.StatusActive)
	repo := &fakeRepo{detail: target}

	_, err := newService(repo, &fakeRevoker{}).SetStatus(
		context.Background(), uuid.New(), target.ID, customer.StatusBlocked,
		strings.Repeat("a", customer.MaxReasonRunes+1),
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, statusOf(t, err))
	assert.Zero(t, repo.applyCalls)
}

func TestReportsALostRaceAsConflict(t *testing.T) {
	target := accountWith(customer.StatusActive)
	repo := &fakeRepo{detail: target, applyErr: customer.ErrNotFound}

	_, err := newService(repo, &fakeRevoker{}).SetStatus(
		context.Background(), uuid.New(), target.ID, customer.StatusBlocked, "penipuan",
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, statusOf(t, err))
}

// --- reads -------------------------------------------------------------

func TestGetTranslatesAMissingAccount(t *testing.T) {
	repo := &fakeRepo{findErr: customer.ErrNotFound}

	_, err := newService(repo, &fakeRevoker{}).Get(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, statusOf(t, err))
}

func TestListHidesTheDriverErrorFromTheClient(t *testing.T) {
	repo := &fakeRepo{listErr: errors.New(`pq: relation "users" does not exist`)}

	_, _, err := newService(repo, &fakeRevoker{}).List(
		context.Background(), customer.ListQuery{Page: 1, PerPage: 20},
	)

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err))

	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "pq:")
}
