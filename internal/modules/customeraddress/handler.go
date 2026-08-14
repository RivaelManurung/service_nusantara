package customeraddress

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service: decode, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// addressBody is the create/update payload.
//
// Longitude is accepted under four names because that is what is on the wire:
// the previous service bound it as `lang` on create and `lng` on update, and
// the mobile clients settled the ambiguity by sending every spelling at once.
// httpx.DecodeJSON rejects unknown fields, so each alias has to be declared
// here or a real request from a shipped app would be answered with a 400.
type addressBody struct {
	Label       *string `json:"label" validate:"omitempty,max=100"`
	AddressText *string `json:"address_text" validate:"omitempty,max=1000"`

	Lat      *float64 `json:"lat" validate:"omitempty,gte=-90,lte=90"`
	Latitude *float64 `json:"latitude" validate:"omitempty,gte=-90,lte=90"`

	Lang      *float64 `json:"lang" validate:"omitempty,gte=-180,lte=180"`
	Lng       *float64 `json:"lng" validate:"omitempty,gte=-180,lte=180"`
	Longitude *float64 `json:"longitude" validate:"omitempty,gte=-180,lte=180"`
}

// latitude and longitude collapse the aliases, preferring the key the service
// itself emits.
func (b addressBody) latitude() *float64 {
	return firstNonNil(b.Lat, b.Latitude)
}

func (b addressBody) longitude() *float64 {
	return firstNonNil(b.Lang, b.Lng, b.Longitude)
}

// List handles GET /customer/addresses.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	items, err := h.service.List(r.Context(), userID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "addresses retrieved successfully", items)
	return nil
}

// Get handles GET /customer/addresses/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	item, err := h.service.Get(r.Context(), userID, id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "address retrieved successfully", item)
	return nil
}

// GetDefault handles GET /customer/addresses/default.
func (h *Handler) GetDefault(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	item, err := h.service.GetDefault(r.Context(), userID)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "default address retrieved successfully", item)
	return nil
}

// Create handles POST /customer/addresses/create.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	var body addressBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	input, err := body.required()
	if err != nil {
		return err
	}

	created, err := h.service.Create(r.Context(), userID, input)
	if err != nil {
		return err
	}

	httpx.Created(w, r, "address created successfully", created)
	return nil
}

// Update handles PUT /customer/addresses/{id}/edit.
//
// The update is a partial one, as it was before: an omitted field keeps its
// stored value rather than being blanked.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var body addressBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	patch, err := body.patch()
	if err != nil {
		return err
	}

	updated, err := h.service.Update(r.Context(), userID, id, patch)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "address updated successfully", updated)
	return nil
}

// Delete handles DELETE /customer/addresses/{id}/delete.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.Delete(r.Context(), userID, id); err != nil {
		return err
	}

	httpx.OK(w, r, "address deleted successfully", nil)
	return nil
}

// SetDefault handles PUT /customer/addresses/{id}/set-default.
func (h *Handler) SetDefault(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	if err := h.service.SetDefault(r.Context(), userID, id); err != nil {
		return err
	}

	httpx.OK(w, r, "default address updated successfully", nil)
	return nil
}

// NearbyShops handles GET /customer/addresses/nearby-shops.
//
// A lat/lng pair in the query string wins -- that is the device's current
// position -- and the customer's default address is the fallback.
func (h *Handler) NearbyShops(w http.ResponseWriter, r *http.Request) error {
	userID, err := callerID(r)
	if err != nil {
		return err
	}

	origin, ok, err := coordinateFromQuery(r)
	if err != nil {
		return err
	}
	if !ok {
		if origin, err = h.service.OriginForUser(r.Context(), userID); err != nil {
			return err
		}
	}

	return h.renderNearby(w, r, origin)
}

// PublicNearbyShops handles GET /customer/addresses/public-nearby-shops.
//
// The unauthenticated variant. There is no identity to fall back on, so the
// coordinate is mandatory, and it renders the same lean shop card as the
// authenticated route -- no owner, no staff, no product list.
func (h *Handler) PublicNearbyShops(w http.ResponseWriter, r *http.Request) error {
	origin, ok, err := coordinateFromQuery(r)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{
				{Field: "lat", Message: "is required"},
				{Field: "lng", Message: "is required"},
			})
	}

	return h.renderNearby(w, r, origin)
}

