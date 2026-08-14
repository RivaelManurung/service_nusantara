package customeraddress

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// RoleCustomer owns delivery addresses. The service being replaced enforced
// this with a per-request join against roles that answered 404 "user not
// permission"; the same rule is a role check here, which costs no query and
// returns the honest 403.
const RoleCustomer = "customer"

// addressPath is the sub-tree these routes live under. The `/customer` segment
// is part of the URL the clients already call, so it is spelled out here rather
// than being a caller's choice.
const addressPath = "/customer/addresses"

// Register mounts the module.
//
// Route precedence note: Go 1.22's ServeMux resolves conflicts by specificity,
// not by registration order, so "GET /customer/addresses/default" wins over
// "GET /customer/addresses/{id}" for the literal path. `default`,
// `nearby-shops` and `public-nearby-shops` therefore cannot be swallowed as
// address ids, and the pattern set contains no ambiguous pair that would make
// the mux panic at registration.
//
// rateLimit is mounted inside authenticate on the private routes so the limiter
// keys on the user rather than on the NAT they share. The public route has no
// identity to key on and is limited by IP, which is the whole reason it must
// still carry the limiter: it is the one endpoint here that an anonymous script
// can hammer.
func Register(
	mux *http.ServeMux,
	prefix string,
	h *Handler,
	authenticate middleware.Middleware,
	rateLimit middleware.Middleware,
) {
	customer := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(RoleCustomer)(handler)))
	}

	private := map[string]httpx.Handler{
		"POST " + prefix + addressPath + "/create":          h.Create,
		"GET " + prefix + addressPath:                       h.List,
		"GET " + prefix + addressPath + "/default":          h.GetDefault,
		"GET " + prefix + addressPath + "/nearby-shops":     h.NearbyShops,
		"GET " + prefix + addressPath + "/{id}":             h.Get,
		"PUT " + prefix + addressPath + "/{id}/edit":        h.Update,
		"PUT " + prefix + addressPath + "/{id}/set-default": h.SetDefault,
		"DELETE " + prefix + addressPath + "/{id}/delete":   h.Delete,
	}
	for pattern, handler := range private {
		mux.Handle(pattern, customer(handler))
	}

	// Unauthenticated: the storefront shows nearby shops before sign-in. It
	// returns only what a shop card renders, and it is rate limited like every
	// other public endpoint.
	mux.Handle("GET "+prefix+addressPath+"/public-nearby-shops", rateLimit(httpx.Handler(h.PublicNearbyShops)))
}
