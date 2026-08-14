package middleware_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/middleware"
)

// stubResolver answers with a fixed set and counts how often it was asked, so
// the per-request cache can be observed rather than assumed.
type stubResolver struct {
	sets  map[string][]string
	err   error
	calls int
}

func (s *stubResolver) PermissionsFor(_ context.Context, roleName string) (map[string]struct{}, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	set := map[string]struct{}{}
	for _, code := range s.sets[roleName] {
		set[code] = struct{}{}
	}
	return set, nil
}

// authenticated wraps a handler in a fake Authenticate: it stores an identity
// and nothing else, so these tests exercise RequirePermission alone.
func authenticated(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithIdentity(r.Context(), auth.Identity{
			UserID: "user-1", Role: role, TokenID: "token-1",
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequest() (*http.Request, *httptest.ResponseRecorder) {
	return httptest.NewRequest(http.MethodGet, "/api/v1/product", nil), httptest.NewRecorder()
}

func TestRequirePermissionAllowsARoleThatHoldsTheCode(t *testing.T) {
	// Arrange
	resolver := &stubResolver{sets: map[string][]string{"admin": {"product.read"}}}
	var reached bool
	var identity auth.Identity

	handler := authenticated("admin",
		middleware.RequirePermission(resolver, "product.read")(
			protectedHandler(&reached, &identity)))

	req, rec := newRequest()

	// Act
	handler.ServeHTTP(rec, req)

	// Assert
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, reached)
}

func TestRequirePermissionRefusesARoleWithoutTheCode(t *testing.T) {
	resolver := &stubResolver{sets: map[string][]string{"admin": {"product.read"}}}
	var reached bool
	var identity auth.Identity

	handler := authenticated("admin",
		middleware.RequirePermission(resolver, "product.write")(
			protectedHandler(&reached, &identity)))

	req, rec := newRequest()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, reached)
}

func TestRequirePermissionNeedsEveryListedCode(t *testing.T) {
	// Holding one of two is not enough: a route asking for both wants both.
	resolver := &stubResolver{sets: map[string][]string{"admin": {"product.read"}}}
	var reached bool
	var identity auth.Identity

	handler := authenticated("admin",
		middleware.RequirePermission(resolver, "product.read", "product.write")(
			protectedHandler(&reached, &identity)))

	req, rec := newRequest()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.False(t, reached)
}

func TestRequirePermissionRejectsAnUnauthenticatedRequest(t *testing.T) {
	// No Authenticate in front: a missing identity must be a 401, never an
	// implicit allow.
	resolver := &stubResolver{sets: map[string][]string{}}
	var reached bool
	var identity auth.Identity

	handler := middleware.RequirePermission(resolver, "product.read")(
		protectedHandler(&reached, &identity))

	req, rec := newRequest()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.False(t, reached)
	assert.Zero(t, resolver.calls, "an anonymous caller must not reach the store")
}

func TestRequirePermissionFailsClosedWhenTheStoreIsUnreachable(t *testing.T) {
	resolver := &stubResolver{err: errors.New("connection refused")}
	var reached bool
	var identity auth.Identity

	handler := authenticated("admin",
		middleware.RequirePermission(resolver, "product.read")(
			protectedHandler(&reached, &identity)))

	req, rec := newRequest()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.False(t, reached)
	assert.NotContains(t, rec.Body.String(), "connection refused")
}

func TestRequirePermissionResolvesOncePerRequestNotPerGuard(t *testing.T) {
	// Arrange: three guards stacked on one route.
	resolver := &stubResolver{
		sets: map[string][]string{"admin": {"product.read", "product.write"}},
	}
	var reached bool
	var identity auth.Identity

	handler := authenticated("admin",
		middleware.RequirePermission(resolver, "product.read")(
			middleware.RequirePermission(resolver, "product.write")(
				middleware.RequirePermission(resolver, "product.read")(
					protectedHandler(&reached, &identity)))))

	req, rec := newRequest()

	// Act
	handler.ServeHTTP(rec, req)

	// Assert
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, reached)
	assert.Equal(t, 1, resolver.calls, "the set must be cached on the request")
}

func TestRequirePermissionCachesAnEmptySetToo(t *testing.T) {
	// A role with no grants is a legitimate answer; re-querying for it on every
	// guard would make the unauthorised path the expensive one.
	resolver := &stubResolver{sets: map[string][]string{}}
	var reached bool
	var identity auth.Identity

	handler := authenticated("cashier",
		middleware.RequirePermission(resolver, "product.read")(
			protectedHandler(&reached, &identity)))

	req, rec := newRequest()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, 1, resolver.calls)
}

func TestPermissionsFromExposesTheResolvedSetToTheHandler(t *testing.T) {
	resolver := &stubResolver{
		sets: map[string][]string{"admin": {"product.read", "order.read"}},
	}

	var got map[string]struct{}
	handler := authenticated("admin",
		middleware.RequirePermission(resolver, "product.read")(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				set, ok := middleware.PermissionsFrom(r.Context())
				require.True(t, ok)
				got = set
				w.WriteHeader(http.StatusOK)
			})))

	req, rec := newRequest()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Len(t, got, 2)
	assert.Contains(t, got, "order.read")
}
