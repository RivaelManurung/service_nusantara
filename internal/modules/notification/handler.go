package notification

import (
	"net/http"
	"time"

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

// sendRequest is the body of POST /notification/send.
//
// It is the back office's own shape: an audience, a message, and whether to
// wake the devices. The rules live on the service, so a future scheduled
// broadcast reuses them instead of re-deriving them here.
type sendRequest struct {
	Audience    audienceRequest `json:"audience"`
	Channel     string          `json:"channel"`
	Title       string          `json:"title" validate:"required,max=255"`
	Body        string          `json:"body" validate:"required,max=1000"`
	Type        string          `json:"type"`
	TargetType  string          `json:"target_type"`
	TargetRoute string          `json:"target_route" validate:"omitempty,max=255"`
	ReferenceID *uuid.UUID      `json:"reference_id"`
	// Push is a pointer so "not mentioned" is told apart from "explicitly
	// false". Omitting it means yes: an operator filling in a promo form
	// expects it to reach the phone, not to sit in an inbox nobody opens.
	Push *bool `json:"push"`
}

type audienceRequest struct {
	Mode    string          `json:"mode" validate:"required"`
	UserIDs []uuid.UUID     `json:"user_ids"`
	Segment *segmentRequest `json:"segment"`
}

type segmentRequest struct {
	RoleName       string     `json:"role_name" validate:"omitempty,max=100"`
	HasOrdered     bool       `json:"has_ordered"`
	RegisteredFrom *time.Time `json:"registered_from"`
	RegisteredTo   *time.Time `json:"registered_to"`
}

// Send handles POST /notification/send.
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) error {
	var body sendRequest
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	// Taken from the verified token, never the body: the send history is only
	// worth keeping if "who sent this" cannot be spoofed by the sender.
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return httpx.Unauthorized("authentication required")
	}
	actorID, err := uuid.Parse(identity.UserID)
	if err != nil {
		return httpx.Unauthorized("your session is not valid")
	}

	request := SendRequest{
		ActorID: actorID,
		Audience: Audience{
			Mode:    body.Audience.Mode,
			UserIDs: body.Audience.UserIDs,
		},
		Channel:     body.Channel,
		Title:       body.Title,
		Body:        body.Body,
		Type:        body.Type,
		TargetType:  body.TargetType,
		TargetRoute: body.TargetRoute,
		ReferenceID: body.ReferenceID,
		Push:        body.Push == nil || *body.Push,
	}

	if body.Audience.Segment != nil {
		request.Audience.Segment = Segment{
			RoleName:       body.Audience.Segment.RoleName,
			HasOrdered:     body.Audience.Segment.HasOrdered,
			RegisteredFrom: body.Audience.Segment.RegisteredFrom,
			RegisteredTo:   body.Audience.Segment.RegisteredTo,
		}
	}

	result, err := h.service.Send(r.Context(), request)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "notification sent", result)
	return nil
}

// Broadcasts handles GET /notification/broadcast.
//
// The back office's send history: one row per send, not per recipient. It is
// what the notifications screen lists, so an operator opens a record of what
// has gone out rather than a blank compose form.
func (h *Handler) Broadcasts(w http.ResponseWriter, r *http.Request) error {
	page := httpx.ParsePagination(r)

	rows, total, err := h.service.Broadcasts(r.Context(), page.CurrentPage, page.PerPage)
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "notification history retrieved", rows, page.WithTotal(total))
	return nil
}

// Customers handles GET /notification/audience.
//
// It exists to fill the recipient picker on the broadcast screen, and is
// guarded by the same permission as the send itself: whoever may notify a
// customer may look one up, and nobody else may.
func (h *Handler) Customers(w http.ResponseWriter, r *http.Request) error {
	page := httpx.ParsePagination(r)

	items, total, err := h.service.SearchCustomers(r.Context(), CustomerQuery{
		Search:  r.URL.Query().Get("search"),
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "customers retrieved", items, page.WithTotal(total))
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
