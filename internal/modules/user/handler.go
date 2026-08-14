package user

import (
	"net/http"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service. It contains no business rules: its job is
// to decode, delegate and render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Register handles POST /api/v1/auth/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) error {
	var req RegisterRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}

	profile, err := h.service.Register(r.Context(), req)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "account registered", profile)
	return nil
}

// Login handles POST /api/v1/auth/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) error {
	var req LoginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}

	pair, err := h.service.Login(r.Context(), req)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "login success", pair)
	return nil
}

// Refresh handles POST /api/v1/auth/refresh.
func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) error {
	var req RefreshRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}

	pair, err := h.service.Refresh(r.Context(), req)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "token refreshed", pair)
	return nil
}

// Logout handles POST /api/v1/auth/logout.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) error {
	identity, err := requireIdentity(r)
	if err != nil {
		return err
	}

	// The body is optional here, so an absent one is not an error.
	var req RefreshRequest
	if r.ContentLength > 0 {
		if err := httpx.DecodeJSON(r, &req); err != nil {
			return err
		}
	}

	if err := h.service.Logout(r.Context(), identity, req.RefreshToken); err != nil {
		return err
	}

	httpx.OK(w, r, "logout success", nil)
	return nil
}

// Profile handles GET /api/v1/auth/profile.
func (h *Handler) Profile(w http.ResponseWriter, r *http.Request) error {
	identity, err := requireIdentity(r)
	if err != nil {
		return err
	}

	profile, err := h.service.Profile(r.Context(), identity.UserID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "profile retrieved", profile)
	return nil
}

// ChangePassword handles PATCH /api/v1/auth/password.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) error {
	identity, err := requireIdentity(r)
	if err != nil {
		return err
	}

	var req ChangePasswordRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}

	if err := h.service.ChangePassword(r.Context(), identity, req); err != nil {
		return err
	}

	httpx.OK(w, r, "password changed, please sign in again", nil)
	return nil
}

// requireIdentity reads the caller identity placed by the auth middleware.
// Unlike the previous handlers, it never type-asserts without checking, so a
// malformed token cannot panic the goroutine.
func requireIdentity(r *http.Request) (auth.Identity, error) {
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return auth.Identity{}, httpx.Unauthorized("authentication required")
	}
	return identity, nil
}

// SignInWithGoogle handles POST /api/v1/auth/google.
func (h *Handler) SignInWithGoogle(w http.ResponseWriter, r *http.Request) error {
	return h.socialSignIn(w, r, ProviderGoogle)
}

// SignInWithApple handles POST /api/v1/auth/apple.
func (h *Handler) SignInWithApple(w http.ResponseWriter, r *http.Request) error {
	return h.socialSignIn(w, r, ProviderApple)
}

func (h *Handler) socialSignIn(w http.ResponseWriter, r *http.Request, provider string) error {
	var req SocialLoginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}

	pair, err := h.service.SignInWithProvider(r.Context(), provider, req)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "login success", pair)
	return nil
}

// StartPhoneSignIn handles POST /api/v1/auth/phone/request.
func (h *Handler) StartPhoneSignIn(w http.ResponseWriter, r *http.Request) error {
	var req PhoneStartRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}

	result, err := h.service.StartPhoneSignIn(r.Context(), req)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "verification code sent", result)
	return nil
}

// VerifyPhoneSignIn handles POST /api/v1/auth/phone/verify.
func (h *Handler) VerifyPhoneSignIn(w http.ResponseWriter, r *http.Request) error {
	var req PhoneVerifyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		return err
	}

	pair, err := h.service.VerifyPhoneSignIn(r.Context(), req)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "login success", pair)
	return nil
}

// SignInMethods handles GET /api/v1/auth/methods.
func (h *Handler) SignInMethods(w http.ResponseWriter, r *http.Request) error {
	identity, err := requireIdentity(r)
	if err != nil {
		return err
	}

	methods, err := h.service.SignInMethods(r.Context(), identity.UserID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "sign-in methods retrieved", methods)
	return nil
}
