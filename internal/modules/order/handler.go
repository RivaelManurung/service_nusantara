package order

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/auth"
	"service_nusantara/internal/httpx"
	"service_nusantara/internal/model"
)

// Handler adapts HTTP to the Service: parse, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// dateLayout is the format the filter dates arrive in.
const dateLayout = "2006-01-02"

// List handles GET /order.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) error {
	caller, err := callerFrom(r)
	if err != nil {
		return err
	}

	filters, err := parseFilters(r)
	if err != nil {
		return err
	}

	page := httpx.ParsePagination(r)

	rows, total, err := h.service.List(r.Context(), caller, ListQuery{
		Filters: filters,
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "orders retrieved", rows, page.WithTotal(total))
	return nil
}

// Get handles GET /order/{id}.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) error {
	caller, err := callerFrom(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	detail, err := h.service.Get(r.Context(), caller, id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "order retrieved", detail)
	return nil
}

// Timeline handles GET /order/{id}/timeline.
func (h *Handler) Timeline(w http.ResponseWriter, r *http.Request) error {
	caller, err := callerFrom(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	rows, err := h.service.Timeline(r.Context(), caller, id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "order timeline retrieved", rows)
	return nil
}

// statusBody is the PUT /order/{id}/status payload.
type statusBody struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// SetStatus handles PUT /order/{id}/status.
func (h *Handler) SetStatus(w http.ResponseWriter, r *http.Request) error {
	caller, err := callerFrom(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	var body statusBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return err
	}

	detail, err := h.service.ChangeStatus(r.Context(), caller, id, body.Status, body.Reason)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "order status updated", detail)
	return nil
}

// Lifecycle handles GET /order/lifecycle.
//
// It publishes the state machine so the admin UI can label statuses and render
// a funnel without maintaining a second copy of the lifecycle in TypeScript --
// the duplication that let the old dashboard and the old report disagree about
// which statuses counted as a sale.
func (h *Handler) Lifecycle(w http.ResponseWriter, r *http.Request) error {
	type node struct {
		Status         string   `json:"status"`
		Next           []string `json:"next"`
		IsTerminal     bool     `json:"is_terminal"`
		ReasonRequired bool     `json:"reason_required"`
	}

	nodes := make([]node, 0, len(AllStatuses))
	for _, status := range AllStatuses {
		next := make([]string, 0, len(Transitions[status]))
		for _, candidate := range Transitions[status] {
			next = append(next, string(candidate))
		}
		nodes = append(nodes, node{
			Status:         string(status),
			Next:           next,
			IsTerminal:     len(next) == 0,
			ReasonRequired: ReasonRequired(status),
		})
	}

	types := make([]string, 0, len(AllOrderTypes))
	for _, t := range AllOrderTypes {
		types = append(types, string(t))
	}
	methods := make([]string, 0, len(AllPaymentMethods))
	for _, m := range AllPaymentMethods {
		methods = append(methods, string(m))
	}

	httpx.OK(w, r, "order lifecycle retrieved", map[string]any{
		"statuses":        nodes,
		"order_types":     types,
		"payment_methods": methods,
	})
	return nil
}

