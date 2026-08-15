package devicetoken

import (
	"net/http"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service: decode, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// registerRequest is the body of POST /device-token/register.
//
// There is no user_id field, and there must never be one: the owner comes from
// the token. A client that could name the owner could subscribe a device of
// its choosing to another customer's order updates.
type registerRequest struct {
	Token      string `json:"token" validate:"required,max=4096"`
	Platform   string `json:"platform" validate:"required"`
	AppVersion string `json:"app_version" validate:"omitempty,max=50"`
}

type unregisterRequest struct {
	Token string `json:"token" validate:"required,max=4096"`
}

// Register handles POST /device-token/register.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	var body registerRequest
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	saved, err := h.service.Register(r.Context(), Registration{
		UserID:     userID,
		Token:      body.Token,
		Platform:   body.Platform,
		AppVersion: body.AppVersion,
	})
	if err != nil {
		return err
	}

	httpx.OK(w, r, "device registered for notifications", saved)
	return nil
}

// Unregister handles POST /device-token/unregister.
//
// It is a POST with a body rather than DELETE /{token}: an FCM registration is
// long and full of characters that do not survive a path segment cleanly, and
// putting it in a URL writes it into every access log on the way.
func (h *Handler) Unregister(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	var body unregisterRequest
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	if err := h.service.Unregister(r.Context(), userID, body.Token); err != nil {
		return err
	}

	httpx.OK(w, r, "device unregistered", nil)
	return nil
}

// List handles GET /device-token.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	items, err := h.service.List(r.Context(), userID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "devices retrieved", items)
	return nil
}

// callerID is the owner of every operation here. It comes from the verified
// token, never from the body.
func callerID(r *http.Request) (uuid.UUID, error) {
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return uuid.Nil, httpx.Unauthorized("authentication required")
	}

	userID, err := uuid.Parse(identity.UserID)
	if err != nil {
		return uuid.Nil, httpx.Unauthorized("the token does not carry a valid user id").WithCause(err)
	}
	return userID, nil
}
