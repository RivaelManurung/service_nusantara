package notification_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/modules/notification"
)

// --- fakes -------------------------------------------------------------

// row keeps the owner next to the payload so the fake can enforce the same
// scoping rule the SQL does.
type row struct {
	item  notification.Notification
	owner uuid.UUID
}

type fakeRepo struct {
	rows     map[uuid.UUID]row
	failWith error
	// lastQuery records what the service asked for, so a test can assert the
	// owner was carried down rather than dropped.
	lastQuery notification.ListQuery
	markedAll []uuid.UUID
}

func newRepo() *fakeRepo {
	return &fakeRepo{rows: map[uuid.UUID]row{}}
}

func (f *fakeRepo) List(_ context.Context, query notification.ListQuery) ([]notification.Notification, int64, error) {
	f.lastQuery = query
	if f.failWith != nil {
		return nil, 0, f.failWith
	}

	items := make([]notification.Notification, 0, len(f.rows))
	for _, r := range f.rows {
		if r.owner != query.UserID {
			continue
		}
		if query.Channel != "" && r.item.Channel != query.Channel {
			continue
		}
		items = append(items, r.item)
	}
	return items, int64(len(items)), nil
}

func (f *fakeRepo) UnreadByChannel(_ context.Context, userID uuid.UUID) (map[string]int, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}

	counts := map[string]int{}
	for _, r := range f.rows {
		if r.owner != userID || r.item.IsRead {
			continue
		}
		counts[r.item.Channel]++
	}
	return counts, nil
}

func (f *fakeRepo) FindByIDForUser(_ context.Context, id, userID uuid.UUID) (notification.Notification, error) {
	if f.failWith != nil {
		return notification.Notification{}, f.failWith
	}
	r, ok := f.rows[id]
	if !ok || r.owner != userID {
		return notification.Notification{}, notification.ErrNotFound
	}
	return r.item, nil
}

func (f *fakeRepo) MarkRead(_ context.Context, id, userID uuid.UUID) error {
	r, ok := f.rows[id]
	if !ok || r.owner != userID {
		return notification.ErrNotFound
	}
	now := time.Now()
	r.item.IsRead = true
	r.item.ReadAt = &now
	f.rows[id] = r
	return nil
}

func (f *fakeRepo) MarkAllRead(_ context.Context, userID uuid.UUID, channel string) (int64, error) {
	if f.failWith != nil {
		return 0, f.failWith
	}

	var updated int64
	for id, r := range f.rows {
		if r.owner != userID || r.item.IsRead {
			continue
		}
		if channel != "" && r.item.Channel != channel {
			continue
		}
		now := time.Now()
		r.item.IsRead = true
		r.item.ReadAt = &now
		f.rows[id] = r
		f.markedAll = append(f.markedAll, id)
		updated++
	}
	return updated, nil
}

// --- helpers -----------------------------------------------------------

func newService(repo *fakeRepo) *notification.Service {
	return notification.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func seed(repo *fakeRepo, owner uuid.UUID, channel string) notification.Notification {
	item := notification.Notification{
		ID:        uuid.New(),
		Channel:   channel,
		Title:     "Pesanan berhasil dibuat",
		Type:      "SUCCESS",
		CreatedAt: time.Now(),
	}
	repo.rows[item.ID] = row{item: item, owner: owner}
	return item
}

func status(t *testing.T, err error) int {
	t.Helper()
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	return appErr.Status
}

// --- tests -------------------------------------------------------------

func TestListReturnsOnlyTheCallersNotifications(t *testing.T) {
	// Arrange
	repo := newRepo()
	me, someoneElse := uuid.New(), uuid.New()
	mine := seed(repo, me, notification.ChannelTransaksi)
	seed(repo, someoneElse, notification.ChannelTransaksi)
	service := newService(repo)

	// Act
	items, total, err := service.List(context.Background(), notification.ListQuery{
		UserID: me, Page: 1, PerPage: 20,
	})

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, items, 1)
	assert.Equal(t, mine.ID, items[0].ID)
	// The owner must reach the repository, not be applied afterwards.
	assert.Equal(t, me, repo.lastQuery.UserID)
}

func TestListUppercasesTheChannelFilter(t *testing.T) {
	repo := newRepo()
	me := uuid.New()
	seed(repo, me, notification.ChannelPromo)
	service := newService(repo)

	items, _, err := service.List(context.Background(), notification.ListQuery{
		UserID: me, Channel: "promo", Page: 1, PerPage: 20,
	})

	require.NoError(t, err)
	assert.Len(t, items, 1)
	assert.Equal(t, notification.ChannelPromo, repo.lastQuery.Channel)
}

func TestListRejectsAnUnknownChannel(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	_, _, err := service.List(context.Background(), notification.ListQuery{
		UserID: uuid.New(), Channel: "SPAM", Page: 1, PerPage: 20,
	})

	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, status(t, err))
}

