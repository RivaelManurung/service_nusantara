package voucher_test

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
	"service_nusantara/internal/modules/voucher"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	rows     map[uuid.UUID]voucher.Voucher
	claimed  map[uuid.UUID]bool
	failWith error
	deleted  int
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		rows:    map[uuid.UUID]voucher.Voucher{},
		claimed: map[uuid.UUID]bool{},
	}
}

func (f *fakeRepo) List(context.Context, voucher.ListQuery) ([]voucher.Voucher, int64, error) {
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	items := make([]voucher.Voucher, 0, len(f.rows))
	for _, row := range f.rows {
		items = append(items, row)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (voucher.Voucher, error) {
	row, ok := f.rows[id]
	if !ok {
		return voucher.Voucher{}, voucher.ErrNotFound
	}
	return row, nil
}

func (f *fakeRepo) ExistsByCode(_ context.Context, code string, excludeID uuid.UUID) (bool, error) {
	for id, row := range f.rows {
		if id != excludeID && strings.EqualFold(row.Code, code) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) Create(_ context.Context, row voucher.Voucher, _ uuid.UUID) (voucher.Voucher, error) {
	f.rows[row.ID] = row
	return row, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, row voucher.Voucher) (voucher.Voucher, error) {
	existing, ok := f.rows[id]
	if !ok {
		return voucher.Voucher{}, voucher.ErrNotFound
	}
	row.ID = id
	// Status is not part of an edit; /edit-status owns it.
	row.Status = existing.Status
	f.rows[id] = row
	return row, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID, status int) error {
	row, ok := f.rows[id]
	if !ok {
		return voucher.ErrNotFound
	}
	row.Status = status
	f.rows[id] = row
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.rows[id]; !ok {
		return voucher.ErrNotFound
	}
	delete(f.rows, id)
	f.deleted++
	return nil
}

func (f *fakeRepo) Claimed(_ context.Context, id uuid.UUID) (bool, error) {
	return f.claimed[id], nil
}

// --- helpers -----------------------------------------------------------

func newService(repo *fakeRepo) *voucher.Service {
	return voucher.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func percentInput() voucher.Input {
	return voucher.Input{
		Code:            "RAMADAN10",
		DiscountType:    voucher.DiscountPercent,
		DiscountPercent: 10,
		MinimumSpend:    50_000,
		PointCost:       100,
		Quota:           25,
		StartDate:       time.Now(),
		EndDate:         time.Now().Add(24 * time.Hour),
		Description:     "Ten percent off",
		Status:          voucher.StatusActive,
	}
}

func status(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestCreateStoresAPercentageVoucher(t *testing.T) {
	// Arrange
	repo := newRepo()
	service := newService(repo)

	// Act
	created, err := service.Create(context.Background(), percentInput())

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "RAMADAN10", created.Code)
	assert.Equal(t, 10, created.DiscountPercent)
	assert.Zero(t, created.DiscountAmount)
	assert.Equal(t, voucher.StatusActive, created.Status)
}

func TestCreateTrimsTheCode(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	input := percentInput()
	input.Code = "  RAMADAN10  "

	created, err := service.Create(context.Background(), input)

	require.NoError(t, err)
	assert.Equal(t, "RAMADAN10", created.Code)
}

func TestCreateRejectsAVoucherCarryingBothDiscountKinds(t *testing.T) {
	// Arrange: with both columns set there is no single answer to "how much is
	// off", so the request is refused rather than stored ambiguously.
	repo := newRepo()
	service := newService(repo)

	input := percentInput()
	input.DiscountAmount = 5_000

	// Act
	_, err := service.Create(context.Background(), input)

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
	assert.Empty(t, repo.rows)
}

func TestCreateRejectsANeitherDiscountVoucher(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	input := percentInput()
	input.DiscountPercent = 0

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateRejectsAnUnknownDiscountType(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	input := percentInput()
	input.DiscountType = "buy_one_get_one"

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateRejectsAPercentageAbove100(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	input := percentInput()
	input.DiscountPercent = 120

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateRejectsAnEndDateThatIsNotAfterTheStartDate(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	input := percentInput()
	input.EndDate = input.StartDate

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateReportsADuplicateCodeAsAConflictRatherThanADriverError(t *testing.T) {
	// Arrange
	repo := newRepo()
	service := newService(repo)
	_, err := service.Create(context.Background(), percentInput())
	require.NoError(t, err)

	// Act: the same code, differently cased.
	input := percentInput()
	input.Code = "ramadan10"
	_, err = service.Create(context.Background(), input)

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "duplicate key")
}

func TestUpdateAllowsAVoucherToKeepItsOwnCode(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	created, err := service.Create(context.Background(), percentInput())
	require.NoError(t, err)

	updated, err := service.Update(context.Background(), created.ID, percentInput())

	require.NoError(t, err)
	assert.Equal(t, "RAMADAN10", updated.Code)
}

func TestUpdateSwitchingToAnAmountClearsThePercentage(t *testing.T) {
	// Arrange
	repo := newRepo()
	service := newService(repo)
	created, err := service.Create(context.Background(), percentInput())
	require.NoError(t, err)

	// Act
	input := percentInput()
	input.DiscountType = voucher.DiscountAmount
	input.DiscountPercent = 0
	input.DiscountAmount = 5_000
	updated, err := service.Update(context.Background(), created.ID, input)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 5_000, updated.DiscountAmount)
	assert.Zero(t, updated.DiscountPercent)
}

func TestUpdateReportsAMissingVoucherAsNotFound(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	_, err := service.Update(context.Background(), uuid.New(), percentInput())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestDeleteRefusesAVoucherThatHasAlreadyBeenClaimed(t *testing.T) {
	// Arrange
	repo := newRepo()
	service := newService(repo)
	created, err := service.Create(context.Background(), percentInput())
	require.NoError(t, err)
	repo.claimed[created.ID] = true

	// Act
	err = service.Delete(context.Background(), created.ID)

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
	assert.Zero(t, repo.deleted, "a claimed voucher must survive in the holders' wallets")
}

func TestDeleteRemovesAnUnclaimedVoucher(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	created, err := service.Create(context.Background(), percentInput())
	require.NoError(t, err)

	require.NoError(t, service.Delete(context.Background(), created.ID))
	assert.Equal(t, 1, repo.deleted)
}

func TestDeleteReportsAMissingVoucherAsNotFound(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	err := service.Delete(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestSetStatusClampsAnOutOfRangeValue(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	created, err := service.Create(context.Background(), percentInput())
	require.NoError(t, err)

	require.NoError(t, service.SetStatus(context.Background(), created.ID, 9))
	assert.Equal(t, voucher.StatusInactive, repo.rows[created.ID].Status)
}

func TestListReportsARepositoryFailureAsAnInternalErrorWithoutLeakingIt(t *testing.T) {
	repo := newRepo()
	repo.failWith = errors.New(`pq: relation "vouchers" does not exist`)
	service := newService(repo)

	_, _, err := service.List(context.Background(), voucher.ListQuery{Page: 1, PerPage: 10})

	require.Error(t, err)
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	assert.NotContains(t, appErr.Message, "pq:")
}
