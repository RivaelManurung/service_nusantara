package review_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/review"
)

// --- fakes -------------------------------------------------------------

type fakeRepo struct {
	rows      map[uuid.UUID]review.Review
	lastQuery review.ListQuery
	failWith  error
	deleted   int
}

func newRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]review.Review{}}
}

func (f *fakeRepo) add(row review.Review) review.Review {
	if row.ID == uuid.Nil {
		row.ID = uuid.New()
	}
	f.rows[row.ID] = row
	return row
}

func (f *fakeRepo) List(_ context.Context, query review.ListQuery) ([]review.Review, int64, error) {
	f.lastQuery = query
	if f.failWith != nil {
		return nil, 0, f.failWith
	}

	items := make([]review.Review, 0, len(f.rows))
	for _, row := range f.rows {
		if query.Rating != nil && row.Rating != *query.Rating {
			continue
		}
		if query.Status != nil && row.Status != *query.Status {
			continue
		}
		items = append(items, row)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) FindByID(_ context.Context, id uuid.UUID) (review.Review, error) {
	row, ok := f.rows[id]
	if !ok {
		return review.Review{}, review.ErrNotFound
	}
	return row, nil
}

func (f *fakeRepo) UpdateStatus(_ context.Context, id uuid.UUID, status int) error {
	row, ok := f.rows[id]
	if !ok {
		return review.ErrNotFound
	}
	row.Status = status
	f.rows[id] = row
	return nil
}

func (f *fakeRepo) Delete(_ context.Context, id uuid.UUID) error {
	if _, ok := f.rows[id]; !ok {
		return review.ErrNotFound
	}
	delete(f.rows, id)
	f.deleted++
	return nil
}

// --- helpers -----------------------------------------------------------

func newService(repo *fakeRepo) *review.Service {
	return review.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func status(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

func page(rating, reviewStatus *int) review.ListQuery {
	return review.ListQuery{Page: 1, PerPage: 20, Rating: rating, Status: reviewStatus}
}

func intPtr(value int) *int { return &value }

// --- tests -------------------------------------------------------------

func TestListReturnsEveryReviewWhenNoFilterIsGiven(t *testing.T) {
	// Arrange
	repo := newRepo()
	repo.add(review.Review{Rating: 5, Status: review.StatusVisible})
	repo.add(review.Review{Rating: 1, Status: review.StatusHidden})
	service := newService(repo)

	// Act
	items, total, err := service.List(context.Background(), page(nil, nil))

	// Assert
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, int64(2), total)
}

func TestListFiltersByRating(t *testing.T) {
	repo := newRepo()
	repo.add(review.Review{Rating: 5, Status: review.StatusVisible})
	repo.add(review.Review{Rating: 2, Status: review.StatusVisible})
	service := newService(repo)

	items, _, err := service.List(context.Background(), page(intPtr(2), nil))

	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 2, items[0].Rating)
}

func TestListCanFilterToHiddenReviews(t *testing.T) {
	// Hidden is status 0, which is also the zero value: a plain int filter would
	// make this page unreachable.
	repo := newRepo()
	repo.add(review.Review{Rating: 5, Status: review.StatusVisible})
	hidden := repo.add(review.Review{Rating: 3, Status: review.StatusHidden})
	service := newService(repo)

	items, _, err := service.List(context.Background(), page(nil, intPtr(review.StatusHidden)))

	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, hidden.ID, items[0].ID)
}

func TestListRejectsARatingOutsideTheAllowedRange(t *testing.T) {
	// Arrange: a 7-star filter can only ever return nothing, which reads as
	// "there are no reviews" rather than "that filter is impossible".
	repo := newRepo()
	service := newService(repo)

	// Act
	_, _, err := service.List(context.Background(), page(intPtr(7), nil))

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestListRejectsAnUnknownStatusFilter(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	_, _, err := service.List(context.Background(), page(nil, intPtr(9)))

	require.Error(t, err)
	assert.Equal(t, http.StatusUnprocessableEntity, status(t, err))
}

func TestListReportsARepositoryFailureAsAnInternalErrorWithoutLeakingIt(t *testing.T) {
	repo := newRepo()
	repo.failWith = errors.New(`pq: relation "reviews" does not exist`)
	service := newService(repo)

	_, _, err := service.List(context.Background(), page(nil, nil))

	require.Error(t, err)
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusInternalServerError, appErr.Status)
	assert.NotContains(t, appErr.Message, "pq:")
}

func TestGetReportsAMissingReviewAsNotFound(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	_, err := service.Get(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestGetReturnsTheJoinedProductAndReviewerNames(t *testing.T) {
	repo := newRepo()
	created := repo.add(review.Review{
		ProductName:  "Keripik Singkong",
		ReviewerName: "Siti",
		Rating:       4,
		Comment:      "Renyah",
		Status:       review.StatusVisible,
	})
	service := newService(repo)

	got, err := service.Get(context.Background(), created.ID)

	require.NoError(t, err)
	assert.Equal(t, "Keripik Singkong", got.ProductName)
	assert.Equal(t, "Siti", got.ReviewerName)
}

func TestSetStatusHidesAReview(t *testing.T) {
	repo := newRepo()
	created := repo.add(review.Review{Rating: 1, Status: review.StatusVisible})
	service := newService(repo)

	require.NoError(t, service.SetStatus(context.Background(), created.ID, review.StatusHidden))
	assert.Equal(t, review.StatusHidden, repo.rows[created.ID].Status)
}

func TestSetStatusClampsAnOutOfRangeValue(t *testing.T) {
	repo := newRepo()
	created := repo.add(review.Review{Rating: 5, Status: review.StatusVisible})
	service := newService(repo)

	require.NoError(t, service.SetStatus(context.Background(), created.ID, 9))
	assert.Equal(t, review.StatusHidden, repo.rows[created.ID].Status)
}

func TestSetStatusReportsAMissingReviewAsNotFound(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	err := service.SetStatus(context.Background(), uuid.New(), review.StatusHidden)

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}

func TestDeleteRemovesTheReview(t *testing.T) {
	repo := newRepo()
	created := repo.add(review.Review{Rating: 3, Status: review.StatusVisible})
	service := newService(repo)

	require.NoError(t, service.Delete(context.Background(), created.ID))
	assert.Equal(t, 1, repo.deleted)
	assert.Empty(t, repo.rows)
}

func TestDeleteReportsAMissingReviewAsNotFound(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	err := service.Delete(context.Background(), uuid.New())

	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
}
