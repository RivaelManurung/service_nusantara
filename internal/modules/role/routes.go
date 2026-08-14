package role

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Roles allowed to manage the account roles themselves.
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
)

// Register mounts the module.
//
// Every route is authenticated and writes require superadmin. The previous
// service mounted /role with no JWT middleware at all, so anyone on the
// internet could create or delete the roles that authorise every other
// endpoint; that is not reproduced here.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	mux.Handle("GET "+prefix+"/role", read(h.List))
	mux.Handle("GET "+prefix+"/role/{id}", read(h.Get))

	mux.Handle("POST "+prefix+"/role/create", write(h.Create))
	mux.Handle("PUT "+prefix+"/role/{id}/edit", write(h.Update))
	mux.Handle("DELETE "+prefix+"/role/{id}/delete", write(h.Delete))
}

// ReadRoles lists the roles allowed to read the role list, exported so the
// server wiring can document the intent in one place.
var ReadRoles = []string{roleSuperAdmin, roleAdmin}
