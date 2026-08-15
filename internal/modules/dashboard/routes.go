package dashboard

import (
	"net/http"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/middleware"
)

// Register mounts the module.
//
// Two different guards, because the two halves answer to different people:
//
//   - The summary and the trend are figures about the shop, so they take
//     report_transaction.read -- whoever may see the transaction report may see
//     today's headline version of it.
//   - The anomaly queue names individual accounts and their behaviour, so it
//     takes user.read. A role trusted with sales totals is not automatically
//     trusted with a list of customers to investigate.
func Register(
	mux *http.ServeMux,
	prefix string,
	h *Handler,
	authenticate, rateLimit middleware.Middleware,
	requireReport, requireUser middleware.Middleware,
) {
	figures := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireReport(handler)))
	}
	accounts := func(handler httpx.Handler) http.Handler {
		return authenticate(rateLimit(requireUser(handler)))
	}

	mux.Handle("GET "+prefix+"/dashboard/summary", figures(h.Summary))
	mux.Handle("GET "+prefix+"/dashboard/trend", figures(h.Trend))
	mux.Handle("GET "+prefix+"/dashboard/anomalies", accounts(h.Anomalies))
}
