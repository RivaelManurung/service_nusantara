package notification

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Register mounts the module.
//
// Every route is authenticated and none is role restricted: the inbox belongs
// to whoever is holding the token, and the handler scopes each query to that
// identity, so there is nothing here one role may see and another may not.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	own := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}

	// The static path is registered alongside the wildcard one; net/http's
	// pattern matching prefers the more specific route, so /unread-count is
	// never captured as an {id}.
	mux.Handle("GET "+prefix+"/notification", own(h.List))
	mux.Handle("GET "+prefix+"/notification/unread-count", own(h.UnreadCount))

	mux.Handle("PUT "+prefix+"/notification/read-all", own(h.MarkAllRead))
	mux.Handle("PUT "+prefix+"/notification/{id}/read", own(h.MarkRead))
}
