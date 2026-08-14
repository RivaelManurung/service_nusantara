// Package httpx holds the transport-level helpers shared by every module:
// the response envelope, the error taxonomy, request decoding and pagination.
package httpx

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"service_nusantara/internal/platform/logging"
)

// Envelope keeps the exact field names the previous service emitted
// (status_code / message / data / error) so the existing mobile clients keep
// working against the rewritten backend.
type Envelope struct {
	StatusCode int         `json:"status_code"`
	Message    string      `json:"message"`
	Data       any         `json:"data,omitempty"`
	Error      any         `json:"error,omitempty"`
	Pagination *Pagination `json:"pagination,omitempty"`
}

// JSON writes an arbitrary payload with the given status code.
func JSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	if body == nil || status == http.StatusNoContent {
		return
	}

	if err := json.NewEncoder(w).Encode(body); err != nil {
		// The status line is already on the wire, so the only useful action is
		// to record that the client received a truncated body.
		logging.FromContext(r.Context()).Error("encode response body",
			slog.String("error", err.Error()))
	}
}

// OK writes a 200 success envelope.
func OK(w http.ResponseWriter, r *http.Request, message string, data any) {
	Success(w, r, http.StatusOK, message, data)
}

// Created writes a 201 success envelope.
func Created(w http.ResponseWriter, r *http.Request, message string, data any) {
	Success(w, r, http.StatusCreated, message, data)
}

// Success writes a success envelope with an explicit status code.
func Success(w http.ResponseWriter, r *http.Request, status int, message string, data any) {
	JSON(w, r, status, Envelope{StatusCode: status, Message: message, Data: data})
}

// Paginated writes a success envelope carrying page metadata.
func Paginated(w http.ResponseWriter, r *http.Request, message string, data any, p Pagination) {
	JSON(w, r, http.StatusOK, Envelope{
		StatusCode: http.StatusOK,
		Message:    message,
		Data:       data,
		Pagination: &p,
	})
}
