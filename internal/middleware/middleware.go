// Package middleware holds the cross-cutting HTTP layers.
//
// Order matters and is fixed in internal/server: RequestID -> Logger ->
// Recover -> SecurityHeaders -> CORS -> BodyLimit, then per route
// Authenticate -> RateLimit -> RequireRole.
//
// RateLimit sits inside Authenticate on purpose: it keys on the authenticated
// user when there is one, which it cannot do if it runs first.
package middleware

import "net/http"

// Middleware decorates a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so that the first argument is the outermost layer,
// which is the order a reader expects from the list at the call site.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		h = mw[i](h)
	}
	return h
}
