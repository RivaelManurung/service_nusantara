package cashier

import (
	"context"
	"errors"
	"log/slog"
	"mime/multipart"
	"net/mail"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
	"service_nusantara/internal/platform/storage"
)

// imageFolder groups this module's uploads in the storage provider.
const imageFolder = "cashiers"

// Password bounds. The upper bound is bcrypt's: beyond 72 bytes the input is
// silently truncated, which would make a long passphrase weaker than it looks.
const (
	minPasswordLength = 8
	maxPasswordLength = 72
)

// uploader is the slice of the storage port this module needs.
type uploader interface {
	Upload(ctx context.Context, file *multipart.FileHeader, folder string) (storage.Uploaded, error)
}

// hasher is the slice of internal/auth.Hasher this module needs. Depending on
// the port rather than on bcrypt directly keeps the cost a configuration
// decision made once, in the server wiring.
type hasher interface {
	Hash(password string) (string, error)
}

// reaper removes assets a record no longer points at. It is an interface so a
// test can assert that a replaced photo is actually cleaned up.
type reaper interface {
	Discard(ctx context.Context, publicID string)
}

// Service holds the business rules.
type Service struct {
	repo      Repository
	images    uploader
	reaper    reaper
	passwords hasher
	log       *slog.Logger
}

// NewService takes the reaper directly after the uploader it complements, which
// is where typeproduct and banner also put it.
func NewService(repo Repository, images uploader, reaper reaper, passwords hasher, log *slog.Logger) *Service {
	return &Service{repo: repo, images: images, reaper: reaper, passwords: passwords, log: log}
}

// List returns one page of cashier accounts.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Cashier, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load cashiers").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single cashier.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Cashier, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Cashier{}, s.translate(err, "failed to load cashier")
	}
	return item, nil
}

// Create provisions a cashier account.
//
// Order matters: validate, then check uniqueness, then hash, then upload, then
// insert. The upload comes before the insert so a storage failure cannot leave
// an account pointing at a photo that was never stored, and the password is
// hashed before it is ever handed to the repository -- nothing below this line
// sees the plaintext.
func (s *Service) Create(ctx context.Context, input CreateInput) (Cashier, error) {
	if err := validateCredentials(input.Email, input.Password); err != nil {
		return Cashier{}, err
	}

	roleID, err := s.repo.FindRoleIDByName(ctx, RoleName)
	if err != nil {
		if errors.Is(err, ErrRoleMissing) {
			return Cashier{}, httpx.Unavailable("the cashier role is not configured on this server").WithCause(err)
		}
		return Cashier{}, httpx.Internal("failed to resolve the cashier role").WithCause(err)
	}

	if err := s.assertAvailable(ctx, input.Username, input.Email, uuid.Nil); err != nil {
		return Cashier{}, err
	}

	passwordHash, err := s.passwords.Hash(input.Password)
	if err != nil {
		return Cashier{}, httpx.Internal("failed to secure the password").WithCause(err)
	}

	uploaded, err := s.uploadImage(ctx, input.Image)
	if err != nil {
		return Cashier{}, err
	}

	created, err := s.repo.Create(ctx, Cashier{
		ID:            uuid.New(),
		Name:          input.Name,
		Username:      input.Username,
		Email:         strings.ToLower(input.Email),
		Photo:         uploaded.URL,
		PhotoPublicID: uploaded.PublicID,
		Status:        normalizeStatus(input.Status),
	}, passwordHash, roleID)
	if err != nil {
		// The upload already happened, so a failed insert would strand it.
		s.reaper.Discard(ctx, uploaded.PublicID)
		return Cashier{}, httpx.Internal("failed to create cashier").WithCause(err)
	}
	return created, nil
}

