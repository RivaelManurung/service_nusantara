package voucher

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

// body is the JSON payload for create and update.
//
// Vouchers carry no image, so unlike the catalogue modules these endpoints take
// JSON. Status is a pointer because it is settable only at creation time: the
// update payload omits it, and /edit-status owns it afterwards.
type body struct {
	Code            string    `json:"code" validate:"required,max=50"`
	DiscountType    string    `json:"discount_type" validate:"required,oneof=amount percent"`
	DiscountAmount  int       `json:"discount_amount" validate:"gte=0"`
	DiscountPercent int       `json:"discount_percent" validate:"gte=0,lte=100"`
	MinimumSpend    int       `json:"minimum_spend" validate:"gte=1"`
	PointCost       int       `json:"point_cost" validate:"gte=1"`
	Quota           int       `json:"quota" validate:"gte=1"`
	StartDate       time.Time `json:"start_date" validate:"required"`
	EndDate         time.Time `json:"end_date" validate:"required"`
	Description     string    `json:"description" validate:"required,max=2000"`
	Status          *int      `json:"status" validate:"omitempty,oneof=0 1"`
}

func (b body) toInput() Input {
	input := Input{
		Code:            b.Code,
		DiscountType:    b.DiscountType,
		DiscountAmount:  b.DiscountAmount,
		DiscountPercent: b.DiscountPercent,
		MinimumSpend:    b.MinimumSpend,
		PointCost:       b.PointCost,
		Quota:           b.Quota,
		StartDate:       b.StartDate,
		EndDate:         b.EndDate,
		Description:     b.Description,
		Status:          StatusInactive,
	}
	if b.Status != nil {
		input.Status = *b.Status
	}
	return input
}

// List handles GET /voucher.
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

	httpx.Paginated(w, r, "vouchers retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /voucher/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "voucher retrieved", item)
	return nil
}

// Create handles POST /voucher/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	input, err := h.parseInput(r)
	if err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), input)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "voucher created", created)
	return nil
}

// Update handles PUT /voucher/{id}/edit.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	input, err := h.parseInput(r)
	if err != nil {
		return err
	}

	updated, err := h.service.Update(r.Context(), id, input)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "voucher updated", updated)
	return nil
}

// SetStatus handles PUT /voucher/{id}/edit-status.
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var payload struct {
		Status *int `json:"status" validate:"required,oneof=0 1"`
	}
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		return err
	}

	if err := h.service.SetStatus(r.Context(), id, *payload.Status); err != nil {
		return err
	}

	httpx.OK(w, r, "voucher status updated", nil)
	return nil
}

// Delete handles DELETE /voucher/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "voucher deleted", nil)
	return nil
}

// parseInput reads the JSON body shared by create and update.
func (h *Handler) parseInput(r *http.Request) (Input, error) {
	var payload body
	if err := httpx.DecodeJSON(r, &payload); err != nil {
		return Input{}, err
	}

	input := payload.toInput()

	// The acting user comes from the verified token, never from the body.
	if identity, ok := auth.IdentityFrom(r.Context()); ok {
		if userID, parseErr := uuid.Parse(identity.UserID); parseErr == nil {
			input.CreatedBy = userID
		}
	}

	return input, nil
}
