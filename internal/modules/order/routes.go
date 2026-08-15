package order

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Register mounts the module.
//
// Authorisation is by permission, not by role. role/catalog.go has carried
// order.read and order.write since the permission screen was built -- an
// operator could tick "Kelola pesanan" and have it mean nothing, because no
// endpoint consulted it. These routes are what make those two codes real.
//
// requireRead and requireWrite are built by the server wiring from the shared
// permission resolver, the same way the notification broadcast route is.
//
// Note the ordering constraint on the mux: "GET /order/lifecycle" is registered
// alongside "GET /order/{id}". Go's ServeMux prefers the more specific literal
// pattern over the wildcard, so /order/lifecycle reaches Lifecycle and is never
// parsed as an id -- but only because the literal segment is spelled out here.
func Register(
	mux *http.ServeMux,
	prefix string,
	h *Handler,
	authenticate, rateLimit middleware.Middleware,
	requireRead, requireWrite middleware.Middleware,
) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireRead(handler)))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireWrite(handler)))
	}

	mux.Handle("GET "+prefix+"/order", read(h.List))
	mux.Handle("GET "+prefix+"/order/lifecycle", read(h.Lifecycle))
	mux.Handle("GET "+prefix+"/order/{id}", read(h.Get))
	mux.Handle("GET "+prefix+"/order/{id}/timeline", read(h.Timeline))

	// PUT rather than POST, matching the /x/{id}/edit-status convention the
	// other modules use: this replaces one field on an existing record.
	//
	// There is deliberately no create and no delete. Orders come from checkout,
	// and history is cancelled rather than erased -- a DELETE here would make
	// every financial report unreproducible.
	mux.Handle("PUT "+prefix+"/order/{id}/status", write(h.SetStatus))

	// The customer's own orders, for the Flutter app.
	//
	// Authenticated and rate limited, but deliberately NOT permission-guarded:
	// order.read is a back-office grant, and a customer holds none of the
	// catalogue's codes. The scope is the caller's own user id, forced from the
	// token inside the service.
	//
	// The /customer prefix is the convention this codebase already uses for
	// caller-scoped reads, mirroring shop/routes.go's /cashier group. It also
	// avoids a hard constraint: registering "/order/my/{id}" alongside
	// "/order/{id}/timeline" makes /order/my/timeline match both patterns with
	// neither more specific, and ServeMux panics at registration.
	own := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}

	mux.Handle("GET "+prefix+"/customer/order", own(h.MyList))
	mux.Handle("GET "+prefix+"/customer/order/{id}", own(h.MyGet))
	mux.Handle("GET "+prefix+"/customer/order/{id}/timeline", own(h.MyTimeline))
}
