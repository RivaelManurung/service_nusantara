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
	"service_nusantara/internal/platform/push"
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

	// The broadcast half: what the audience resolves to, what was written, and
	// what the service asked for.
	recipients   []uuid.UUID
	created      []notification.NewNotification
	createErr    error
	lastAudience notification.Audience

	customers         []notification.Customer
	lastCustomerQuery notification.CustomerQuery

	// The send history. recordErr is separate from failWith so a test can fail
	// the bookkeeping write while the send itself succeeds.
	broadcasts []notification.Broadcast
	recordErr  error
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

// Recipients answers with whatever the test staged, and records the audience
// it was asked for so the selection rules can be asserted.
func (f *fakeRepo) Recipients(_ context.Context, audience notification.Audience) ([]uuid.UUID, error) {
	f.lastAudience = audience
	if f.failWith != nil {
		return nil, f.failWith
	}
	return f.recipients, nil
}

func (f *fakeRepo) CreateMany(_ context.Context, messages []notification.NewNotification) (int64, error) {
	if f.createErr != nil {
		return 0, f.createErr
	}
	f.created = append(f.created, messages...)
	return int64(len(messages)), nil
}

// --- helpers -----------------------------------------------------------

func newService(repo *fakeRepo) *notification.Service {
	return notification.NewService(notification.Deps{
		Repo:   repo,
		Logger: discardLogger(),
	})
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
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

// --- broadcast ---------------------------------------------------------

// fakeRegistry stands in for the device_tokens table.
type fakeRegistry struct {
	devices []notification.Device
	loadErr error
	// deleted records what the service pruned after FCM reported it gone.
	deleted []string
}

func (f *fakeRegistry) TokensFor(_ context.Context, userIDs []uuid.UUID) ([]notification.Device, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}

	wanted := make(map[uuid.UUID]struct{}, len(userIDs))
	for _, id := range userIDs {
		wanted[id] = struct{}{}
	}

	matched := make([]notification.Device, 0, len(f.devices))
	for _, device := range f.devices {
		if _, ok := wanted[device.UserID]; ok {
			matched = append(matched, device)
		}
	}
	return matched, nil
}

func (f *fakeRegistry) DeleteTokens(_ context.Context, tokens []string) error {
	f.deleted = append(f.deleted, tokens...)
	return nil
}

// fakeSender stands in for FCM.
type fakeSender struct {
	enabled bool
	sent    []push.Message
	report  push.Report
	sendErr error
	invalid []string
}

func (f *fakeSender) Enabled() bool { return f.enabled }

func (f *fakeSender) Send(_ context.Context, messages []push.Message) (push.Report, error) {
	f.sent = append(f.sent, messages...)

	report := f.report
	if report.Success == 0 && report.Failure == 0 && f.sendErr == nil {
		report.Success = len(messages)
	}
	report.InvalidTokens = f.invalid

	return report, f.sendErr
}

// broadcaster wires a service with a stubbed registry and sender.
func broadcaster(repo *fakeRepo, registry *fakeRegistry, sender *fakeSender) *notification.Service {
	return notification.NewService(notification.Deps{
		Repo:    repo,
		Logger:  discardLogger(),
		Devices: registry,
		Push:    sender,
	})
}

func promo() notification.SendRequest {
	return notification.SendRequest{
		Audience: notification.Audience{Mode: notification.AudienceAll},
		Title:    "Promo Merdeka",
		Body:     "Diskon 50% untuk semua oleh-oleh",
		Push:     true,
	}
}

func TestSendWritesOneInboxRowPerRecipient(t *testing.T) {
	repo := newRepo()
	first, second := uuid.New(), uuid.New()
	repo.recipients = []uuid.UUID{first, second}

	registry := &fakeRegistry{}
	service := broadcaster(repo, registry, &fakeSender{enabled: true})

	result, err := service.Send(context.Background(), promo())

	require.NoError(t, err)
	assert.Equal(t, 2, result.Recipients)
	assert.Equal(t, int64(2), result.Saved)
	require.Len(t, repo.created, 2)

	// A back-office broadcast lands on the promo tab unless it says otherwise;
	// the transactional tab belongs to the order flow.
	assert.Equal(t, notification.ChannelPromo, repo.created[0].Channel)
	assert.Equal(t, "PROMO", repo.created[0].Type)
	assert.Equal(t, first, repo.created[0].UserID)
	assert.Equal(t, second, repo.created[1].UserID)
}

