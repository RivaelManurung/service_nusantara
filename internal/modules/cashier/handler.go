package cashier

import (
	"net/http"

	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service: decode, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// List handles GET /cashier.
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

	httpx.Paginated(w, r, "cashiers retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /cashier/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "cashier retrieved", item)
	return nil
}

// Create handles POST /cashier/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	form, err := httpx.ParseMultipart(r)
	if err != nil {
		return err
	}

	input := CreateInput{
		Name:     form.RequiredMax("name", 255),
		Username: form.RequiredMax("username", 64),
		Email:    form.RequiredMax("email", 255),
		// Trimmed, matching the legacy use case which did the same before
		// hashing; its length bounds are enforced in the service, next to the
		// hasher that actually cares about them.
		Password: form.Required("password"),
		Status:   form.Int("status", StatusActive),
		Image:    form.RequiredFile("image"),
	}

	if err := form.Err(); err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), input)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "cashier created", created)
	return nil
}

// Update handles PUT /cashier/{id}/edit.
//
// This is also the status toggle: the legacy service never registered
// /cashier/{id}/edit-status, so the web client sends `status` here on its own.
// Every field is therefore optional and only what was sent is applied.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	form, err := httpx.ParseMultipart(r)
	if err != nil {
		return err
	}

	var input UpdateInput
	if name := form.String("name"); name != "" {
		input.Name = &name
	}
	if username := form.String("username"); username != "" {
		input.Username = &username
	}
	if raw := form.String("status"); raw != "" {
		status := form.Int("status", StatusActive)
		input.Status = &status
	}
	input.Image = form.File("image")

	if err := form.Err(); err != nil {
		return err
	}

	updated, err := h.service.Update(r.Context(), id, input)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "cashier updated", updated)
	return nil
}

// Delete handles DELETE /cashier/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "cashier deleted", nil)
	return nil
}
