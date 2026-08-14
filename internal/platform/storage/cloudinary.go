package storage

import (
	"context"
	"fmt"
	"mime/multipart"
	"time"

	"github.com/cloudinary/cloudinary-go/v2"
	"github.com/cloudinary/cloudinary-go/v2/api/admin"
	"github.com/cloudinary/cloudinary-go/v2/api/uploader"
)

// Cloudinary uploads through the Cloudinary API.
type Cloudinary struct {
	client *cloudinary.Cloudinary
	// rootFolder namespaces every asset, so one account can serve several
	// environments without them overwriting each other.
	rootFolder string
	timeout    time.Duration
}

// CloudinaryConfig is the credential set, read from configuration.
type CloudinaryConfig struct {
	CloudName  string
	APIKey     string
	APISecret  string
	RootFolder string
	Timeout    time.Duration
}

// Configured reports whether all three credentials are present.
func (c CloudinaryConfig) Configured() bool {
	return c.CloudName != "" && c.APIKey != "" && c.APISecret != ""
}

// NewCloudinary builds the adapter. It returns an error rather than panicking,
// so a missing credential is a startup message and not a stack trace.
func NewCloudinary(cfg CloudinaryConfig) (*Cloudinary, error) {
	if !cfg.Configured() {
		return nil, ErrNotConfigured
	}

	client, err := cloudinary.NewFromParams(cfg.CloudName, cfg.APIKey, cfg.APISecret)
	if err != nil {
		return nil, fmt.Errorf("build cloudinary client: %w", err)
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &Cloudinary{client: client, rootFolder: cfg.RootFolder, timeout: cfg.Timeout}, nil
}

func (c *Cloudinary) Upload(ctx context.Context, file *multipart.FileHeader, folder string) (Uploaded, error) {
	if err := Validate(file); err != nil {
		return Uploaded{}, err
	}

	handle, err := file.Open()
	if err != nil {
		return Uploaded{}, fmt.Errorf("open upload: %w", err)
	}
	defer handle.Close()

	// A slow provider must not hold the request open for the client's whole
	// patience budget.
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	result, err := c.client.Upload.Upload(ctx, handle, uploader.UploadParams{
		Folder: c.folderPath(folder),
		// Let Cloudinary name the asset; a client-supplied filename would let
		// one upload overwrite another.
		UniqueFilename: boolPtr(true),
		ResourceType:   "image",
	})
	if err != nil {
		return Uploaded{}, fmt.Errorf("upload image: %w", err)
	}
	if result.SecureURL == "" {
		return Uploaded{}, fmt.Errorf("cloudinary returned no url: %s", result.Error.Message)
	}

	return Uploaded{URL: result.SecureURL, PublicID: result.PublicID}, nil
}

func (c *Cloudinary) Delete(ctx context.Context, publicID string) error {
	if publicID == "" {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	if _, err := c.client.Upload.Destroy(ctx, uploader.DestroyParams{PublicID: publicID}); err != nil {
		return fmt.Errorf("delete image: %w", err)
	}
	return nil
}

func (c *Cloudinary) folderPath(folder string) string {
	if c.rootFolder == "" {
		return folder
	}
	return c.rootFolder + "/" + folder
}

func boolPtr(v bool) *bool { return &v }

// Exists reports whether an asset with this public id is present.
//
// The backfill command uses it to prove a public id derived from a stored URL
// is real before writing it. Deriving without verifying would replace one
// unusable value (empty) with a worse one (a plausible id pointing at nothing),
// and a later delete would silently do nothing.
func (c *Cloudinary) Exists(ctx context.Context, publicID string) (bool, error) {
	if publicID == "" {
		return false, nil
	}

	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	result, err := c.client.Admin.Asset(ctx, admin.AssetParams{PublicID: publicID})
	if err != nil {
		return false, fmt.Errorf("look up %s: %w", publicID, err)
	}
	// The API answers 404 in the body rather than as a transport error.
	if result.Error.Message != "" {
		return false, nil
	}
	return result.PublicID != "", nil
}

// UploadFile stores a file from disk at an exact public id, replacing whatever
// was there.
//
// The seed assets need a deterministic handle so the fixture can compute their
// URLs, which the multipart Upload path deliberately does not allow: there a
// caller-chosen id would let one upload overwrite another's asset.
func (c *Cloudinary) UploadFile(ctx context.Context, path, publicID string) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	overwrite := true
	_, err := c.client.Upload.Upload(ctx, path, uploader.UploadParams{
		PublicID:     publicID,
		Overwrite:    &overwrite,
		ResourceType: "image",
		// The id already carries the folder, so Cloudinary must not add one.
		UseFilename: boolPtr(false),
	})
	if err != nil {
		return fmt.Errorf("upload %s: %w", path, err)
	}
	return nil
}
