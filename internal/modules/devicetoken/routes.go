package devicetoken

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Register mounts the module.
//
// Every route is authenticated and none is role restricted, exactly like the
// notification inbox: a registration belongs to whoever is holding the token,
// and the handler scopes each write to that identity.
//
// Rate limiting matters more here than elsewhere. The app re-registers on
// every launch, and an unlimited endpoint that writes a row per call is an
// invitation to fill the table from one signed-in account.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	own := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}

	mux.Handle("GET "+prefix+"/device-token", own(h.List))
	mux.Handle("POST "+prefix+"/device-token/register", own(h.Register))
	mux.Handle("POST "+prefix+"/device-token/unregister", own(h.Unregister))
}
