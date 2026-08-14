package role

import (
	"net/http"

	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service: decode, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// body is the JSON payload for create and update.
//
// Roles carry no image, so unlike the catalogue modules these endpoints take
// JSON -- which is also what the previous handler's c.Bind read.
type body struct {
	Name string `json:"name" validate:"required,max=100"`
}

// List handles GET /role.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	page := httpx.ParsePagination(r)

	items, total, err := h.service.List(r.Context(), ListQuery{
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
		Search:  r.URL.Query().Get("search"),
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "roles retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /role/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "role retrieved", item)
	return nil
}

// Create handles POST /role/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	var payload body
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), Input{Name: payload.Name})
	if err != nil {
		return err
	}

	httpx.Created(w, r, "role created", created)
	return nil
}

// Update handles PUT /role/{id}/edit.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var payload body
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		return err
	}

	updated, err := h.service.Update(r.Context(), id, Input{Name: payload.Name})
	if err != nil {
		return err
	}

	httpx.OK(w, r, "role updated", updated)
	return nil
}

// Delete handles DELETE /role/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "role deleted", nil)
	return nil
}
