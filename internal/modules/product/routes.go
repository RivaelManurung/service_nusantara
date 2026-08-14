package product

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Roles allowed to manage the catalogue's products.
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
)

// Register mounts the module.
//
// Every route is authenticated, and writes additionally require superadmin --
// matching internal/modules/typeproduct and the web client's route guard, so
// the catalogue cannot be edited by someone the UI would not let in.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	mux.Handle("GET "+prefix+"/product", read(h.List))
	mux.Handle("GET "+prefix+"/product/{id}", read(h.Get))

	mux.Handle("POST "+prefix+"/product/create", write(h.Create))
	mux.Handle("PUT "+prefix+"/product/{id}/edit", write(h.Update))
	mux.Handle("PUT "+prefix+"/product/{id}/edit-status", write(h.SetStatus))
	mux.Handle("DELETE "+prefix+"/product/{id}/delete", write(h.Delete))
}

// ReadRoles lists the roles allowed to read the catalogue, exported so the
// server wiring can document the intent in one place.
var ReadRoles = []string{roleSuperAdmin, roleAdmin}