func TestSendWakesEveryDeviceOfTheRecipientsWithTheDeepLink(t *testing.T) {
	repo := newRepo()
	owner := uuid.New()
	repo.recipients = []uuid.UUID{owner}

	registry := &fakeRegistry{devices: []notification.Device{
		{UserID: owner, Token: "phone"},
		{UserID: owner, Token: "tablet"},
		{UserID: uuid.New(), Token: "stranger-phone"},
	}}
	sender := &fakeSender{enabled: true}

	request := promo()
	request.TargetType = "VOUCHER"
	request.TargetRoute = "/rewards"

	result, err := broadcaster(repo, registry, sender).Send(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, 2, result.Devices)
	assert.Equal(t, 2, result.PushSent)
	require.Len(t, sender.sent, 2, "a device belonging to somebody outside the audience must not be woken")
	assert.Equal(t, "Promo Merdeka", sender.sent[0].Title)
	assert.Equal(t, "VOUCHER", sender.sent[0].Data["target_type"])
	assert.Equal(t, "/rewards", sender.sent[0].Data["target_route"])
}

// Saving without pushing is a real choice: the promo is waiting when the
// customer next opens the app, without a tray notification at 11pm.
func TestSendWithoutPushSavesTheInboxOnly(t *testing.T) {
	repo := newRepo()
	owner := uuid.New()
	repo.recipients = []uuid.UUID{owner}

	registry := &fakeRegistry{devices: []notification.Device{{UserID: owner, Token: "phone"}}}
	sender := &fakeSender{enabled: true}

	request := promo()
	request.Push = false

	result, err := broadcaster(repo, registry, sender).Send(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, int64(1), result.Saved)
	assert.Empty(t, sender.sent)
	assert.Zero(t, result.PushSent)
}

// A deployment without FCM credentials still records the promo, and says push
// was off rather than claiming a delivery that never happened.
func TestSendReportsThatPushIsDisabled(t *testing.T) {
	repo := newRepo()
	repo.recipients = []uuid.UUID{uuid.New()}

	service := notification.NewService(notification.Deps{Repo: repo, Logger: discardLogger()})

	result, err := service.Send(context.Background(), promo())

	require.NoError(t, err)
	assert.Equal(t, int64(1), result.Saved)
	assert.False(t, result.PushEnabled)
	assert.Zero(t, result.PushSent)
}

// The inbox rows are already committed when the push fails. Failing the
// request now would tell the operator nothing was sent, and the retry would
// notify everybody twice.
func TestSendSurvivesAPushFailure(t *testing.T) {
	repo := newRepo()
	owner := uuid.New()
	repo.recipients = []uuid.UUID{owner}

	registry := &fakeRegistry{devices: []notification.Device{{UserID: owner, Token: "phone"}}}
	sender := &fakeSender{
		enabled: true,
		sendErr: errors.New("firebase rejected the credentials"),
		report:  push.Report{Failure: 1},
	}

	result, err := broadcaster(repo, registry, sender).Send(context.Background(), promo())

	require.NoError(t, err)
	assert.Equal(t, int64(1), result.Saved)
	assert.Equal(t, 1, result.PushFailed)
	assert.NotEmpty(t, result.PushError)
	assert.NotContains(t, result.PushError, "credentials", "the operator gets a summary, not the provider's wording")
}

