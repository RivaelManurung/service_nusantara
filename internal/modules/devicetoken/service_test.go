package devicetoken_test

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
	"service_nusantara/internal/modules/devicetoken"
)

// --- fake --------------------------------------------------------------

// fakeRepo keeps registrations keyed by token, the same way the unique index
// does, so the reassignment rule can be asserted without a database.
type fakeRepo struct {
	rows     map[string]devicetoken.Registration
	seenAt   map[string]time.Time
	failWith error
}

func newRepo() *fakeRepo {
	return &fakeRepo{
		rows:   map[string]devicetoken.Registration{},
		seenAt: map[string]time.Time{},
	}
}

func (f *fakeRepo) Save(_ context.Context, registration devicetoken.Registration, now time.Time) (devicetoken.DeviceToken, error) {
	if f.failWith != nil {
		return devicetoken.DeviceToken{}, f.failWith
	}

	f.rows[registration.Token] = registration
	f.seenAt[registration.Token] = now

	return devicetoken.DeviceToken{
		ID:         uuid.New(),
		Platform:   registration.Platform,
		AppVersion: registration.AppVersion,
		LastSeenAt: now,
	}, nil
}

func (f *fakeRepo) Delete(_ context.Context, userID uuid.UUID, token string) error {
	if f.failWith != nil {
		return f.failWith
	}

	row, ok := f.rows[token]
	if !ok || row.UserID != userID {
		return devicetoken.ErrNotFound
	}
	delete(f.rows, token)
	return nil
}

func (f *fakeRepo) ListForUser(_ context.Context, userID uuid.UUID) ([]devicetoken.DeviceToken, error) {
	if f.failWith != nil {
		return nil, f.failWith
	}

	items := make([]devicetoken.DeviceToken, 0, len(f.rows))
	for token, row := range f.rows {
		if row.UserID != userID {
			continue
		}
		items = append(items, devicetoken.DeviceToken{
			Platform:   row.Platform,
			AppVersion: row.AppVersion,
			LastSeenAt: f.seenAt[token],
		})
	}
	return items, nil
}

