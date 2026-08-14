package event

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Roles allowed to manage promotions.
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
)

// Register mounts the module.
//
// Every route is authenticated, and writes additionally require superadmin --
// matching what the web client's route guard already assumes, so the two cannot
// disagree about who may edit a promotion.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	mux.Handle("GET "+prefix+"/event", read(h.List))
	mux.Handle("GET "+prefix+"/event/{id}", read(h.Get))

	mux.Handle("POST "+prefix+"/event/create", write(h.Create))
	mux.Handle("PUT "+prefix+"/event/{id}/edit", write(h.Update))
	mux.Handle("PUT "+prefix+"/event/{id}/edit-status", write(h.SetStatus))
	mux.Handle("DELETE "+prefix+"/event/{id}/delete", write(h.Delete))
}

// ReadRoles lists the roles allowed to read promotions, exported so the server
// wiring can document the intent in one place.
var ReadRoles = []string{roleSuperAdmin, roleAdmin}
