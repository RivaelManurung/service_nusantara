package role

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// RegisterPermissions mounts the permission endpoints.
//
// All three require superadmin, reads included: the catalogue describes exactly
// which levers exist over every module, and the assignment screen is the one
// place a role can be handed the keys to the rest of the API. It is mounted
// separately from Register so adding it to the server is two new lines rather
// than a change to the existing wiring.
func RegisterPermissions(mux *http.ServeMux, prefix string, h *PermissionHandler, authenticate, rateLimit middleware.Middleware) {
	guard := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	mux.Handle("GET "+prefix+"/permission", guard(h.Catalog))
	mux.Handle("GET "+prefix+"/role/{id}/permission", guard(h.ForRole))
	mux.Handle("PUT "+prefix+"/role/{id}/permission/edit", guard(h.Replace))
}
