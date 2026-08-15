package customer

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// List handles GET /user.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	filters, err := parseFilters(r)
	if err != nil {
		return err
	}

	page := httpx.ParsePagination(r)

	rows, total, err := h.service.List(r.Context(), ListQuery{
		Filters: filters,
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "accounts retrieved", rows, page.WithTotal(total))
	return nil
}

// Get handles GET /user/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	detail, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "account retrieved", detail)
	return nil
}

// Roles handles GET /user/roles.
//
// The list screen's role filter is built from this rather than from a constant
// in the client, so adding a role in the admin UI does not silently leave the
// filter unable to find its holders.
func (h *Handler) Roles(w http.ResponseWriter, r *http.Request) error {
	names, err := h.service.Roles(r.Context())
	if err != nil {
		return err
	}

	httpx.OK(w, r, "roles retrieved", names)
	return nil
}

// statusBody is the PUT /user/{id}/status payload.
type statusBody struct {
	Status int    `json:"status"`
	Reason string `json:"reason"`
}

// SetStatus handles PUT /user/{id}/status.
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) error {
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return httpx.Unauthorized("authentication required")
	}
	actorID, err := uuid.Parse(identity.UserID)
	if err != nil {
		return httpx.Unauthorized("your session is not valid")
	}

	targetID, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var body statusBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	detail, err := h.service.SetStatus(r.Context(), actorID, targetID, body.Status, body.Reason)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "account status updated", detail)
	return nil
}

// parseFilters reads the query string, reporting every problem together.
func parseFilters(r *http.Request) (Filters, error) {
	query := r.URL.Query()
	var fields []httpx.FieldError

	filters := Filters{
		Search: strings.TrimSpace(query.Get("search")),
		Role:   strings.TrimSpace(query.Get("role")),
	}

	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		status, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			fields = append(fields, httpx.FieldError{Field: "status", Message: "must be a number"})
		case status != StatusActive && status != StatusBlocked:
			fields = append(fields, httpx.FieldError{
				Field:   "status",
				Message: "must be 0 (blocked) or 1 (active)",
			})
		default:
			filters.Status = &status
		}
	}

	if len(fields) > 0 {
		return Filters{}, httpx.Validation("request validation failed").WithDetails(fields)
	}
	return filters, nil
}
