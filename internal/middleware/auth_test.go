package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/config"
	"service_nusantara/internal/middleware"
)

// stubRevocations lets each test decide what the revocation store reports,
// including an outage.
type stubRevocations struct {
	revoked map[string]bool
	err     error
}

func (s stubRevocations) IsRevoked(_ context.Context, tokenID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return s.revoked[tokenID], nil
}

func testManager() *auth.Manager {
	return auth.NewManager(config.Auth{
		JWTSecret:       "test-secret-that-is-at-least-32-bytes-long",
		Issuer:          "nusantara-test",
		AccessTokenTTL:  time.Minute,
		RefreshTokenTTL: time.Hour,
	})
}

// reachedHandler records whether the protected handler actually ran, which is
// the only way to catch a middleware that lets a request through silently.
func protectedHandler(reached *bool, identity *auth.Identity) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		if id, ok := auth.IdentityFrom(r.Context()); ok {
			*identity = id
		}
		w.WriteHeader(http.StatusOK)
	})
}

func TestAuthenticateAllowsAValidToken(t *testing.T) {
	manager := testManager()
	pair, tokenID, err := manager.Issue("user-1", "superadmin")
	require.NoError(t, err)

	var reached bool
	var identity auth.Identity
	handler := middleware.Authenticate(manager, stubRevocations{})(protectedHandler(&reached, &identity))

	req := httptest.NewRequest(http.MethodGet, "/auth/profile", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, reached)
	assert.Equal(t, "user-1", identity.UserID)
	assert.Equal(t, tokenID, identity.TokenID)
}

func TestAuthenticateRejectsARevokedTokenInsteadOfSkippingAuth(t *testing.T) {
	// Regression test for the previous service: its echo-jwt Skipper returned
	// true for blacklisted tokens, and a Skipper returning true means "do not
	// run this middleware". A revoked token therefore skipped authentication
	// entirely and reached the handler with no identity at all.
	manager := testManager()
	pair, tokenID, err := manager.Issue("user-1", "superadmin")
	require.NoError(t, err)

	var reached bool
	var identity auth.Identity
	handler := middleware.Authenticate(manager,
		stubRevocations{revoked: map[string]bool{tokenID: true}},
	)(protectedHandler(&reached, &identity))

	req := httptest.NewRequest(http.MethodGet, "/auth/profile", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, reached, "a revoked token must never reach the handler")
}

func TestAuthenticateFailsClosedWhenTheRevocationStoreIsDown(t *testing.T) {
	// Treating "cannot check" as "not revoked" would re-open the bypass above
	// during any Redis incident.
	manager := testManager()
	pair, _, err := manager.Issue("user-1", "superadmin")
	require.NoError(t, err)

	var reached bool
	var identity auth.Identity
	handler := middleware.Authenticate(manager,
		stubRevocations{err: errors.New("redis: connection refused")},
	)(protectedHandler(&reached, &identity))

	req := httptest.NewRequest(http.MethodGet, "/auth/profile", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, reached)
}

func TestAuthenticateRejectsMalformedHeaders(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{"missing header", ""},
		{"no bearer scheme", "abcdef"},
		{"wrong scheme", "Basic abcdef"},
		{"empty token", "Bearer "},
		{"garbage token", "Bearer not-a-jwt"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var reached bool
			var identity auth.Identity
			handler := middleware.Authenticate(testManager(), stubRevocations{})(
				protectedHandler(&reached, &identity))

			req := httptest.NewRequest(http.MethodGet, "/auth/profile", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusUnauthorized, rec.Code)
			assert.False(t, reached)
		})
	}
}

func TestRequireRoleAllowsAListedRole(t *testing.T) {
	var reached bool
	handler := middleware.RequireRole("superadmin")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { reached = true }))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(),
		auth.Identity{UserID: "user-1", Role: "superadmin"}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, reached)
}

func TestRequireRoleForbidsAnUnlistedRole(t *testing.T) {
	var reached bool
	handler := middleware.RequireRole("superadmin")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { reached = true }))

	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(auth.WithIdentity(req.Context(),
		auth.Identity{UserID: "user-2", Role: "cashier"}))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, reached)
}

func TestRequireRoleRejectsAnUnauthenticatedRequest(t *testing.T) {
	// Without an identity the check must deny, never default to allow.
	var reached bool
	handler := middleware.RequireRole("superadmin")(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) { reached = true }))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin", nil))

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, reached)
}
