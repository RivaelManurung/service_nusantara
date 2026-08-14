package review

import (
	"net/http"
	"strconv"
	"strings"

	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service: decode, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// List handles GET /review.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	page := httpx.ParsePagination(r)

	rating, err := optionalInt(r, "rating")
	if err != nil {
		return err
	}
	status, err := optionalInt(r, "status")
	if err != nil {
		return err
	}

	items, total, err := h.service.List(r.Context(), ListQuery{
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
		Search:  r.URL.Query().Get("search"),
		Rating:  rating,
		Status:  status,
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "reviews retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /review/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "review retrieved", item)
	return nil
}

// SetStatus handles PUT /review/{id}/edit-status -- the moderation action.
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var body struct {
		Status *int `json:"status" validate:"required,oneof=0 1"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	if err := h.service.SetStatus(r.Context(), id, *body.Status); err != nil {
		return err
	}

	httpx.OK(w, r, "review status updated", nil)
	return nil
}

// Delete handles DELETE /review/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "review deleted", nil)
	return nil
}

// optionalInt reads a filter that may be absent.
//
// Absent means "no filter" and returns nil; present but unparseable is rejected
// rather than ignored, because silently dropping a filter shows the moderator
// more rows than they asked to see.
func optionalInt(r *http.Request, name string) (*int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: name, Message: "must be a whole number"}})
	}
	return &value, nil
}
