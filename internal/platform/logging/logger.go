// Package logging builds the single *slog.Logger the process uses.
//
// Structured logging replaces the ad-hoc log.Printf calls of the previous
// service: every line carries the request id, so a report from the mobile app
// can be traced end to end.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// contextKey is unexported so no other package can collide with our key.
type contextKey struct{}

var loggerKey contextKey

// New returns a logger writing to stdout in the requested format.
func New(level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var handler slog.Handler
	if strings.EqualFold(format, "text") {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// WithContext stores a request-scoped logger for later retrieval.
func WithContext(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// FromContext returns the request-scoped logger, or the default logger when the
// context carries none. It never returns nil, so callers need no nil checks.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok && l != nil {
		return l
	}
	return slog.Default()
}