func (h *Handler) renderNearby(w http.ResponseWriter, r *http.Request, origin Point) error {
	shops, err := h.service.NearbyShops(r.Context(), origin)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "nearby shops retrieved successfully", shops)
	return nil
}

// required turns the body into a complete Input, rejecting anything missing.
func (b addressBody) required() (Input, error) {
	var fields []httpx.FieldError

	label := trimmed(b.Label)
	if label == "" {
		fields = append(fields, httpx.FieldError{Field: "label", Message: "is required"})
	}

	text := trimmed(b.AddressText)
	if text == "" {
		fields = append(fields, httpx.FieldError{Field: "address_text", Message: "is required"})
	}

	lat := b.latitude()
	if lat == nil {
		fields = append(fields, httpx.FieldError{Field: "lat", Message: "is required"})
	}

	lng := b.longitude()
	if lng == nil {
		fields = append(fields, httpx.FieldError{Field: "lang", Message: "is required"})
	}

	if len(fields) > 0 {
		return Input{}, httpx.Validation("request validation failed").WithDetails(fields)
	}

	return Input{Label: label, AddressText: text, Lat: *lat, Lng: *lng}, nil
}

// patch turns the body into the set of fields the caller actually sent.
func (b addressBody) patch() (Patch, error) {
	patch := Patch{Lat: b.latitude(), Lng: b.longitude()}

	if b.Label != nil {
		label := trimmed(b.Label)
		if label == "" {
			return Patch{}, httpx.Validation("request validation failed").
				WithDetails([]httpx.FieldError{{Field: "label", Message: "must not be blank"}})
		}
		patch.Label = &label
	}

	if b.AddressText != nil {
		text := trimmed(b.AddressText)
		if text == "" {
			return Patch{}, httpx.Validation("request validation failed").
				WithDetails([]httpx.FieldError{{Field: "address_text", Message: "must not be blank"}})
		}
		patch.AddressText = &text
	}

	return patch, nil
}

// coordinateFromQuery reads `lat` and `lng`. Both are needed or neither counts:
// half a coordinate is not a location, and silently defaulting the other half
// to zero would rank shops against a point in the Gulf of Guinea.
func coordinateFromQuery(r *http.Request) (Point, bool, error) {
	query := r.URL.Query()
	rawLat := strings.TrimSpace(query.Get("lat"))
	rawLng := strings.TrimSpace(query.Get("lng"))

	if rawLat == "" && rawLng == "" {
		return Point{}, false, nil
	}

	var fields []httpx.FieldError

	lat, err := strconv.ParseFloat(rawLat, 64)
	if rawLat == "" {
		fields = append(fields, httpx.FieldError{Field: "lat", Message: "is required alongside lng"})
	} else if err != nil {
		fields = append(fields, httpx.FieldError{Field: "lat", Message: "must be a number"})
	}

	lng, err := strconv.ParseFloat(rawLng, 64)
	if rawLng == "" {
		fields = append(fields, httpx.FieldError{Field: "lng", Message: "is required alongside lat"})
	} else if err != nil {
		fields = append(fields, httpx.FieldError{Field: "lng", Message: "must be a number"})
	}

	if len(fields) > 0 {
		return Point{}, false, httpx.Validation("request validation failed").WithDetails(fields)
	}

	return Point{Lat: lat, Lng: lng}, true, nil
}

// callerID reads the acting user out of the verified token. Identity never
// comes from the body or the URL, so one customer cannot address another's
// records by editing a field.
func callerID(r *http.Request) (uuid.UUID, error) {
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return uuid.Nil, httpx.Unauthorized("authentication required")
	}

	userID, err := uuid.Parse(identity.UserID)
	if err != nil {
		// A token that passed verification but carries an unusable subject is a
		// server-side problem, not something the caller can fix by retrying.
		return uuid.Nil, httpx.Internal("the session is missing a usable user id").WithCause(err)
	}
	return userID, nil
}

func trimmed(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func firstNonNil(values ...*float64) *float64 {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