func TestListRefusesAnAnonymousCaller(t *testing.T) {
	repo := newRepo()
	service := newService(repo)

	_, _, err := service.List(context.Background(), notification.ListQuery{Page: 1, PerPage: 20})

	require.Error(t, err)
	assert.Equal(t, http.StatusUnauthorized, status(t, err))
}

func TestUnreadCountCountsPerTabForTheCallerOnly(t *testing.T) {
	// Arrange
	repo := newRepo()
	me, someoneElse := uuid.New(), uuid.New()
	seed(repo, me, notification.ChannelTransaksi)
	seed(repo, me, notification.ChannelTransaksi)
	seed(repo, me, notification.ChannelPromo)
	seed(repo, someoneElse, notification.ChannelPromo)
	service := newService(repo)

	// Act
	counts, err := service.UnreadCount(context.Background(), me)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 2, counts.Transaksi)
	assert.Equal(t, 1, counts.Promo)
	assert.Equal(t, 3, counts.Total)
}

func TestUnreadCountIgnoresReadMessages(t *testing.T) {
	repo := newRepo()
	me := uuid.New()
	item := seed(repo, me, notification.ChannelTransaksi)
	svc := newService(repo)
	require.NoError(t, svc.MarkRead(context.Background(), me, item.ID))

	counts, err := svc.UnreadCount(context.Background(), me)

	require.NoError(t, err)
	assert.Zero(t, counts.Total)
}

func TestMarkReadAcknowledgesTheCallersOwnMessage(t *testing.T) {
	repo := newRepo()
	me := uuid.New()
	item := seed(repo, me, notification.ChannelTransaksi)
	svc := newService(repo)

	err := svc.MarkRead(context.Background(), me, item.ID)

	require.NoError(t, err)
	assert.True(t, repo.rows[item.ID].item.IsRead)
}

func TestMarkReadCannotTouchAnotherUsersMessage(t *testing.T) {
	// Arrange: the message exists, but not for this caller. Answering 404
	// rather than 403 keeps the endpoint from confirming that the id is real.
	repo := newRepo()
	me, someoneElse := uuid.New(), uuid.New()
	theirs := seed(repo, someoneElse, notification.ChannelTransaksi)
	svc := newService(repo)

	// Act
	err := svc.MarkRead(context.Background(), me, theirs.ID)

	// Assert
	require.Error(t, err)
	assert.Equal(t, http.StatusNotFound, status(t, err))
	assert.False(t, repo.rows[theirs.ID].item.IsRead)
}

func TestMarkReadOnAnAlreadyReadMessageIsNotAnError(t *testing.T) {
	repo := newRepo()
	me := uuid.New()
	item := seed(repo, me, notification.ChannelTransaksi)
	svc := newService(repo)
	require.NoError(t, svc.MarkRead(context.Background(), me, item.ID))

	err := svc.MarkRead(context.Background(), me, item.ID)

	require.NoError(t, err)
}

func TestMarkAllReadOnlyUpdatesTheCallersInbox(t *testing.T) {
	// Arrange
	repo := newRepo()
	me, someoneElse := uuid.New(), uuid.New()
	seed(repo, me, notification.ChannelTransaksi)
	seed(repo, me, notification.ChannelPromo)
	theirs := seed(repo, someoneElse, notification.ChannelPromo)
	svc := newService(repo)

	// Act
	updated, err := svc.MarkAllRead(context.Background(), me, "")

	// Assert
	require.NoError(t, err)
	assert.Equal(t, int64(2), updated)
	assert.False(t, repo.rows[theirs.ID].item.IsRead)
}

func TestMarkAllReadHonoursTheChannelFilter(t *testing.T) {
	repo := newRepo()
	me := uuid.New()
	transaksi := seed(repo, me, notification.ChannelTransaksi)
	promo := seed(repo, me, notification.ChannelPromo)
	svc := newService(repo)

	updated, err := svc.MarkAllRead(context.Background(), me, "promo")

	require.NoError(t, err)
	assert.Equal(t, int64(1), updated)
	assert.True(t, repo.rows[promo.ID].item.IsRead)
	assert.False(t, repo.rows[transaksi.ID].item.IsRead)
}

func TestMarkAllReadRejectsAnUnknownChannel(t *testing.T) {
	repo := newRepo()
	svc := newService(repo)

	_, err := svc.MarkAllRead(context.Background(), uuid.New(), "INBOX")

	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, status(t, err))
}

func TestListReportsARepositoryFailureAsInternal(t *testing.T) {
	repo := newRepo()
	repo.failWith = errors.New("connection refused")
	svc := newService(repo)

	_, _, err := svc.List(context.Background(), notification.ListQuery{
		UserID: uuid.New(), Page: 1, PerPage: 20,
	})

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, status(t, err))

	// The driver text must not reach the client.
	var appErr *httpx.Error
	require.ErrorAs(t, err, &appErr)
	assert.NotContains(t, appErr.Message, "connection refused")
}
