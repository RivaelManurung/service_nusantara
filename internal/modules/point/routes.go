package point

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Register mounts the module.
//
// The point routes sit under /user/{id} because points are account data, and
// they are guarded by the same permissions as the account screen itself:
// somebody who may read an account may read its balance, and somebody who may
// moderate an account may correct it. Splitting them into their own codes would
// mean a "customer support" role could see a balance it could never explain.
//
// /voucher/{id}/claims is guarded by voucher.read instead: it is voucher
// oversight, reached from the voucher screen, and answers "who took this
// promotion" rather than anything about one person.
//
// No pattern here collides. "GET /user/roles" (customer module) is two
// segments; "GET /user/{id}/point" is three with a literal third; "GET
// /user/{id}" is two with a wildcard second, which ServeMux ranks below the
// literal. See internal/modules/order/routes_test.go for the case where two
// patterns are equally specific and registration panics.
func Register(
	mux *http.ServeMux,
	prefix string,
	h *Handler,
	authenticate, rateLimit middleware.Middleware,
	requireRead, requireWrite, requireVoucherRead middleware.Middleware,
) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireRead(handler)))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireWrite(handler)))
	}
	voucherRead := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireVoucherRead(handler)))
	}

	mux.Handle("GET "+prefix+"/user/{id}/point", read(h.Balance))
	mux.Handle("GET "+prefix+"/user/{id}/point/history", read(h.History))
	mux.Handle("GET "+prefix+"/user/{id}/voucher", read(h.ClaimedVouchers))

	// POST rather than PUT: an adjustment appends a movement to a ledger, it
	// does not replace a value. Making it idempotent-looking would invite a
	// client to retry it, and a retried grant is a doubled grant.
	mux.Handle("POST "+prefix+"/user/{id}/point/adjust", write(h.Adjust))

	mux.Handle("GET "+prefix+"/voucher/{id}/claims", voucherRead(h.Claimants))
}