// MyList handles GET /customer/order.
func (h *Handler) MyList(w http.ResponseWriter, r *http.Request) error {
	caller, err := callerFrom(r)
	if err != nil {
		return err
	}

	filters, err := parseFilters(r)
	if err != nil {
		return err
	}

	page := httpx.ParsePagination(r)

	rows, total, err := h.service.MyOrders(r.Context(), caller.UserID, ListQuery{
		Filters: filters,
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "orders retrieved", rows, page.WithTotal(total))
	return nil
}

// MyGet handles GET /customer/order/{id}.
func (h *Handler) MyGet(w http.ResponseWriter, r *http.Request) error {
	caller, err := callerFrom(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	detail, err := h.service.MyOrder(r.Context(), caller.UserID, id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "order retrieved", detail)
	return nil
}

// MyTimeline handles GET /customer/order/{id}/timeline.
func (h *Handler) MyTimeline(w http.ResponseWriter, r *http.Request) error {
	caller, err := callerFrom(r)
	if err != nil {
		return err
	}

	id, err := httpx.PathUUID(r, "id")
	if err != nil {
		return err
	}

	rows, err := h.service.MyOrderTimeline(r.Context(), caller.UserID, id)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "order timeline retrieved", rows)
	return nil
}

// callerFrom lifts the acting user out of the request context.
func callerFrom(r *http.Request) (Caller, error) {
	identity, ok := auth.IdentityFrom(r.Context())
	if !ok {
		return Caller{}, httpx.Unauthorized("authentication required")
	}

	userID, err := uuid.Parse(identity.UserID)
	if err != nil {
		return Caller{}, httpx.Unauthorized("your session is not valid")
	}

	return Caller{UserID: userID, Role: identity.Role}, nil
}

// parseFilters reads the query string, reporting every problem together.
func parseFilters(r *http.Request) (Filters, error) {
	query := r.URL.Query()
	var fields []httpx.FieldError

	filters := Filters{Search: strings.TrimSpace(query.Get("search"))}

	if raw := strings.TrimSpace(query.Get("status")); raw != "" {
		if !IsKnownStatus(raw) {
			fields = append(fields, httpx.FieldError{Field: "status", Message: "is not a known order status"})
		} else {
			filters.Status = raw
		}
	}

	if raw := strings.TrimSpace(strings.ToUpper(query.Get("order_type"))); raw != "" {
		if !contains(AllOrderTypes, model.OrderType(raw)) {
			fields = append(fields, httpx.FieldError{Field: "order_type", Message: "must be one of: TAKE_AWAY, DELIVERY"})
		} else {
			filters.OrderType = raw
		}
	}

	if raw := strings.TrimSpace(strings.ToUpper(query.Get("payment_method"))); raw != "" {
		if !contains(AllPaymentMethods, model.PaymentMethod(raw)) {
			fields = append(fields, httpx.FieldError{Field: "payment_method", Message: "must be one of: CASH, QRIS, TRANSFER"})
		} else {
			filters.PaymentMethod = raw
		}
	}

	if raw := strings.TrimSpace(query.Get("shop_id")); raw != "" {
		shopID, err := uuid.Parse(raw)
		if err != nil {
			fields = append(fields, httpx.FieldError{Field: "shop_id", Message: "must be a valid UUID"})
		} else {
			filters.ShopID = shopID
		}
	}

	if raw := strings.TrimSpace(query.Get("customer_id")); raw != "" {
		customerID, err := uuid.Parse(raw)
		if err != nil {
			fields = append(fields, httpx.FieldError{Field: "customer_id", Message: "must be a valid UUID"})
		} else {
			filters.CustomerID = customerID
		}
	}

	filters.From = parseDay(query.Get("from"), "from", &fields)
	filters.To = parseDay(query.Get("to"), "to", &fields)

	if filters.From != nil && filters.To != nil && filters.To.Before(*filters.From) {
		fields = append(fields, httpx.FieldError{Field: "to", Message: "must not be earlier than from"})
	}

	if len(fields) > 0 {
		return Filters{}, httpx.Validation("request validation failed").WithDetails(fields)
	}
	return filters, nil
}

// parseDay reads an optional YYYY-MM-DD bound, appending a field error rather
// than returning one so several parsers can be merged into a single response.
func parseDay(raw, field string, fields *[]httpx.FieldError) *time.Time {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}

	// UTC rather than the server's local zone: the same request must select the
	// same rows regardless of where the process happens to run.
	day, err := time.ParseInLocation(dateLayout, trimmed, time.UTC)
	if err != nil {
		*fields = append(*fields, httpx.FieldError{Field: field, Message: "must be a date in YYYY-MM-DD format"})
		return nil
	}
	return &day
}

// contains reports whether needle is in haystack.
func contains[T comparable](haystack []T, needle T) bool {
	for _, candidate := range haystack {
		if candidate == needle {
			return true
		}
	}
	return false
}
