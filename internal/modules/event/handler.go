package event

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// dateLayout is the format the clients already send, kept from the previous
// service so no app has to be updated.
const dateLayout = time.RFC3339

// Handler adapts HTTP to the Service: decode, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// List handles GET /event.
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

	httpx.Paginated(w, r, "events retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /event/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "event retrieved", item)
	return nil
}

// Create handles POST /event/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	input, err := h.parseInput(r, true)
	if err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), input)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "event created", created)
	return nil
}

// Update handles PUT /event/{id}/edit.
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

	httpx.OK(w, r, "event updated", updated)
	return nil
}

// SetStatus handles PUT /event/{id}/edit-status.
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

	httpx.OK(w, r, "event status updated", nil)
	return nil
}

// Delete handles DELETE /event/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "event deleted", nil)
	return nil
}

// parseInput reads the multipart body shared by create and update.
//
// The child rows arrive as JSON strings inside the multipart body -- that is
// what the web client sends, and repeated form fields cannot express a nested
// array. The cover field is also named differently on the two paths: `cover`
// when creating, `new_cover` when replacing.
func (h *Handler) parseInput(r *http.Request, isCreate bool) (Input, error) {
	form, err := httpx.ParseMultipart(r)
	if err != nil {
		return Input{}, err
	}

	// extra collects the problems the Form helpers cannot express, so they are
	// reported together with the field errors rather than one request later.
	var extra []httpx.FieldError

	input := Input{
		Name:      form.RequiredMax("name", 255),
		TypeEvent: Type(form.Required("type_event")),
		StartDate: parseDate(form, "start_date", &extra),
		EndDate:   parseDate(form, "end_date", &extra),
	}

	if isCreate {
		// A new event starts hidden unless the client says otherwise, so an
		// incomplete promotion never goes live.
		input.Status = form.Int("status", StatusInactive)
		input.Cover = form.RequiredFile("cover")
	} else {
		input.Cover = form.File("new_cover")
	}

	input.Products = parseJSONArray[ProductDiscountInput](form, "event_products", &extra)
	input.BundleBuys = parseJSONArray[BundleItemInput](form, "event_bundle_buys", &extra)
	input.BundleRewards = parseJSONArray[BundleItemInput](form, "event_bundle_rewards", &extra)

	if err := mergeErrors(form, extra); err != nil {
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

// parseDate reads a required RFC3339 timestamp.
func parseDate(form *httpx.Form, field string, extra *[]httpx.FieldError) time.Time {
	raw := form.Required(field)
	if raw == "" {
		return time.Time{}
	}

	value, err := time.Parse(dateLayout, raw)
	if err != nil {
		*extra = append(*extra, httpx.FieldError{
			Field:   field,
			Message: "must be a timestamp in RFC3339 format, for example 2026-01-31T00:00:00Z",
		})
		return time.Time{}
	}
	return value
}

// parseJSONArray decodes one of the JSON-stringified child arrays. An absent
// field is not an error here: which arrays are required depends on the event
// type, which the service decides.
func parseJSONArray[T any](form *httpx.Form, field string, extra *[]httpx.FieldError) []T {
	raw := form.String(field)
	if raw == "" {
		return nil
	}

	var items []T
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		*extra = append(*extra, httpx.FieldError{
			Field:   field,
			Message: "must be a JSON array",
		})
		return nil
	}
	return items
}

// mergeErrors reports the form's own field errors together with the ones
// collected while decoding, as one 422.
func mergeErrors(form *httpx.Form, extra []httpx.FieldError) error {
	var details []httpx.FieldError

	if err := form.Err(); err != nil {
		var appErr *httpx.Error
		if errors.As(err, &appErr) {
			if fields, ok := appErr.Details.([]httpx.FieldError); ok {
				details = append(details, fields...)
			}
		}
	}
	details = append(details, extra...)

	if len(details) == 0 {
		return nil
	}
	return httpx.Validation("request validation failed").WithDetails(details)
}
