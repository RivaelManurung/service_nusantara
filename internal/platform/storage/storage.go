// Package storage uploads images and hands back the URL to persist.
package storage

import (
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"path/filepath"
	"strings"
)

// ErrNotConfigured means no image backend was set up, so upload endpoints must
// refuse rather than silently store nothing.
var ErrNotConfigured = errors.New("image storage is not configured")

// Uploaded is what the caller persists: the public URL plus the provider's own
// handle, which is the only thing that can delete the asset later.
type Uploaded struct {
	URL      string
	PublicID string
}

// Uploader stores and removes images.
//
// It is an interface so a module's tests never touch the network, and so the
// provider is a deployment decision rather than something baked into every use
// case -- which is what the previous service did by passing a concrete
// *CloudinaryService into each one.
type Uploader interface {
	Upload(ctx context.Context, file *multipart.FileHeader, folder string) (Uploaded, error)
	Delete(ctx context.Context, publicID string) error
}

// maxImageBytes bounds an upload before it leaves the process.
const maxImageBytes = 5 << 20

var allowedExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// Validate checks an uploaded file before any provider call.
//
// Doing this here rather than per handler means a new endpoint cannot forget
// it, and a rejected file costs no bandwidth to the provider.
func Validate(file *multipart.FileHeader) error {
	if file == nil {
		return errors.New("no file provided")
	}
	if file.Size <= 0 {
		return errors.New("file is empty")
	}
	if file.Size > maxImageBytes {
		return fmt.Errorf("file is larger than %d MB", maxImageBytes>>20)
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	if !allowedExtensions[ext] {
		return fmt.Errorf("unsupported image type %q; use jpg, png or webp", ext)
	}
	return nil
}

// Disabled is the no-op backend used when no provider is configured. It fails
// loudly on upload so a misconfigured deployment cannot look like it works.
type Disabled struct{}

func (Disabled) Upload(context.Context, *multipart.FileHeader, string) (Uploaded, error) {
	return Uploaded{}, ErrNotConfigured
}

// Delete succeeds: there is nothing stored to remove, and failing here would
// block deleting a record whose image was never uploaded.
func (Disabled) Delete(context.Context, string) error { return nil }
