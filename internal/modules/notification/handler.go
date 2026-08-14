package notification

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

// List handles GET /notification.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	page := httpx.ParsePagination(r)

	items, total, err := h.service.List(r.Context(), ListQuery{
		UserID:  userID,
		Channel: r.URL.Query().Get("channel"),
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "notifications retrieved", items, page.WithTotal(total))
	return nil
}

// UnreadCount handles GET /notification/unread-count.
func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	counts, err := h.service.UnreadCount(r.Context(), userID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "unread notification count retrieved", counts)
	return nil
}

// MarkAllRead handles PUT /notification/read-all.
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	updated, err := h.service.MarkAllRead(r.Context(), userID, r.URL.Query().Get("channel"))
	if err != nil {
		return err
	}

	httpx.OK(w, r, "notifications marked as read", map[string]int64{"updated": updated})
	return nil
}

// MarkRead handles PUT /notification/{id}/read.
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.MarkRead(r.Context(), userID, id); err != nil {
		return err
	}

	httpx.OK(w, r, "notification marked as read", nil)
	return nil
}

// callerID is the owner of every inbox operation. It comes from the verified
// token, never from the path, the query or the body -- otherwise any caller
// could read another account's notifications by sending their user id.
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
