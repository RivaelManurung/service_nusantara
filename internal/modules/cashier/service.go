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

// Service holds the business rules.
type Service struct {
	repo      Repository
	images    uploader
	passwords hasher
	log       *slog.Logger
}

func NewService(repo Repository, images uploader, passwords hasher, log *slog.Logger) *Service {
	return &Service{repo: repo, images: images, passwords: passwords, log: log}
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

	photo, err := s.uploadImage(ctx, input.Image)
	if err != nil {
		return Cashier{}, err
	}

	created, err := s.repo.Create(ctx, Cashier{
		ID:       uuid.New(),
		Name:     input.Name,
		Username: input.Username,
		Email:    strings.ToLower(input.Email),
		Photo:    photo,
		Status:   normalizeStatus(input.Status),
	}, passwordHash, roleID)
	if err != nil {
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
	if _, err := s.repo.FindByID(ctx, id); err != nil {
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

	photo := ""
	if input.Image != nil {
		uploaded, err := s.uploadImage(ctx, input.Image)
		if err != nil {
			return Cashier{}, err
		}
		photo = uploaded
	}

	updated, err := s.repo.Update(ctx, id, name, username, photo, status)
	if err != nil {
		return Cashier{}, s.translate(err, "failed to update cashier")
	}
	return updated, nil
}

// Delete removes a cashier account.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete cashier")
	}
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

func (s *Service) uploadImage(ctx context.Context, file *multipart.FileHeader) (string, error) {
	if file == nil {
		return "", httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "image", Message: "is required"}})
	}

	uploaded, err := s.images.Upload(ctx, file, imageFolder)
	if err != nil {
		if errors.Is(err, storage.ErrNotConfigured) {
			return "", httpx.Unavailable("image uploads are not configured on this server").WithCause(err)
		}
		// A rejected file is the caller's problem; anything else is ours.
		return "", httpx.BadRequest(err.Error()).WithCause(err)
	}
	return uploaded.URL, nil
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
