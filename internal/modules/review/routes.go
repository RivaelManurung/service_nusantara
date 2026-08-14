package review

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// roleSuperAdmin is the only role allowed to moderate.
const roleSuperAdmin = "superadmin"

// Register mounts the module.
//
// Reads are open to any authenticated caller; moderation requires superadmin,
// matching what the web client's route guard already assumes for
// /customer-reviews, so the two cannot disagree about who may take a review
// down.
//
// There is deliberately no POST /review/create here: writing a review belongs
// to the customer app, and an admin-only module has no business authoring one.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}

	mux.Handle("GET "+prefix+"/review", read(h.List))
	mux.Handle("GET "+prefix+"/review/{id}", read(h.Get))

	mux.Handle("PUT "+prefix+"/review/{id}/edit-status", write(h.SetStatus))
	mux.Handle("DELETE "+prefix+"/review/{id}/delete", write(h.Delete))
}
