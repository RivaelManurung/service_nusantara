package role

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// Service holds the business rules.
type Service struct {
	repo Repository
	log  *slog.Logger
}

func NewService(repo Repository, log *slog.Logger) *Service {
	return &Service{repo: repo, log: log}
}

// List returns one page of roles.
func (s *Service) List(ctx context.Context, query ListQuery) ([]Role, int64, error) {
	items, total, err := s.repo.List(ctx, query)
	if err != nil {
		return nil, 0, httpx.Internal("failed to load roles").WithCause(err)
	}
	return items, total, nil
}

// Get returns a single role.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Role, error) {
	item, err := s.repo.FindByID(ctx, id)
	if err != nil {
		return Role{}, s.translate(err, "failed to load role")
	}
	return item, nil
}

// Create adds a role. The name is unique, so a duplicate is reported as a
// conflict rather than left to the unique index to raise as a 500.
func (s *Service) Create(ctx context.Context, input Input) (Role, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Role{}, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "name", Message: "is required"}})
	}

	taken, err := s.repo.ExistsByName(ctx, name, uuid.Nil)
	if err != nil {
		return Role{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return Role{}, httpx.Conflict("a role with this name already exists")
	}

	created, err := s.repo.Create(ctx, Role{ID: uuid.New(), Name: name})
	if err != nil {
		return Role{}, httpx.Internal("failed to create role").WithCause(err)
	}
	return created, nil
}

// Update renames a role.
func (s *Service) Update(ctx context.Context, id uuid.UUID, input Input) (Role, error) {
	name := strings.TrimSpace(input.Name)
	if name == "" {
		return Role{}, httpx.Validation("request validation failed").
			WithDetails([]httpx.FieldError{{Field: "name", Message: "is required"}})
	}

	if _, err := s.repo.FindByID(ctx, id); err != nil {
		return Role{}, s.translate(err, "failed to load role")
	}

	taken, err := s.repo.ExistsByName(ctx, name, id)
	if err != nil {
		return Role{}, httpx.Internal("failed to verify name availability").WithCause(err)
	}
	if taken {
		return Role{}, httpx.Conflict("another role already uses this name")
	}

	updated, err := s.repo.Update(ctx, id, name)
	if err != nil {
		return Role{}, s.translate(err, "failed to update role")
	}
	return updated, nil
}

// Delete removes a role, refusing while accounts still reference it.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if _, err := s.repo.FindByID(ctx, id); err != nil {
		return s.translate(err, "failed to load role")
	}

	inUse, err := s.repo.InUse(ctx, id)
	if err != nil {
		return httpx.Internal("failed to check whether the role is in use").WithCause(err)
	}
	if inUse {
		// A foreign key violation would surface as a 500; this explains what to
		// do instead.
		return httpx.Conflict("this role is still assigned to users; reassign them first")
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return s.translate(err, "failed to delete role")
	}
	return nil
}

// translate turns a repository error into the right HTTP status.
func (s *Service) translate(err error, message string) error {
	if errors.Is(err, ErrNotFound) {
		return httpx.NotFound("role not found")
	}
	return httpx.Internal(message).WithCause(err)
}
