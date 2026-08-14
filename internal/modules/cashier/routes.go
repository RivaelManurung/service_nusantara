package cashier

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Roles allowed to manage cashier accounts.
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
)

// Register mounts the module.
//
// Every route is authenticated, and writes additionally require superadmin:
// these endpoints provision accounts, so the role that may create one must be
// narrower than the role that may list them.
//
// There is deliberately no PUT /cashier/{id}/edit-status. The legacy routes
// (nusantara_service/routes/cashier_routes.go) never registered one, and the
// web client sends the status toggle to /edit as a multipart field.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	mux.Handle("GET "+prefix+"/cashier", read(h.List))
	mux.Handle("GET "+prefix+"/cashier/{id}", read(h.Get))

	mux.Handle("POST "+prefix+"/cashier/create", write(h.Create))
	mux.Handle("PUT "+prefix+"/cashier/{id}/edit", write(h.Update))
	mux.Handle("DELETE "+prefix+"/cashier/{id}/delete", write(h.Delete))
}

// ReadRoles lists the roles allowed to read the cashier list, exported so the
// server wiring can document the intent in one place.
var ReadRoles = []string{roleSuperAdmin, roleAdmin}
