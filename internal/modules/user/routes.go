package user

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Role names recognised by the API.
const (
	RoleSuperAdmin = "superadmin"
	RoleAdmin      = "admin"
)

// Register mounts the account routes on mux under prefix.
//
// Authentication is applied per route group rather than globally, so a new
// endpoint is unauthenticated only when someone writes it into the public block
// on purpose.
//
// rateLimit is mounted *inside* authenticate on protected routes. Order is
// outer-to-inner, so authenticate resolves the caller before the limiter picks
// its key; mounting the limiter in the outer server chain instead would leave
// it keyed by IP for every request, and users behind one NAT would share a
// single budget.
func Register(
	mux *http.ServeMux,
	prefix string,
	h *Handler,
	authenticate middleware.Middleware,
	rateLimit middleware.Middleware,
) {
	public := map[string]httpx.Handler{
		// Email and password.
		"POST " + prefix + "/auth/register": h.Register,
		"POST " + prefix + "/auth/login":    h.Login,
		// Google and Apple: the app obtains an ID token, the backend verifies it.
		"POST " + prefix + "/auth/google": h.SignInWithGoogle,
		"POST " + prefix + "/auth/apple":  h.SignInWithApple,
		// Phone: request a code, then exchange it for a session.
		"POST " + prefix + "/auth/phone/request": h.StartPhoneSignIn,
		"POST " + prefix + "/auth/phone/verify":  h.VerifyPhoneSignIn,

		"POST " + prefix + "/auth/refresh": h.Refresh,
	}
	for pattern, handler := range public {
		mux.Handle(pattern, rateLimit(handler))
	}

	protected := map[string]httpx.Handler{
		"POST " + prefix + "/auth/logout":    h.Logout,
		"GET " + prefix + "/auth/profile":    h.Profile,
		"GET " + prefix + "/auth/methods":    h.SignInMethods,
		"PATCH " + prefix + "/auth/password": h.ChangePassword,
	}
	for pattern, handler := range protected {
		mux.Handle(pattern, authenticate(rateLimit(handler)))
	}
}
