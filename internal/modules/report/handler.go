package report

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service: parse the query string, delegate, render.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Transactions handles GET /report/transactions.
func (h *Handler) Transactions(w http.ResponseWriter, r *http.Request) error {
	filters, err := parseFilters(r, true)
	if err != nil {
		return err
	}

	page := httpx.ParsePagination(r)

	rows, total, err := h.service.Transactions(r.Context(), TransactionQuery{
		Filters: filters,
		Page:    page.CurrentPage,
		PerPage: page.PerPage,
	})
	if err != nil {
		return err
	}

	httpx.Paginated(w, r, "transaction report retrieved", rows, page.WithTotal(total))
	return nil
}

// Summary handles GET /report/transactions/summary.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) error {
	// The status filter is deliberately not read here: the summary *is* the
	// per-status breakdown, so narrowing it to one status would leave the page
	// unable to show the figures it exists to show.
	filters, err := parseFilters(r, false)
	if err != nil {
		return err
	}

	summary, err := h.service.Summary(r.Context(), filters)
	if err != nil {
		return err
	}

	httpx.OK(w, r, "transaction summary retrieved", summary)
	return nil
}

// Financial handles GET /report/financial.
func (h *Handler) Financial(w http.ResponseWriter, r *http.Request) error {
	filters, err := parseFilters(r, false)
	if err != nil {
		return err
	}

	granularity, err := ParseGranularity(r.URL.Query().Get("granularity"))
	if err != nil {
		return err
	}

	report, err := h.service.Financial(r.Context(), FinancialQuery{
		Filters:     filters,
		Granularity: granularity,
	})
	if err != nil {
		return err
	}

	httpx.OK(w, r, "financial report retrieved", report)
	return nil
}

// TopProducts handles GET /report/financial/top-products.
func (h *Handler) TopProducts(w http.ResponseWriter, r *http.Request) error {
	filters, err := parseFilters(r, false)
	if err != nil {
		return err
	}

	rows, err := h.service.TopProducts(r.Context(), TopProductQuery{
		Filters: filters,
		Limit:   parseLimit(r.URL.Query().Get("limit")),
	})
	if err != nil {
		return err
	}

	httpx.OK(w, r, "top products retrieved", rows)
	return nil
}

// parseFilters reads the period and the optional narrowing filters.
//
// Every field problem is reported together, matching what httpx.Form does for
// multipart bodies: a screen with three filter controls should not have to
// discover its mistakes one request at a time.
func parseFilters(r *http.Request, withStatus bool) (Filters, error) {
	query := r.URL.Query()

	period, rangeErr := ParseRange(query.Get("from"), query.Get("to"))

	var fields []httpx.FieldError
	fields = append(fields, detailsOf(rangeErr)...)

	filters := Filters{Range: period}

	if withStatus {
		status, err := ParseStatus(query.Get("status"))
		fields = append(fields, detailsOf(err)...)
		filters.Status = status
	}

	method, err := ParsePaymentMethod(query.Get("payment_method"))
	fields = append(fields, detailsOf(err)...)
	filters.PaymentMethod = method

	if raw := strings.TrimSpace(query.Get("shop_id")); raw != "" {
		shopID, parseErr := uuid.Parse(raw)
		if parseErr != nil {
			fields = append(fields, httpx.FieldError{Field: "shop_id", Message: "must be a valid UUID"})
		} else {
			filters.ShopID = shopID
		}
	}

	if len(fields) > 0 {
		return Filters{}, httpx.Validation("request validation failed").WithDetails(fields)
	}

	return filters, nil
}

// detailsOf pulls the field errors out of a validation error so several parsers
// can be merged into one response.
func detailsOf(err error) []httpx.FieldError {
	if err == nil {
		return nil
	}

	var appErr *httpx.Error
	if !errors.As(err, &appErr) {
		return []httpx.FieldError{{Field: "query", Message: "is invalid"}}
	}

	fields, ok := appErr.Details.([]httpx.FieldError)
	if !ok {
		return []httpx.FieldError{{Field: "query", Message: appErr.Message}}
	}
	return fields
}

// parseLimit reads `limit`, leaving anything unparseable to the service's clamp.
func parseLimit(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultTopProducts
	}
	return value
}