// Update edits a cashier.
//
// The status toggle arrives here too: the legacy service had no /edit-status
// route for cashiers, so the web client posts `status` to this same endpoint.
// Every field is therefore optional, and an absent one is left untouched.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input UpdateInput) (Cashier, error) {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Cashier{}, s.translate(err, "failed to load cashier")
	}

	username := ""
	if input.Username != nil {
		username = *input.Username
		taken, err := s.repo.ExistsByUsername(ctx, username, id)
		if err != nil {
			return Cashier{}, httpx.Internal("failed to verify username availability").WithCause(err)
		}
		if taken {
			return Cashier{}, httpx.Conflict("another account already uses this username")
		}
	}

	name := ""
	if input.Name != nil {
		name = *input.Name
	}

	var status *int
	if input.Status != nil {
		normalized := normalizeStatus(*input.Status)
		status = &normalized
	}

	var uploaded storage.Uploaded
	if input.Image != nil {
		if uploaded, err = s.uploadImage(ctx, input.Image); err != nil {
			return Cashier{}, err
		}
	}

	updated, err := s.repo.Update(ctx, id, name, username, uploaded.URL, uploaded.PublicID, status)
	if err != nil {
		// The row still points at the old photo, so the new upload is stranded.
		s.reaper.Discard(ctx, uploaded.PublicID)
		return Cashier{}, s.translate(err, "failed to update cashier")
	}

	// Only once the row commits to the new photo is the old one safe to drop.
	if uploaded.PublicID != "" {
		s.reaper.Discard(ctx, existing.PhotoPublicID)
	}
	return updated, nil
}

// Delete removes a cashier account.
//
// The row is read before it is removed, because afterwards there is nothing
// left to learn the storage handle from.
//
// Deleting a cashier soft-deletes its `users` row, so the record technically
// survives with a photo URL this call is about to invalidate. The asset is
// still discarded: nothing in this codebase ever un-deletes a user (no
// Unscoped read, no restore endpoint), so a retained image would be an
// unreachable file kept forever on the off chance of a manual SQL revival --
// which is precisely the leak this change exists to stop. A restore would have
// to re-upload the photo, exactly as it would have to re-check the now-freed
// username and email uniqueness.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	existing, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return s.translate(err, "failed to load cashier")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete cashier")
	}

	s.reaper.Discard(ctx, existing.PhotoPublicID)
	return nil
}

// assertAvailable rejects a username or email already in use anywhere in the
// users table, because both unique indexes are global.
func (s *Service) assertAvailable(ctx context.Context, username, email string, excludeID uuid.UUID) error {
	taken, err := s.repo.ExistsByUsername(ctx, username, excludeID)
	if err != nil {
		return httpx.Internal("failed to verify username availability").WithCause(err)
	}
	if taken {
		return httpx.Conflict("an account with this username already exists")
	}

	taken, err = s.repo.ExistsByEmail(ctx, email, excludeID)
	if err != nil {
		return httpx.Internal("failed to verify email availability").WithCause(err)
	}
	if taken {
		return httpx.Conflict("an account with this email already exists")
	}
	return nil
}

func (s *Service) uploadImage(ctx context.Context, file *multipart.FileHeader) (storage.Uploaded, error) {
	if file == nil {
		return storage.Uploaded{}, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "image", Message: "is required"}})
	}

	uploaded, err := s.images.Upload(ctx, file, imageFolder)
	if err != nil {
		if errors.Is(err, storage.ErrNotConfigured) {
			return storage.Uploaded{}, httpx.Unavailable("image uploads are not configured on this server").WithCause(err)
		}
		// A rejected file is the caller's problem; anything else is ours.
		return storage.Uploaded{}, httpx.BadRequest(err.Error()).WithCause(err)
	}
	return uploaded, nil
}

// validateCredentials enforces what the multipart form cannot: a parseable
// address and a password long enough to be worth hashing.
func validateCredentials(email, password string) error {
	var fields []httpx.FieldError

	if _, err := mail.ParseAddress(email); err != nil {
		fields = append(fields, httpx.FieldError{Field: "email", Message: "must be a valid email address"})
	}
	switch {
	case len(password) < minPasswordLength:
		fields = append(fields, httpx.FieldError{
			Field: "password", Message: "must be at least 8 characters",
		})
	case len(password) > maxPasswordLength:
		fields = append(fields, httpx.FieldError{
			Field: "password", Message: "must not exceed 72 characters",
		})
	}

	if len(fields) == 0 {
		return nil
	}
	return httpx.Validation("request validation failed").WithDetails(fields)
}

// translate turns a repository error into the right HTTP status.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("cashier not found")
	}
	return httpx.Internal(message).WithCause(err)
}

// normalizeStatus collapses anything that is not "active" to inactive, so an
// out-of-range integer cannot reach the database.
func normalizeStatus(status int) int {
	if status == StatusActive {
		return StatusActive
	}
	return StatusInactive
}
