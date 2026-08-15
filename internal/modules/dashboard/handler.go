package dashboard

import (
	"net/http"
	"strconv"
	"strings"

	"service_nusantara/internal/httpx"
)

// Handler adapts HTTP to the Service.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

// Summary handles GET /dashboard/summary.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) error {
	summary, err := h.service.Summary(r.Context())
	if err != nil {
		return err
	}

	httpx.OK(w, r, "dashboard summary retrieved", summary)
	return nil
}

// Trend handles GET /dashboard/trend.
func (h *Handler) Trend(w http.ResponseWriter, r *http.Request) error {
	points, err := h.service.Trend(r.Context(), intParam(r, "days"))
	if err != nil {
		return err
	}

	httpx.OK(w, r, "sales trend retrieved", points)
	return nil
}

// Anomalies handles GET /dashboard/anomalies.
func (h *Handler) Anomalies(w http.ResponseWriter, r *http.Request) error {
	findings, err := h.service.Anomalies(r.Context(), intParam(r, "limit"))
	if err != nil {
		return err
	}

	httpx.OK(w, r, "anomalies retrieved", findings)
	return nil
}

// intParam reads an optional positive integer, leaving anything unparseable to
// the service's clamp rather than failing a dashboard over a query string.
func intParam(r *http.Request, name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get(name)))
	if err != nil {
		return 0
	}
	return value
}
