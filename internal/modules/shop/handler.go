package shop

import (
	"encoding/json"
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

// List handles GET /shop.
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

	httpx.Paginated(w, r, "shops retrieved", items, page.WithTotal(total))
	return nil
}

// Get handles GET /shop/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "shop retrieved", item)
	return nil
}

// Create handles POST /shop/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	input, err := h.parseInput(r, true)
	if err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), input)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "shop created", created)
	return nil
}

// Update handles PUT /shop/{id}/edit.
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

	httpx.OK(w, r, "shop updated", updated)
	return nil
}

// SetStatus handles PUT /shop/{id}/edit-status.
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

	httpx.OK(w, r, "shop status updated", nil)
	return nil
}

// Delete handles DELETE /shop/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), id); err != nil {
		return err
	}

	httpx.OK(w, r, "shop deleted", nil)
	return nil
}

// AssignedShops handles GET /cashier/shop-names.
func (h *Handler) AssignedShops(w http.ResponseWriter, r *http.Request) error {
	staffID, err := callerID(r)
	if err != nil {
		return err
	}

	items, err := h.service.AssignedShops(r.Context(), staffID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "shop names retrieved", items)
	return nil
}

// AssignedShop handles GET /cashier/shop-cashier/{shop_id}.
func (h *Handler) AssignedShop(w http.ResponseWriter, r *http.Request) error {
	staffID, err := callerID(r)
	if err != nil {
		return err
	}

	shopID, err := httpx.PathUUID(r, "shop_id")
	if err != nil {
		return err
	}

	item, err := h.service.AssignedShop(r.Context(), staffID, shopID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "shop retrieved", item)
	return nil
}

// AssignedShopProducts handles GET /cashier/cashier-shop-product/{shop_id}.
func (h *Handler) AssignedShopProducts(w http.ResponseWriter, r *http.Request) error {
	staffID, err := callerID(r)
	if err != nil {
		return err
	}

	shopID, err := httpx.PathUUID(r, "shop_id")
	if err != nil {
		return err
	}

	items, err := h.service.AssignedShopProducts(r.Context(), staffID, shopID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "shop products retrieved", items)
	return nil
}

// parseInput reads the multipart body shared by create and update.
//
// On create every descriptive field is required; on update an absent field
// means "leave it alone", which is how the edit form sends a partial change.
func (h *Handler) parseInput(r *http.Request, creating bool) (Input, error) {
	form, err := httpx.ParseMultipart(r)
	if err != nil {
		return Input{}, err
	}

	input := Input{
		ReplaceGallery: form.Bool("replace_gallery"),
		Cover:          form.File("cover"),
		Gallery:        form.Files("gallery"),
	}

	if creating {
		input.Name = form.RequiredMax("name", 255)
		input.Description = form.Required("description")
		input.FullAddress = form.RequiredMax("full_address", 255)
		form.Required("lat")
		form.Required("lang")
		// Status is only settable at creation; afterwards it moves through
		// /edit-status so the two paths cannot disagree.
		input.Status = form.Int("status", StatusActive)
		input.Cover = form.RequiredFile("cover")
	} else {
		input.Name = form.String("name")
		input.Description = form.String("description")
		input.FullAddress = form.String("full_address")
		if len(input.Name) > 255 {
			return Input{}, tooLong("name")
		}
		if len(input.FullAddress) > 255 {
			return Input{}, tooLong("full_address")
		}
	}

	input.Lat = optionalFloat(form, "lat")
	input.Lng = optionalFloat(form, "lang")

	if err := form.Err(); err != nil {
		return Input{}, err
	}

	// The JSON-in-multipart fields are parsed after the scalar ones so their
	// errors are reported with the same shape rather than as a bare 400.
	var details []httpx.FieldError

	if raw := form.String("cashier_ids"); raw != "" {
		ids, fieldErrs := parseCashierIDs(raw)
		input.CashierIDs, input.SetCashiers = ids, true
		details = append(details, fieldErrs...)
	}

	if raw := form.String("products"); raw != "" {
		products, fieldErrs := parseProducts(raw)
		input.Products, input.SetProducts = products, true
		details = append(details, fieldErrs...)
	}

	if len(details) > 0 {
		return Input{}, httpx.Validation("request validation failed").WithDetails(details)
	}

	// The acting user comes from the verified token, never from the body.
	if identity, ok := auth.IdentityFrom(r.Context()); ok {
		if userID, parseErr := uuid.Parse(identity.UserID); parseErr == nil {
			input.CreatedBy = userID
		}
	}

	return input, nil
}

// parseCashierIDs reads the `cashier_ids` field, a JSON array of UUID strings.
func parseCashierIDs(raw string) ([]uuid.UUID, []httpx.FieldError) {
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, []httpx.FieldError{{Field: "cashier_ids", Message: "must be a JSON array of ids"}}
	}

	var (
		ids  []uuid.UUID
		errs []httpx.FieldError
	)
	seen := map[uuid.UUID]bool{}
	for _, value := range values {
		parsed, err := uuid.Parse(value)
		if err != nil {
			errs = append(errs, httpx.FieldError{Field: "cashier_ids", Message: "contains an invalid id: " + value})
			continue
		}
		// A duplicated id would insert the same assignment twice.
		if seen[parsed] {
			continue
		}
		seen[parsed] = true
		ids = append(ids, parsed)
	}
	return ids, errs
}

// parseProducts reads the `products` field, a JSON array of
// {product_id, stock, price?, status?}.
func parseProducts(raw string) ([]ProductAssignment, []httpx.FieldError) {
	var values []ProductAssignment
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, []httpx.FieldError{{Field: "products", Message: "must be a JSON array of product assignments"}}
	}

	var errs []httpx.FieldError
	for _, value := range values {
		if value.ProductID == uuid.Nil {
			errs = append(errs, httpx.FieldError{Field: "products", Message: "every entry needs a product_id"})
			break
		}
	}
	return values, errs
}

// optionalFloat returns nil when the field was not sent, so an omitted
// coordinate keeps the stored one instead of moving the shop to the equator.
func optionalFloat(form *httpx.Form, field string) *float64 {
	if form.String(field) == "" {
		return nil
	}
	value := form.Float(field, 0)
	return &value
}

func tooLong(field string) error {
	return httpx.Validation("request validation failed").
		WithDetails([]httpx.FieldError{{Field: field, Message: "is longer than 255 characters"}})
}

// callerID is the authenticated user the cashier-scoped reads are scoped to.
func callerID(r *http.Request) (uuid.UUID, error) {
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return uuid.Nil, httpx.Unauthorized("authentication required")
	}
	id, err := uuid.Parse(identity.UserID)
	if err != nil {
		return uuid.Nil, httpx.Unauthorized("access token is invalid").WithCause(err)
	}
	return id, nil
}
