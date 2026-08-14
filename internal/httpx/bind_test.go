package httpx_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"service_nusantara/internal/httpx"
)

type loginPayload struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required,min=8"`
}

func jsonRequest(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestDecodeJSONAcceptsAValidBody(t *testing.T) {
	var payload loginPayload

	err := httpx.DecodeJSON(jsonRequest(`{"email":"a@b.com","password":"supersecret"}`), &payload)

	require.NoError(t, err)
	assert.Equal(t, "a@b.com", payload.Email)
}

func TestDecodeJSONReportsEveryInvalidFieldAtOnce(t *testing.T) {
	// One round trip should tell the client everything that is wrong.
	var payload loginPayload

	err := httpx.DecodeJSON(jsonRequest(`{"email":"not-an-email","password":"short"}`), &payload)

	var appErr *httpx.Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusUnprocessableEntity, appErr.Status)
	assert.Equal(t, httpx.CodeValidation, appErr.Code)

	fields, ok := appErr.Details.([]httpx.FieldError)
	require.True(t, ok)
	assert.Len(t, fields, 2)
}

func TestDecodeJSONNamesTheJSONFieldNotTheGoField(t *testing.T) {
	var payload loginPayload

	err := httpx.DecodeJSON(jsonRequest(`{"email":"","password":"supersecret"}`), &payload)

	var appErr *httpx.Error
	require.True(t, errors.As(err, &appErr))
	fields := appErr.Details.([]httpx.FieldError)
	assert.Equal(t, "email", fields[0].Field)
}

func TestDecodeJSONRejectsAnEmptyBody(t *testing.T) {
	var payload loginPayload

	err := httpx.DecodeJSON(jsonRequest(``), &payload)

	var appErr *httpx.Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusBadRequest, appErr.Status)
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	// Silently dropping an unexpected field hides client/server contract drift.
	var payload loginPayload

	err := httpx.DecodeJSON(jsonRequest(`{"email":"a@b.com","password":"supersecret","admin":true}`), &payload)

	var appErr *httpx.Error
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, http.StatusBadRequest, appErr.Status)
}

func TestDecodeJSONRejectsTrailingGarbage(t *testing.T) {
	var payload loginPayload

	err := httpx.DecodeJSON(jsonRequest(`{"email":"a@b.com","password":"supersecret"}{"x":1}`), &payload)

	assert.Error(t, err)
}

func TestDecodeJSONRejectsNonJSONContentType(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/auth/login", strings.NewReader(`email=a@b.com`))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var payload loginPayload
	err := httpx.DecodeJSON(r, &payload)

	assert.Error(t, err)
}

func TestWriteErrorHidesTheCauseOfInternalFailures(t *testing.T) {
	// A driver message must never reach the client.
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/anything", nil)

	httpx.WriteError(rec, r, errors.New(`pq: password authentication failed for user "admin"`))

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "password authentication failed")

	var envelope httpx.Envelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	assert.Equal(t, "something went wrong", envelope.Message)
}

func TestWriteErrorPreservesClientFacingErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/anything", nil)

	httpx.WriteError(rec, r, httpx.NotFound("account no longer exists").
		WithCause(errors.New("sql: no rows in result set")))

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Contains(t, rec.Body.String(), "account no longer exists")
	assert.NotContains(t, rec.Body.String(), "no rows in result set")
}
