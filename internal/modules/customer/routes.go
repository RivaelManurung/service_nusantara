package customer

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Register mounts the module.
//
// Authorisation is by permission, not by role: role/catalog.go has carried
// user.read and user.write since the permission screen was built, and until now
// nothing consulted them.
//
// Note "GET /user/roles" alongside "GET /user/{id}": Go's ServeMux prefers the
// literal segment, so /user/roles reaches Roles and is never parsed as an id.
// That only holds because the literal is registered -- see
// internal/modules/order/routes_test.go for what happens when two patterns are
// equally specific.
func Register(
	mux *http.ServeMux,
	prefix string,
	h *Handler,
	authenticate, rateLimit middleware.Middleware,
	requireRead, requireWrite middleware.Middleware,
) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireRead(handler)))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireWrite(handler)))
	}

	mux.Handle("GET "+prefix+"/user", read(h.List))
	mux.Handle("GET "+prefix+"/user/roles", read(h.Roles))
	mux.Handle("GET "+prefix+"/user/{id}", read(h.Get))

	// Blocking is the only write. There is deliberately no create, no edit and
	// no delete: accounts are created by people signing up, their details are
	// theirs to change through /auth, and deleting one would orphan its orders
	// and break every report that counts them. Blocking is the reversible,
	// auditable alternative.
	mux.Handle("PUT "+prefix+"/user/{id}/status", write(h.SetStatus))
}
