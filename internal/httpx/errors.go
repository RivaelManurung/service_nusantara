package httpx

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"service_nusantara/internal/platform/logging"
)

// Error is the only error type handlers are expected to return upward. It
// separates what the client is told (Code/Message/Details) from what actually
// went wrong (cause), so internal failures never leak driver or SQL text the
// way the previous service's `err.Error()` responses did.
type Error struct {
	Status  int
	Code    string
	Message string
	Details any
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.cause)
	}
	return e.Message
}

// Unwrap exposes the cause to errors.Is/errors.As without exposing it to clients.
func (e *Error) Unwrap() error { return e.cause }

// WithCause attaches the underlying failure for logging purposes.
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

// WithDetails attaches a client-safe payload, typically field validation errors.
func (e *Error) WithDetails(details any) *Error {
	e.Details = details
	return e
}

// Machine-readable codes; clients switch on these rather than on message text.
const (
	CodeBadRequest   = "BAD_REQUEST"
	CodeValidation   = "VALIDATION_ERROR"
	CodeUnauthorized = "UNAUTHORIZED"
	CodeForbidden    = "FORBIDDEN"
	CodeNotFound     = "NOT_FOUND"
	CodeConflict     = "CONFLICT"
	CodeRateLimited  = "RATE_LIMITED"
	CodePayloadLarge = "PAYLOAD_TOO_LARGE"
	CodeInternal     = "INTERNAL_ERROR"
	CodeUnavailable  = "SERVICE_UNAVAILABLE"
)

func newError(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func BadRequest(message string) *Error {
	return newError(http.StatusBadRequest, CodeBadRequest, message)
}

func Validation(message string) *Error {
	return newError(http.StatusUnprocessableEntity, CodeValidation, message)
}

func Unauthorized(message string) *Error {
	return newError(http.StatusUnauthorized, CodeUnauthorized, message)
}

func Forbidden(message string) *Error {
	return newError(http.StatusForbidden, CodeForbidden, message)
}

func NotFound(message string) *Error {
	return newError(http.StatusNotFound, CodeNotFound, message)
}

func Conflict(message string) *Error {
	return newError(http.StatusConflict, CodeConflict, message)
}

func RateLimited(message string) *Error {
	return newError(http.StatusTooManyRequests, CodeRateLimited, message)
}

func PayloadTooLarge(message string) *Error {
	return newError(http.StatusRequestEntityTooLarge, CodePayloadLarge, message)
}

func Internal(message string) *Error {
	return newError(http.StatusInternalServerError, CodeInternal, message)
}

func Unavailable(message string) *Error {
	return newError(http.StatusServiceUnavailable, CodeUnavailable, message)
}

// errorBody is the client-facing shape of the envelope's `error` field.
type errorBody struct {
	Code    string `json:"code"`
	Details any    `json:"details,omitempty"`
}

// WriteError renders err as a JSON envelope. Anything that is not an *Error is
// treated as an unexpected internal failure: it is logged in full and reported
// to the client as a generic 500.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	if err == nil {
		return
	}

	log := logging.FromContext(r.Context())

	var appErr *Error
	if !errors.As(err, &appErr) {
		log.Error("unhandled error",
			slog.String("error", err.Error()),
			slog.String("path", r.URL.Path),
			slog.String("method", r.Method))
		appErr = Internal("something went wrong")
	} else if appErr.Status >= http.StatusInternalServerError {
		log.Error("server error",
			slog.String("code", appErr.Code),
			slog.String("error", appErr.Error()),
			slog.String("path", r.URL.Path))
	} else {
		log.Debug("client error",
			slog.String("code", appErr.Code),
			slog.String("error", appErr.Error()),
			slog.String("path", r.URL.Path))
	}

	JSON(w, r, appErr.Status, Envelope{
		StatusCode: appErr.Status,
		Message:    appErr.Message,
		Error:      errorBody{Code: appErr.Code, Details: appErr.Details},
	})
}

// Handler is a http.HandlerFunc that may return an error, letting modules use
// `return httpx.NotFound(...)` instead of remembering to write and return.
type Handler func(http.ResponseWriter, *http.Request) error

// ServeHTTP funnels every returned error through WriteError.
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := h(w, r); err != nil {
		WriteError(w, r, err)
	}
}
