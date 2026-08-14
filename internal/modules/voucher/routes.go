package voucher

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Roles allowed to manage vouchers.
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
)

// Register mounts the module.
//
// Every route is authenticated, and writes additionally require superadmin --
// matching what the web client's route guard already assumes, so the two cannot
// disagree about who may mint a discount code.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	mux.Handle("GET "+prefix+"/voucher", read(h.List))
	mux.Handle("GET "+prefix+"/voucher/{id}", read(h.Get))

	mux.Handle("POST "+prefix+"/voucher/create", write(h.Create))
	mux.Handle("PUT "+prefix+"/voucher/{id}/edit", write(h.Update))
	mux.Handle("PUT "+prefix+"/voucher/{id}/edit-status", write(h.SetStatus))
	mux.Handle("DELETE "+prefix+"/voucher/{id}/delete", write(h.Delete))
}

// ReadRoles lists the roles allowed to read vouchers, exported so the server
// wiring can document the intent in one place.
var ReadRoles = []string{roleSuperAdmin, roleAdmin}
