package notification

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Register mounts the module.
//
// The inbox routes are authenticated and not role restricted: the inbox
// belongs to whoever is holding the token, and the handler scopes each query
// to that identity, so there is nothing there one role may see and another may
// not.
//
// The broadcast route is the exception, and the only one in the module that
// writes another account's inbox. It is guarded by requireSend -- the
// notification.write permission from the catalogue -- rather than by a
// hardcoded role, so who may send promos stays an operational decision made in
// the role screen instead of a redeploy.
func Register(
	mux *http.ServeMux,
	prefix string,
	h *Handler,
	authenticate, rateLimit, requireSend middleware.Middleware,
) {
	own := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	send := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireSend(handler)))
	}

	mux.Handle("POST "+prefix+"/notification/send", send(h.Send))
	mux.Handle("GET "+prefix+"/notification/audience", send(h.Customers))

	// The send history. Guarded by notification.write rather than a read code:
	// it is a record of back-office actions, not inbox content, and whoever may
	// broadcast is exactly who needs to see what has already gone out.
	//
	// The literal segment keeps it clear of "GET /notification", which is the
	// caller's own inbox and takes no id.
	mux.Handle("GET "+prefix+"/notification/broadcast", send(h.Broadcasts))

	// The static path is registered alongside the wildcard one; net/http's
	// pattern matching prefers the more specific route, so /unread-count is
	// never captured as an {id}.
	mux.Handle("GET "+prefix+"/notification", own(h.List))
	mux.Handle("GET "+prefix+"/notification/unread-count", own(h.UnreadCount))

	mux.Handle("PUT "+prefix+"/notification/read-all", own(h.MarkAllRead))
	mux.Handle("PUT "+prefix+"/notification/{id}/read", own(h.MarkRead))
}
