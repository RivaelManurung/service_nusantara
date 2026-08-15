package order_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/middleware"
	"service_nusantara/internal/modules/order"
)

// passthrough is a middleware that does nothing, so these tests exercise the
// routing table rather than the guards.
func passthrough(next http.Handler) http.Handler { return next }

func register(mux *http.ServeMux) {
	order.Register(
		mux, "/api/v1", order.NewHandler(nil),
		middleware.Middleware(passthrough), middleware.Middleware(passthrough),
		middleware.Middleware(passthrough), middleware.Middleware(passthrough),
	)
}

// TestRegisterDoesNotConflict is a regression test for a panic, not a
// behaviour.
//
// http.ServeMux rejects two patterns that can both match one request unless one
// is strictly more specific. "/order/my/{id}" alongside "/order/{id}/timeline"
// is exactly that case -- /order/my/timeline matches both, and neither wins --
// so the customer routes live under /customer/order instead. Registration is
// the only place that constraint shows up, and it shows up as a panic at
// startup: precisely the failure a test should catch before a deploy does.
func TestRegisterDoesNotConflict(t *testing.T) {
	require.NotPanics(t, func() { register(http.NewServeMux()) })
}

// TestRoutesResolveToTheIntendedHandler pins the patterns that the ServeMux
// specificity rules make non-obvious: a literal segment must beat a wildcard,
// or /order/lifecycle would be parsed as an order id.
func TestRoutesResolveToTheIntendedHandler(t *testing.T) {
	mux := http.NewServeMux()
	register(mux)

	const id = "0f7d1e2a-0000-0000-0000-000000000000"

	cases := []struct {
		method, path string
		wantPattern  string
	}{
		{"GET", "/api/v1/order", "GET /api/v1/order"},
		{"GET", "/api/v1/order/lifecycle", "GET /api/v1/order/lifecycle"},
		{"GET", "/api/v1/order/" + id, "GET /api/v1/order/{id}"},
		{"GET", "/api/v1/order/" + id + "/timeline", "GET /api/v1/order/{id}/timeline"},
		{"PUT", "/api/v1/order/" + id + "/status", "PUT /api/v1/order/{id}/status"},
		{"GET", "/api/v1/customer/order", "GET /api/v1/customer/order"},
		{"GET", "/api/v1/customer/order/" + id, "GET /api/v1/customer/order/{id}"},
		{"GET", "/api/v1/customer/order/" + id + "/timeline", "GET /api/v1/customer/order/{id}/timeline"},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			request, err := http.NewRequest(tc.method, "http://example.test"+tc.path, nil)
			require.NoError(t, err)

			_, pattern := mux.Handler(request)
			assert.Equal(t, tc.wantPattern, pattern)
		})
	}
}
