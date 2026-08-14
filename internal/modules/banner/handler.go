package banner

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

// List handles GET /banner.
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

	httpx.Paginated(w, r, "banners retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /banner/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "banner retrieved", item)
	return nil
}

// ListPublic handles GET /banner/customer, the storefront carousel read by the
// mobile app without a token.
func (h *Handler) ListPublic(w http.ResponseWriter, r *http.Request) error {
	items, err := h.service.ListPublic(r.Context())
	if err != nil {
		return err
	}

	httpx.OK(w, r, "banners retrieved", items)
	return nil
}

// GetPublic handles GET /banner/{id}/customer.
func (h *Handler) GetPublic(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.GetPublic(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "banner retrieved", item)
	return nil
}

// Create handles POST /banner/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	input, err := h.parseInput(r, true)
	if err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), input)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "banner created", created)
	return nil
}

// Update handles PUT /banner/{id}/edit.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	input, err := h.parseInput(r, false)
	if err != nil {
		return err
	}

	updated, err := h.service.Update(r.Context(), id, input)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "banner updated", updated)
	return nil
}

// SetStatus handles PUT /banner/{id}/edit-status.
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

	httpx.OK(w, r, "banner status updated", nil)
	return nil
}

// Delete handles DELETE /banner/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "banner deleted", nil)
	return nil
}

// parseInput reads the multipart body shared by create and update.
func (h *Handler) parseInput(r *http.Request, imageRequired bool) (Input, error) {
	form, err := httpx.ParseMultipart(r)
	if err != nil {
		return Input{}, err
	}

	input := Input{
		Name:        form.RequiredMax("name", 255),
		Description: form.Required("description"),
		Status:      form.Int("status", StatusActive),
	}

	if imageRequired {
		input.Image = form.RequiredFile("image")
	} else {
		input.Image = form.File("image")
	}

	if err := form.Err(); err != nil {
		return Input{}, err
	}

	// The acting user comes from the verified token, never from the body.
	if identity, ok := auth.IdentityFrom(r.Context()); ok {
		if userID, parseErr := uuid.Parse(identity.UserID); parseErr == nil {
			input.CreatedBy = userID
		}
	}

	return input, nil
}
