package httpx

import (
	"errors"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// maxMultipartMemory is how much of an upload is buffered in RAM before the
// rest spills to a temporary file.
const maxMultipartMemory = 8 << 20

// Form wraps a parsed multipart request and collects field errors, so a handler
// reads every value first and reports all the problems at once instead of
// failing on the first one.
type Form struct {
	request *http.Request
	errs    []FieldError
}

// ParseMultipart reads a multipart body. The catalogue endpoints use multipart
// rather than JSON because they carry an image alongside their fields.
func ParseMultipart(r *http.Request) (*Form, error) {
	if err := r.ParseMultipartForm(maxMultipartMemory); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return nil, PayloadTooLarge("request body is too large").WithCause(err)
		}
		return nil, BadRequest("request must be multipart/form-data").WithCause(err)
	}
	return &Form{request: r}, nil
}

func (f *Form) fail(field, message string) {
	f.errs = append(f.errs, FieldError{Field: field, Message: message})
}

// Err returns a 422 describing every rejected field, or nil when all were fine.
func (f *Form) Err() error {
	if len(f.errs) == 0 {
		return nil
	}
	return Validation("request validation failed").WithDetails(f.errs)
}

// String reads a trimmed optional value.
func (f *Form) String(field string) string {
	return strings.TrimSpace(f.request.FormValue(field))
}

// Required reads a value that must be present and non-empty.
func (f *Form) Required(field string) string {
	value := f.String(field)
	if value == "" {
		f.fail(field, "is required")
	}
	return value
}

// RequiredMax reads a required value bounded by length.
func (f *Form) RequiredMax(field string, max int) string {
	value := f.Required(field)
	if len(value) > max {
		f.fail(field, "is longer than "+strconv.Itoa(max)+" characters")
	}
	return value
}

// Int reads an optional integer, falling back when absent.
func (f *Form) Int(field string, fallback int) int {
	raw := f.String(field)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		f.fail(field, "must be a whole number")
		return fallback
	}
	return value
}

// Float reads an optional decimal, falling back when absent.
func (f *Form) Float(field string, fallback float64) float64 {
	raw := f.String(field)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		f.fail(field, "must be a number")
		return fallback
	}
	return value
}

// Bool reads a checkbox-style value.
func (f *Form) Bool(field string) bool {
	value, err := strconv.ParseBool(f.String(field))
	return err == nil && value
}

// UUID reads a required identifier.
func (f *Form) UUID(field string) uuid.UUID {
	raw := f.Required(field)
	if raw == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		f.fail(field, "must be a valid UUID")
		return uuid.Nil
	}
	return id
}

// File reads an optional upload. A missing file is not an error; the caller
// decides whether it was required.
func (f *Form) File(field string) *multipart.FileHeader {
	if f.request.MultipartForm == nil {
		return nil
	}
	files := f.request.MultipartForm.File[field]
	if len(files) == 0 {
		return nil
	}
	return files[0]
}

// Files reads every upload sent under one field, for galleries.
func (f *Form) Files(field string) []*multipart.FileHeader {
	if f.request.MultipartForm == nil {
		return nil
	}
	return f.request.MultipartForm.File[field]
}

// RequiredFile reads an upload that must be present.
func (f *Form) RequiredFile(field string) *multipart.FileHeader {
	file := f.File(field)
	if file == nil {
		f.fail(field, "is required")
	}
	return file
}

// PathUUID reads an identifier out of the route pattern.
func PathUUID(r *http.Request, name string) (uuid.UUID, error) {
	raw := r.PathValue(name)
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, BadRequest("the id in the URL is not a valid UUID").WithCause(err)
	}
	return id, nil
}