// Dead registrations are dropped, or every future broadcast pays for them.
func TestSendPrunesRegistrationsFirebaseReportsAsGone(t *testing.T) {
	repo := newRepo()
	owner := uuid.New()
	repo.recipients = []uuid.UUID{owner}

	registry := &fakeRegistry{devices: []notification.Device{
		{UserID: owner, Token: "live-phone"},
		{UserID: owner, Token: "uninstalled-phone"},
	}}
	sender := &fakeSender{
		enabled: true,
		report:  push.Report{Success: 1, Failure: 1},
		invalid: []string{"uninstalled-phone"},
	}

	_, err := broadcaster(repo, registry, sender).Send(context.Background(), promo())

	require.NoError(t, err)
	assert.Equal(t, []string{"uninstalled-phone"}, registry.deleted)
}

// An operator who mistyped a segment must not be told the promo went out.
func TestSendRefusesAnAudienceThatMatchesNobody(t *testing.T) {
	repo := newRepo()
	repo.recipients = nil

	_, err := broadcaster(repo, &fakeRegistry{}, &fakeSender{enabled: true}).
		Send(context.Background(), promo())

	require.Error(t, err)
	assert.Equal(t, http.StatusBadRequest, status(t, err))
}

func TestSendRejectsAnInvalidRequest(t *testing.T) {
	owner := uuid.New()

	cases := map[string]func(notification.SendRequest) notification.SendRequest{
		"missing audience mode": func(r notification.SendRequest) notification.SendRequest {
			r.Audience.Mode = ""
			return r
		},
		"unknown audience mode": func(r notification.SendRequest) notification.SendRequest {
			r.Audience.Mode = "EVERYONE"
			return r
		},
		"USERS without ids": func(r notification.SendRequest) notification.SendRequest {
			r.Audience = notification.Audience{Mode: notification.AudienceUsers}
			return r
		},
		"segment with no filter": func(r notification.SendRequest) notification.SendRequest {
			r.Audience = notification.Audience{Mode: notification.AudienceSegment}
			return r
		},
		"blank title": func(r notification.SendRequest) notification.SendRequest {
			r.Title = "   "
			return r
		},
		"blank body": func(r notification.SendRequest) notification.SendRequest {
			r.Body = ""
			return r
		},
		"unknown type": func(r notification.SendRequest) notification.SendRequest {
			r.Type = "SHOUT"
			return r
		},
		"unknown deep-link target": func(r notification.SendRequest) notification.SendRequest {
			r.TargetType = "SPACESHIP"
			return r
		},
	}

	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			repo := newRepo()
			repo.recipients = []uuid.UUID{owner}

			_, err := broadcaster(repo, &fakeRegistry{}, &fakeSender{enabled: true}).
				Send(context.Background(), mutate(promo()))

			require.Error(t, err)
			assert.Equal(t, http.StatusBadRequest, status(t, err))
			assert.Empty(t, repo.created, "nothing may be written when the request is refused")
		})
	}
}

// The same customer picked twice in the back office is one promo, not two.
func TestSendDeduplicatesAHandPickedAudience(t *testing.T) {
	repo := newRepo()
	picked := uuid.New()
	repo.recipients = []uuid.UUID{picked}

	request := promo()
	request.Audience = notification.Audience{
		Mode:    notification.AudienceUsers,
		UserIDs: []uuid.UUID{picked, picked},
	}

	_, err := broadcaster(repo, &fakeRegistry{}, &fakeSender{enabled: true}).
		Send(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{picked}, repo.lastAudience.UserIDs)
}

// A segment must not quietly widen to the whole customer base.
func TestSendCarriesTheSegmentDownToTheRepository(t *testing.T) {
	repo := newRepo()
	repo.recipients = []uuid.UUID{uuid.New()}

	request := promo()
	request.Audience = notification.Audience{
		Mode:    notification.AudienceSegment,
		Segment: notification.Segment{RoleName: "customer", HasOrdered: true},
	}

	_, err := broadcaster(repo, &fakeRegistry{}, &fakeSender{enabled: true}).
		Send(context.Background(), request)

	require.NoError(t, err)
	assert.Equal(t, notification.AudienceSegment, repo.lastAudience.Mode)
	assert.Equal(t, "customer", repo.lastAudience.Segment.RoleName)
	assert.True(t, repo.lastAudience.Segment.HasOrdered)
}

