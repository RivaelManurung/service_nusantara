package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/middleware"
)

// recordingLimiter captures the key the limiter would have used, which is the
// only way to observe that authentication ran first.
type recordingLimiter struct {
	keys []string
}

func (l *recordingLimiter) Allow(_ context.Context, key string) (bool, time.Duration, error) {
	l.keys = append(l.keys, key)
	return true, 0, nil
}

func TestLimitKeysByUserOnceAuthenticationHasRun(t *testing.T) {
	// Regression test: the limiter used to be mounted in the outer server
	// chain, so it ran before per-route authentication and its per-user branch
	// was unreachable. Every caller behind one NAT then shared a single budget.
	limiter := &recordingLimiter{}
	manager := testManager()

	pair, _, err := manager.Issue("user-1", "superadmin")
	require.NoError(t, err)

	var reached bool
	var identity auth.Identity
	// The wiring under test: authenticate outer, rate limit inner.
	handler := middleware.Authenticate(manager, stubRevocations{})(
		middleware.LimitWith(limiter, false)(protectedHandler(&reached, &identity)))

	req := httptest.NewRequest(http.MethodGet, "/auth/profile", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.True(t, reached)
	require.Len(t, limiter.keys, 1)
	assert.Equal(t, "user:user-1", limiter.keys[0])
}

func TestLimitFallsBackToTheClientIPWhenUnauthenticated(t *testing.T) {
	limiter := &recordingLimiter{}

	handler := middleware.LimitWith(limiter, false)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	handler.ServeHTTP(httptest.NewRecorder(), req)

	require.Len(t, limiter.keys, 1)
	assert.Equal(t, "ip:203.0.113.7", limiter.keys[0])
}

func TestLimitIgnoresForgedProxyHeadersWhenProxiesAreNotTrusted(t *testing.T) {
	// Trusting X-Forwarded-For unconditionally would let any client pick its
	// own rate-limit bucket.
	limiter := &recordingLimiter{}

	handler := middleware.LimitWith(limiter, false)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	req := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("X-Forwarded-For", "198.51.100.1")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	assert.Equal(t, "ip:203.0.113.7", limiter.keys[0])
}
