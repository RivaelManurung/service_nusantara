package event_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/event"
	"service_nusantara/internal/platform/storage"
)

// --- fakes -------------------------------------------------------------
//
// The ports are small enough that in-memory fakes cover the business rules
// without PostgreSQL or Cloudinary.

type fakeRepo struct {
	events   map[uuid.UUID]event.Event
	children map[uuid.UUID]event.Children
	prices   map[uuid.UUID]int
	names    map[string]bool

	created  event.Children
	updated  event.Children
	failWith error
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		events:   map[uuid.UUID]event.Event{},
		children: map[uuid.UUID]event.Children{},
		prices:   map[uuid.UUID]int{},
		names:    map[string]bool{},
	}
}

func (f *fakeRepo) List(context.Context, event.ListQuery) ([]event.Event, int64, error) {
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	items := make([]event.Event, 0, len(f.events))
	for _, e := range f.events {
		items = append(items, e)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (event.Event, error) {
	row, ok := f.events[id]
	if !ok {
		return event.Event{}, event.ErrNotFound
	}
	return row, nil
}

func (f *fakeRepo) ExistsByName(_ context.Context, name string, _ uuid.UUID) (bool, error) {
	return f.names[name], nil
}

func (f *fakeRepo) ProductPrices(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]int, error) {
	found := map[uuid.UUID]int{}
	for _, id := range ids {
		if price, ok := f.prices[id]; ok {
			found[id] = price
		}
	}
	return found, nil
}

func (f *fakeRepo) Create(_ context.Context, row event.Event, children event.Children, _ uuid.UUID) (event.Event, error) {
	f.created = children
	f.events[row.ID] = row
	f.children[row.ID] = children
	return row, nil
}

func (f *fakeRepo) Update(_ context.Context, id uuid.UUID, row event.Event, children event.Children) (event.Event, error) {
	if _, ok := f.events[id]; !ok {
		return event.Event{}, event.ErrNotFound
	}
	f.updated = children
	row.ID = id
	f.events[id] = row
	// Replace, never append.
	f.children[id] = children
	return row, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID, status int) error {
	row, ok := f.events[id]
	if !ok {
		return event.ErrNotFound
	}
	row.Status = status
	f.events[id] = row
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.events[id]; !ok {
		return event.ErrNotFound
	}
	delete(f.events, id)
	delete(f.children, id)
	return nil
}

type fakeUploader struct {
	calls int
	err   error
}

func (f *fakeUploader) Upload(context.Context, *multipart.FileHeader, string) (storage.Uploaded, error) {
	f.calls++
	if f.err != nil {
		return storage.Uploaded{}, f.err
	}
	return storage.Uploaded{URL: "https://cdn.example/cover.jpg", PublicID: "cover"}, nil
}

// --- helpers -----------------------------------------------------------

func newService(repo *fakeRepo, images *fakeUploader) *event.Service {
	return event.NewService(repo, images, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func aCover() *multipart.FileHeader {
	return &multipart.FileHeader{Filename: "cover.jpg", Size: 1024}
}

func discountInput(productID uuid.UUID) event.Input {
	return event.Input{
		Name:      "Ramadan Sale",
		TypeEvent: event.TypeDiskon,
		StartDate: time.Now(),
		EndDate:   time.Now().Add(24 * time.Hour),
		Cover:     aCover(),
		Products: []event.ProductDiscountInput{
			{ProductID: productID, DiscountPercent: 10},
		},
	}
}

func bundleInput(buy, reward uuid.UUID) event.Input {
	return event.Input{
		Name:          "Buy One Get One",
		TypeEvent:     event.TypeBundle,
		StartDate:     time.Now(),
		EndDate:       time.Now().Add(24 * time.Hour),
		Cover:         aCover(),
		BundleBuys:    []event.BundleItemInput{{ProductID: buy, Quantity: 2}},
		BundleRewards: []event.BundleItemInput{{ProductID: reward, Quantity: 1}},
	}
}

// status asserts the HTTP status an error carries.
func status(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestCreateStoresDiscountRowsWithTheAmountDerivedFromThePrice(t *testing.T) {
	// Arrange
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 20_000
	service := newService(repo, &fakeUploader{})

	// Act
	created, err := service.Create(context.Background(), discountInput(productID))

	// Assert
	require.NoError(t, err)
	assert.Equal(t, "https://cdn.example/cover.jpg", created.Cover)
	require.Len(t, repo.created.Products, 1)
	assert.Equal(t, 10, repo.created.Products[0].DiscountPercent)
	assert.InDelta(t, 2_000, repo.created.Products[0].DiscountAmount, 0.001)
	assert.Empty(t, repo.created.BundleBuys)
}

func TestCreateStoresBothSidesOfABundle(t *testing.T) {
	buy, reward := uuid.New(), uuid.New()
	repo := newRepo()
	repo.prices[buy] = 10_000
	repo.prices[reward] = 5_000
	service := newService(repo, &fakeUploader{})

	_, err := service.Create(context.Background(), bundleInput(buy, reward))

	require.NoError(t, err)
	assert.Len(t, repo.created.BundleBuys, 1)
	assert.Len(t, repo.created.BundleRewards, 1)
	assert.Empty(t, repo.created.Products)
}

func TestCreateRejectsADiskonEventCarryingBundleRows(t *testing.T) {
	// Arrange: mixed rows would leave two contradictory descriptions of the
	// same promotion, so the request is refused rather than half-applied.
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 10_000
	service := newService(repo, &fakeUploader{})

	input := discountInput(productID)
	input.BundleBuys = []event.BundleItemInput{{ProductID: productID, Quantity: 1}}

	// Act
	_, err := service.Create(context.Background(), input)

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
	assert.Empty(t, repo.events)
}

func TestCreateRejectsABundleEventCarryingDiscountRows(t *testing.T) {
	buy, reward := uuid.New(), uuid.New()
	repo := newRepo()
	repo.prices[buy] = 1
	repo.prices[reward] = 1
	service := newService(repo, &fakeUploader{})

	input := bundleInput(buy, reward)
	input.Products = []event.ProductDiscountInput{{ProductID: buy, DiscountPercent: 5}}

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateRejectsAnEndDateThatIsNotAfterTheStartDate(t *testing.T) {
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 10_000
	service := newService(repo, &fakeUploader{})

	input := discountInput(productID)
	input.EndDate = input.StartDate

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateRejectsABundleMissingItsRewardSide(t *testing.T) {
	buy := uuid.New()
	repo := newRepo()
	repo.prices[buy] = 10_000
	service := newService(repo, &fakeUploader{})

	input := bundleInput(buy, uuid.New())
	input.BundleRewards = nil

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateRejectsAProductThatDoesNotExist(t *testing.T) {
	repo := newRepo()
	service := newService(repo, &fakeUploader{})

	_, err := service.Create(context.Background(), discountInput(uuid.New()))

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestCreateRejectsADuplicateName(t *testing.T) {
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 10_000
	repo.names["Ramadan Sale"] = true
	service := newService(repo, &fakeUploader{})

	_, err := service.Create(context.Background(), discountInput(productID))

	require.Error(t, err)
	assert.Equal(t, http.StatusConflict, status(t, err))
}

func TestCreateDoesNotWriteARowWhenTheUploadFails(t *testing.T) {
	// Arrange
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 10_000
	images := &fakeUploader{err: storage.ErrNotConfigured}
	service := newService(repo, images)

	// Act
	_, err := service.Create(context.Background(), discountInput(productID))

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, status(t, err))
	assert.Empty(t, repo.events, "no row may point at an image that was never stored")
}

func TestCreateRequiresACover(t *testing.T) {
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 10_000
	service := newService(repo, &fakeUploader{})

	input := discountInput(productID)
	input.Cover = nil

	_, err := service.Create(context.Background(), input)

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestUpdateReplacesTheChildRowsRatherThanAppending(t *testing.T) {
	// Arrange
	first, second := uuid.New(), uuid.New()
	repo := newRepo()
	repo.prices[first] = 10_000
	repo.prices[second] = 30_000
	service := newService(repo, &fakeUploader{})

	created, err := service.Create(context.Background(), discountInput(first))
	require.NoError(t, err)

	// Act
	next := discountInput(second)
	next.Cover = nil
	_, err = service.Update(context.Background(), created.ID, next)

	// Assert
	require.NoError(t, err)
	require.Len(t, repo.updated.Products, 1)
	assert.Equal(t, second, repo.updated.Products[0].ProductID)
	assert.Len(t, repo.children[created.ID].Products, 1)
}

func TestUpdateKeepsTheStoredCoverWhenNoFileIsSent(t *testing.T) {
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 10_000
	images := &fakeUploader{}
	service := newService(repo, images)

	created, err := service.Create(context.Background(), discountInput(productID))
	require.NoError(t, err)

	next := discountInput(productID)
	next.Cover = nil
	updated, err := service.Update(context.Background(), created.ID, next)

	require.NoError(t, err)
	assert.Equal(t, 1, images.calls, "the cover must not be re-uploaded")
	assert.Empty(t, updated.Cover, "an empty cover tells the repository to keep the stored one")
}

func TestUpdateReportsAMissingEventAsNotFound(t *testing.T) {
	repo := newRepo()
	service := newService(repo, &fakeUploader{})

	_, err := service.Update(context.Background(), uuid.New(), discountInput(uuid.New()))

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestSetStatusClampsAnOutOfRangeValue(t *testing.T) {
	productID := uuid.New()
	repo := newRepo()
	repo.prices[productID] = 10_000
	service := newService(repo, &fakeUploader{})

	created, err := service.Create(context.Background(), discountInput(productID))
	require.NoError(t, err)

	require.NoError(t, service.SetStatus(context.Background(), created.ID, 7))
	assert.Equal(t, event.StatusInactive, repo.events[created.ID].Status)
}

func TestDeleteReportsAMissingEventAsNotFound(t *testing.T) {
	repo := newRepo()
	service := newService(repo, &fakeUploader{})

	err := service.Delete(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestListReportsARepositoryFailureAsAnInternalErrorWithoutLeakingIt(t *testing.T) {
	repo := newRepo()
	repo.failWith = errors.New(`pq: relation "events" does not exist`)
	service := newService(repo, &fakeUploader{})

	_, _, err := service.List(context.Background(), event.ListQuery{Page: 1, PerPage: 10})

	require.Error(t, err)
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	assert.NotContains(t, appErr.Message, "pq:")
}