func newService(repo devicetoken.Repository) *devicetoken.Service {
	return devicetoken.NewService(repo, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// statusOf reads the HTTP status a service error carries.
func statusOf(t *testing.T, err error) int {
	t.Helper()

	var apiErr *httpx.Error
	require.ErrorAs(t, err, &apiErr)
	return apiErr.Status
}

// --- register ----------------------------------------------------------

func TestRegisterStoresTheRegistrationForTheCaller(t *testing.T) {
	repo := newRepo()
	owner := uuid.New()

	saved, err := newService(repo).Register(context.Background(), devicetoken.Registration{
		UserID:     owner,
		Token:      "fcm-token-a",
		Platform:   "android",
		AppVersion: "1.0.0",
	})

	require.NoError(t, err)
	assert.Equal(t, devicetoken.PlatformAndroid, saved.Platform,
		"the platform is normalised, so a lowercase client still matches")
	assert.Equal(t, owner, repo.rows["fcm-token-a"].UserID)
}

// The app registers on every launch. If that were an error, the second launch
// would show the customer a failure for something entirely normal.
func TestRegisterIsIdempotentForTheSameDevice(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	owner := uuid.New()

	registration := devicetoken.Registration{UserID: owner, Token: "fcm-token-a", Platform: "ANDROID"}

	_, err := service.Register(context.Background(), registration)
	require.NoError(t, err)
	_, err = service.Register(context.Background(), registration)

	require.NoError(t, err)
	assert.Len(t, repo.rows, 1)
}

// One phone, two accounts: the registration must follow whoever signed in
// last, or the previous owner keeps receiving the new owner's notifications.
func TestRegisterMovesADeviceToItsNewOwner(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	first, second := uuid.New(), uuid.New()

	_, err := service.Register(context.Background(), devicetoken.Registration{
		UserID: first, Token: "shared-phone", Platform: "ANDROID",
	})
	require.NoError(t, err)

	_, err = service.Register(context.Background(), devicetoken.Registration{
		UserID: second, Token: "shared-phone", Platform: "ANDROID",
	})
	require.NoError(t, err)

	assert.Equal(t, second, repo.rows["shared-phone"].UserID)
	assert.Len(t, repo.rows, 1)
}

func TestRegisterRejectsInvalidInput(t *testing.T) {
	service := newService(newRepo())
	owner := uuid.New()

	cases := map[string]struct {
		registration devicetoken.Registration
		status       int
	}{
		"unknown platform": {
			registration: devicetoken.Registration{UserID: owner, Token: "fcm-token-a", Platform: "BLACKBERRY"},
			status:       http.StatusBadRequest,
		},
		"blank token": {
			registration: devicetoken.Registration{UserID: owner, Token: "   ", Platform: "ANDROID"},
			status:       http.StatusBadRequest,
		},
		"oversized token": {
			registration: devicetoken.Registration{
				UserID:   owner,
				Token:    strings.Repeat("x", devicetoken.MaxTokenLength+1),
				Platform: "ANDROID",
			},
			status: http.StatusBadRequest,
		},
		"missing owner": {
			registration: devicetoken.Registration{Token: "fcm-token-a", Platform: "ANDROID"},
			status:       http.StatusUnauthorized,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := service.Register(context.Background(), tc.registration)

			require.Error(t, err)
			assert.Equal(t, tc.status, statusOf(t, err))
		})
	}
}

func TestRegisterReportsARepositoryFailureAsInternal(t *testing.T) {
	repo := newRepo()
	repo.failWith = errors.New("connection refused")

	_, err := newService(repo).Register(context.Background(), devicetoken.Registration{
		UserID: uuid.New(), Token: "fcm-token-a", Platform: "ANDROID",
	})

	require.Error(t, err)
	assert.Equal(t, http.StatusInternalServerError, statusOf(t, err))
}

// --- unregister --------------------------------------------------------

func TestUnregisterRemovesTheCallersDevice(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	owner := uuid.New()

	_, err := service.Register(context.Background(), devicetoken.Registration{
		UserID: owner, Token: "fcm-token-a", Platform: "ANDROID",
	})
	require.NoError(t, err)

	require.NoError(t, service.Unregister(context.Background(), owner, "fcm-token-a"))
	assert.Empty(t, repo.rows)
}

// Sign-out must not fail because the registration was already gone: the
// desired state already holds, and a failure here would block the sign-out.
func TestUnregisterSucceedsWhenTheRegistrationIsAlreadyGone(t *testing.T) {
	service := newService(newRepo())

	err := service.Unregister(context.Background(), uuid.New(), "never-registered")

	assert.NoError(t, err)
}

// Unregistering somebody else's device is indistinguishable from unregistering
// one that does not exist -- and, crucially, does not remove it.
func TestUnregisterLeavesAnotherAccountsDeviceAlone(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	owner, stranger := uuid.New(), uuid.New()

	_, err := service.Register(context.Background(), devicetoken.Registration{
		UserID: owner, Token: "fcm-token-a", Platform: "ANDROID",
	})
	require.NoError(t, err)

	require.NoError(t, service.Unregister(context.Background(), stranger, "fcm-token-a"))
	assert.Len(t, repo.rows, 1, "another account's registration must survive")
}

// --- list --------------------------------------------------------------

func TestListReturnsOnlyTheCallersDevices(t *testing.T) {
	repo := newRepo()
	service := newService(repo)
	owner, stranger := uuid.New(), uuid.New()

	for _, registration := range []devicetoken.Registration{
		{UserID: owner, Token: "phone", Platform: "ANDROID"},
		{UserID: owner, Token: "tablet", Platform: "IOS"},
		{UserID: stranger, Token: "other-phone", Platform: "ANDROID"},
	} {
		_, err := service.Register(context.Background(), registration)
		require.NoError(t, err)
	}

	items, err := service.List(context.Background(), owner)

	require.NoError(t, err)
	assert.Len(t, items, 2)
}
