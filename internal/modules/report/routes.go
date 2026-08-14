package report

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// roleSuperAdmin is the only role allowed to read the reports.
//
// Both screens are already declared as `["superadmin"]` in the web client's
// src/config/routes.ts, and the guard there only hides the link -- the data is
// only actually protected here.
const roleSuperAdmin = "superadmin"

// Register mounts the module.
//
// There are no writes: a report derives from orders and never changes them.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	// The summary route is registered before the collection route it shares a
	// prefix with; net/http's mux picks the more specific pattern regardless,
	// but keeping them adjacent makes the pair obvious.
	mux.Handle("GET "+prefix+"/report/transactions", read(h.Transactions))
	mux.Handle("GET "+prefix+"/report/transactions/summary", read(h.Summary))

	mux.Handle("GET "+prefix+"/report/financial", read(h.Financial))
	mux.Handle("GET "+prefix+"/report/financial/top-products", read(h.TopProducts))
}

// ReadRoles lists the roles allowed to read the reports, exported so the server
// wiring can document the intent in one place.
var ReadRoles = []string{roleSuperAdmin}
