package banner

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Roles allowed to manage banners.
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
)

// Register mounts the module.
//
// The back-office routes are authenticated and writes additionally require
// superadmin, matching the legacy handler which resolved the acting user
// through FindByUserIDSuperAdmin before creating anything.
//
// The two /customer routes are public: the mobile app reads the storefront
// carousel before anyone signs in (see mobile_nusantara core/network
// api_endpoints.dart). They are still rate limited, and they only ever expose
// active banners.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	public := func(handler httpx.Handler) http.Handler {
		return rateLimit(handler)
	}
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	// Registered before the {id} patterns for readability; ServeMux prefers the
	// more specific literal segment regardless of registration order.
	mux.Handle("GET "+prefix+"/banner/customer", public(h.ListPublic))
	mux.Handle("GET "+prefix+"/banner/{id}/customer", public(h.GetPublic))

	mux.Handle("GET "+prefix+"/banner", read(h.List))
	mux.Handle("GET "+prefix+"/banner/{id}", read(h.Get))

	mux.Handle("POST "+prefix+"/banner/create", write(h.Create))
	mux.Handle("PUT "+prefix+"/banner/{id}/edit", write(h.Update))
	mux.Handle("PUT "+prefix+"/banner/{id}/edit-status", write(h.SetStatus))
	mux.Handle("DELETE "+prefix+"/banner/{id}/delete", write(h.Delete))
}

// ReadRoles lists the roles allowed to read the back-office banner list,
// exported so the server wiring can document the intent in one place.
var ReadRoles = []string{roleSuperAdmin, roleAdmin}
