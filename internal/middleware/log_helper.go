package middleware

import (
	"log/slog"
	"net/http"

	"service_nusantara/internal/platform/logging"
)

// logWarn records a non-fatal middleware problem against the request logger.
func logWarn(r *http.Request, msg string, err error) {
	logging.FromContext(r.Context()).Warn(msg,
		slog.String("error", err.Error()),
		slog.String("path", r.URL.Path))
}
