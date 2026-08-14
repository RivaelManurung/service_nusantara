package shop

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Roles. Only a superadmin may edit the outlet network; the till-facing reads
// belong to the people who actually work a shop.
const (
	roleSuperAdmin = "superadmin"
	roleAdmin      = "admin"
	roleCashier    = "cashier"
)

// Register mounts the module.
//
// Reads of the catalogue are open to any authenticated caller, matching
// type-product. The /cashier group is different: it is restricted by role *and*
// scoped inside the service to the shops the caller is actually assigned to, so
// holding the role is not enough to read another outlet's stock.
func Register(mux *http.ServeMux, prefix string, h *Handler, authenticate, rateLimit middleware.Middleware) {
	read := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(handler))
	}
	write := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin)(handler)))
	}
	staff := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(middleware.RequireRole(roleSuperAdmin, roleAdmin, roleCashier)(handler)))
	}

	mux.Handle("GET "+prefix+"/shop", read(h.List))
	mux.Handle("GET "+prefix+"/shop/{id}", read(h.Get))

	mux.Handle("POST "+prefix+"/shop/create", write(h.Create))
	mux.Handle("PUT "+prefix+"/shop/{id}/edit", write(h.Update))
	mux.Handle("PUT "+prefix+"/shop/{id}/edit-status", write(h.SetStatus))
	mux.Handle("DELETE "+prefix+"/shop/{id}/delete", write(h.Delete))

	mux.Handle("GET "+prefix+"/cashier/shop-names", staff(h.AssignedShops))
	mux.Handle("GET "+prefix+"/cashier/shop-cashier/{shop_id}", staff(h.AssignedShop))
	mux.Handle("GET "+prefix+"/cashier/cashier-shop-product/{shop_id}", staff(h.AssignedShopProducts))
}

// StaffRoles lists the roles allowed to reach the cashier-scoped reads,
// exported so the server wiring can document the intent in one place.
var StaffRoles = []string{roleSuperAdmin, roleAdmin, roleCashier}