func TestSendReportsAWriteFailureWithoutPushing(t *testing.T) {
	repo := newRepo()
	repo.recipients = []uuid.UUID{uuid.New()}
	repo.createErr = errors.New("deadlock detected")

	sender := &fakeSender{enabled: true}

	_, err := broadcaster(repo, &fakeRegistry{}, sender).Send(context.Background(), promo())

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, status(t, err))
	assert.Empty(t, sender.sent, "nobody may be woken for a promo that was never saved")
}

func (f *fakeRepo) SearchCustomers(_ context.Context, query notification.CustomerQuery) ([]notification.Customer, int64, error) {
	f.lastCustomerQuery = query
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	return f.customers, int64(len(f.customers)), nil
}

// RecordBroadcast and ListBroadcasts back the back-office send history.
//
// recordErr is separate from failWith on purpose: a test needs to make the
// history write fail while the send itself succeeds, which is the whole point
// of recording it outside the delivery path.
func (f *fakeRepo) RecordBroadcast(_ context.Context, broadcast notification.Broadcast) (notification.Broadcast, error) {
	if f.recordErr != nil {
		return notification.Broadcast{}, f.recordErr
	}
	broadcast.ID = uuid.New()
	f.broadcasts = append(f.broadcasts, broadcast)
	return broadcast, nil
}

func (f *fakeRepo) ListBroadcasts(_ context.Context, _, _ int) ([]notification.Broadcast, int64, error) {
	if f.failWith != nil {
		return nil, 0, f.failWith
	}
	return f.broadcasts, int64(len(f.broadcasts)), nil
}

func TestSendRecordsTheBroadcastInTheHistory(t *testing.T) {
	repo := newRepo()
	repo.recipients = []uuid.UUID{uuid.New(), uuid.New()}
	actor := uuid.New()

	_, err := newService(repo).Send(context.Background(), notification.SendRequest{
		Audience: notification.Audience{Mode: "ALL"},
		Channel:  "PROMO",
		Title:    "Promo Merdeka 17 Agustus",
		Body:     "Diskon 50% untuk semua oleh-oleh.",
		ActorID:  actor,
	})

	require.NoError(t, err)
	require.Len(t, repo.broadcasts, 1,
		"the screen lists sends, and it cannot list what was never recorded")

	recorded := repo.broadcasts[0]
	assert.Equal(t, "Promo Merdeka 17 Agustus", recorded.Title)
	assert.Equal(t, "ALL", recorded.AudienceMode)
	assert.Equal(t, 2, recorded.RecipientCount)
	assert.Equal(t, actor, recorded.ActorID,
		"the actor comes from the token, so the history is attributable")
}

func TestSendStillSucceedsWhenTheHistoryWriteFails(t *testing.T) {
	repo := newRepo()
	repo.recipients = []uuid.UUID{uuid.New()}
	repo.recordErr = errors.New("insert notification_broadcasts: connection reset")

	result, err := newService(repo).Send(context.Background(), notification.SendRequest{
		Audience: notification.Audience{Mode: "ALL"},
		Channel:  "PROMO",
		Title:    "Promo",
		Body:     "Diskon hari ini.",
		ActorID:  uuid.New(),
	})

	require.NoError(t, err,
		"the messages are the product; failing to file the bookkeeping must not "+
			"undo a send that already reached the inboxes")
	assert.Equal(t, 1, result.Recipients)
}

func TestSearchCustomersTrimsTheSearchBeforeItReachesTheQuery(t *testing.T) {
	repo := newRepo()
	repo.customers = []notification.Customer{{ID: uuid.New(), Name: "Budi"}}

	items, total, err := newService(repo).SearchCustomers(context.Background(), notification.CustomerQuery{
		Search: "  budi  ", Page: 1, PerPage: 20,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	assert.Len(t, items, 1)
	assert.Equal(t, "budi", repo.lastCustomerQuery.Search)
}
