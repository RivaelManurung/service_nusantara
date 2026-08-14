package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"
)

// validate is safe for concurrent use and caches struct metadata, so one shared
// instance is both correct and faster than building one per request.
var validate = sync.OnceValue(func() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	// Report the JSON name the client actually sent rather than the Go field.
	v.RegisterTagNameFunc(func(f reflect.StructField) string {
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "-" || name == "" {
			return f.Name
		}
		return name
	})
	return v
})

// FieldError describes a single rejected field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// DecodeJSON reads the request body into dst and validates it.
//
// It replaces the hand-written `strings.TrimSpace(x) == ""` chains that were
// repeated in every handler of the previous service: rules now live on the DTO
// as struct tags, and every field problem is reported in one response.
func DecodeJSON(r *http.Request, dst any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" && !strings.HasPrefix(ct, "application/json") {
		return BadRequest("content-type must be application/json")
	}

	dec := json.NewDecoder(r.Body)
	// Unknown fields usually mean the client and server disagree about the
	// contract; failing loudly is cheaper than silently ignoring the value.
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}

	// A second value in the body would otherwise be silently discarded.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("request body must contain a single JSON object")
	}

	return Validate(dst)
}

// Validate runs struct-tag validation and converts failures into a 422 with a
// per-field breakdown.
func Validate(dst any) error {
	err := validate().Struct(dst)
	if err == nil {
		return nil
	}

	var invalid *validator.InvalidValidationError
	if errors.As(err, &invalid) {
		// A programming error (non-struct passed in), not a client mistake.
		return Internal("invalid validation target").WithCause(err)
	}

	var validationErrs validator.ValidationErrors
	if !errors.As(err, &validationErrs) {
		return Internal("validation failed").WithCause(err)
	}

	fields := make([]FieldError, 0, len(validationErrs))
	for _, fe := range validationErrs {
		fields = append(fields, FieldError{Field: fe.Field(), Message: describe(fe)})
	}

	return Validation("request validation failed").WithDetails(fields)
}

func decodeError(err error) error {
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		return BadRequest(fmt.Sprintf("malformed JSON at byte %d", syntaxErr.Offset)).WithCause(err)
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		return BadRequest(fmt.Sprintf("field %q expects a %s value", typeErr.Field, typeErr.Type)).WithCause(err)
	}

	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return PayloadTooLarge("request body is too large").WithCause(err)
	}

	if errors.Is(err, io.EOF) {
		return BadRequest("request body must not be empty").WithCause(err)
	}

	if strings.HasPrefix(err.Error(), "json: unknown field ") {
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return BadRequest(fmt.Sprintf("unknown field %s", field)).WithCause(err)
	}

	return BadRequest("request body could not be parsed").WithCause(err)
}

// describe turns a validator tag into a message a mobile client can display.
func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email address"
	case "uuid", "uuid4":
		return "must be a valid UUID"
	case "min":
		return fmt.Sprintf("must be at least %s characters or %s in value", fe.Param(), fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s characters or %s in value", fe.Param(), fe.Param())
	case "gte":
		return fmt.Sprintf("must be greater than or equal to %s", fe.Param())
	case "lte":
		return fmt.Sprintf("must be less than or equal to %s", fe.Param())
	case "eqfield":
		return fmt.Sprintf("must match %s", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of: %s", strings.ReplaceAll(fe.Param(), " ", ", "))
	case "e164":
		return "must be a phone number in E.164 format, for example +6281234567890"
	default:
		return fmt.Sprintf("failed the %q rule", fe.Tag())
	}
}
