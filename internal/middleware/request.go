package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/logging"
)

const requestIDHeader = "X-Request-ID"

// RequestID assigns every request a correlation id, attaches a logger carrying
// it, and echoes the id back so a client bug report can be matched to a log
// line.
//
// The logger is passed in rather than taken from slog.Default(), so the chain
// has no hidden dependency on global state.
func RequestID(base *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(requestIDHeader)
			if id == "" || len(id) > 64 {
				// Reject an oversized client-supplied id rather than logging it.
				id = uuid.NewString()
			}

			w.Header().Set(requestIDHeader, id)

			ctx := logging.WithContext(r.Context(), base.With(slog.String("request_id", id)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// statusRecorder captures the status code for access logging.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Logger emits one structured line per request, using the request-scoped logger
// installed by RequestID. The previous service registered a LoggerMiddleware
// that called next() and logged nothing at all.
func Logger(trustProxyHeaders bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			logging.FromContext(r.Context()).Info("http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
				slog.String("ip", ClientIP(r, trustProxyHeaders)))
		})
	}
}

// Recover turns a panic into a 500 instead of killing the connection, and logs
// the panic value with the request id already attached by RequestID.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// http.ErrAbortHandler is a deliberate abort, not a bug.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logging.FromContext(r.Context()).Error("panic recovered",
					slog.Any("panic", rec),
					slog.String("path", r.URL.Path))
				httpx.WriteError(w, r, httpx.Internal("something went wrong"))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// BodyLimit caps the request body so a large upload cannot exhaust memory.
func BodyLimit(maxBytes int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP resolves the caller address. Proxy headers are honoured only when
// the deployment explicitly declares it sits behind a trusted proxy, because
// otherwise any client can forge the value the rate limiter keys on.
func ClientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if first, _, ok := strings.Cut(fwd, ","); ok {
				return strings.TrimSpace(first)
			}
			return strings.TrimSpace(fwd)
		}
		if real := r.Header.Get("X-Real-IP"); real != "" {
			return strings.TrimSpace(real)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
