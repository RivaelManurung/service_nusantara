package product

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service: decode, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// List handles GET /product.
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

	httpx.Paginated(w, r, "products retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /product/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "product retrieved", item)
	return nil
}

// Create handles POST /product/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	input, err := h.parseInput(r, true)
	if err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), input)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "product created", created)
	return nil
}

// Update handles PUT /product/{id}/edit.
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

	httpx.OK(w, r, "product updated", updated)
	return nil
}

// SetStatus handles PUT /product/{id}/edit-status.
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

	httpx.OK(w, r, "product status updated", nil)
	return nil
}

// Delete handles DELETE /product/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "product deleted", nil)
	return nil
}

// parseInput reads the multipart body shared by create and update.
//
// The two differ only in how they name the files: create sends `cover` and
// `gallery`, while an edit sends `new_cover` and `new_gallery` alongside
// `replace_gallery`. That asymmetry comes from the client already in the
// field, so it is honoured here rather than renamed.
func (h *Handler) parseInput(r *http.Request, create bool) (Input, error) {
	form, err := httpx.ParseMultipart(r)
	if err != nil {
		return Input{}, err
	}

	coverField, galleryField := "new_cover", "new_gallery"
	if create {
		coverField, galleryField = "cover", "gallery"
	}

	input := Input{
		Name:        form.RequiredMax("name", 255),
		Code:        form.RequiredMax("code", 255),
		Price:       form.Int("price", 0),
		Unit:        form.RequiredMax("unit", 50),
		Description: form.String("description"),
		Status:      form.Int("status", StatusActive),
		TypeProduct: form.UUID("type_product_id"),
		Gallery:     form.Files(galleryField),
	}

	if create {
		input.Cover = form.RequiredFile(coverField)
	} else {
		input.Cover = form.File(coverField)
		// Absent means "leave the stored gallery alone"; the client only sends
		// the whole gallery again when the user actually edited it.
		input.ReplaceGallery = form.Bool("replace_gallery")
	}

	if err := form.Err(); err != nil {
		return Input{}, err
	}

	if err := validateInput(input, galleryField); err != nil {
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

// validateInput covers the rules the Form helpers do not express, reporting
// them together the same way Form.Err does.
func validateInput(input Input, galleryField string) error {
	var errs []httpx.FieldError

	if input.Price < 0 {
		errs = append(errs, httpx.FieldError{Field: "price", Message: "must not be negative"})
	}
	if len(input.Gallery) > MaxGalleryFiles {
		errs = append(errs, httpx.FieldError{
			Field:   galleryField,
			Message: "carries more than " + strconv.Itoa(MaxGalleryFiles) + " images",
		})
	}

	if len(errs) == 0 {
		return nil
	}
	return httpx.Validation("request validation failed").WithDetails(errs)
}
