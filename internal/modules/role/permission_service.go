package role

import (
	"context"
	"log/slog"
	"strings"

	"github.com/google/uuid"

	"service_nusantara/internal/httpx"
)

// PermissionService holds the rules for reading the catalogue and assigning it.
//
// It is a second service rather than more methods on Service so the existing
// constructor keeps its signature: the server wiring adds two lines instead of
// editing one, and the CRUD tests keep their two-argument fake.
type PermissionService struct {
	roles Repository
	perms PermissionRepository
	log   *slog.Logger
}

func NewPermissionService(roles Repository, perms PermissionRepository, log *slog.Logger) *PermissionService {
	return &PermissionService{roles: roles, perms: perms, log: log}
}

// Catalog returns every permission that exists.
func (s *PermissionService) Catalog(ctx context.Context) ([]Permission, error) {
	items, err := s.perms.ListPermissions(ctx)
	if err != nil {
		return nil, httpx.Internal("failed to load permissions").WithCause(err)
	}
	return items, nil
}

// ForRole returns the permissions granted to one role.
func (s *PermissionService) ForRole(ctx context.Context, roleID uuid.UUID) (RolePermissions, error) {
	target, err := s.roles.FindByID(ctx, roleID)
	if err != nil {
		return RolePermissions{}, translate(err, "failed to load role")
	}

	codes, err := s.perms.CodesForRoleID(ctx, roleID)
	if err != nil {
		return RolePermissions{}, httpx.Internal("failed to load role permissions").WithCause(err)
	}

	return RolePermissions{RoleID: target.ID, RoleName: target.Name, Codes: codes}, nil
}

// Replace swaps a role's permissions for exactly the submitted set.
//
// A replace rather than add/remove endpoints: the UI submits the whole matrix,
// and two operators editing at once then last-write-wins on the whole role
// instead of interleaving into a set neither of them chose.
func (s *PermissionService) Replace(ctx context.Context, roleID uuid.UUID, codes []string) (RolePermissions, error) {
	target, err := s.roles.FindByID(ctx, roleID)
	if err != nil {
		return RolePermissions{}, translate(err, "failed to load role")
	}

	wanted, err := normaliseCodes(codes)
	if err != nil {
		return RolePermissions{}, err
	}

	if err := guardSuperAdmin(target.Name, wanted); err != nil {
		return RolePermissions{}, err
	}

	if err := s.perms.ReplaceForRole(ctx, roleID, wanted); err != nil {
		return RolePermissions{}, httpx.Internal("failed to update role permissions").WithCause(err)
	}

	s.log.Info("role permissions replaced",
		slog.String("role", target.Name),
		slog.Int("granted", len(wanted)))

	return RolePermissions{RoleID: target.ID, RoleName: target.Name, Codes: wanted}, nil
}

// normaliseCodes trims, de-duplicates and validates the submitted list against
// the catalogue, so a typo is a 422 naming the offending code rather than a
// grant that silently does nothing.
func normaliseCodes(codes []string) ([]string, error) {
	known := KnownCodes()
	seen := make(map[string]struct{}, len(codes))
	var unknown []httpx.FieldError

	for _, raw := range codes {
		code := strings.TrimSpace(raw)
		if code == "" {
			continue
		}
		if _, ok := known[code]; !ok {
			unknown = append(unknown, httpx.FieldError{
				Field:   "permission_codes",
				Message: "unknown permission: " + code,
			})
			continue
		}
		seen[code] = struct{}{}
	}

	if len(unknown) > 0 {
		return nil, httpx.Validation("request validation failed").WithDetails(unknown)
	}

	// Catalogue order, so the response and the stored set read the same way
	// every time rather than in map iteration order.
	out := make([]string, 0, len(seen))
	for _, code := range AllCodes() {
		if _, ok := seen[code]; ok {
			out = append(out, code)
		}
	}
	return out, nil
}

// guardSuperAdmin refuses to weaken the root role.
//
// The permission endpoints themselves require superadmin, so a superadmin left
// short of the catalogue could no longer restore what it gave away -- there is
// no other door back in.
func guardSuperAdmin(roleName string, wanted []string) error {
	if !strings.EqualFold(strings.TrimSpace(roleName), SuperAdminRoleName) {
		return nil
	}

	if len(wanted) != len(AllCodes()) {
		return httpx.Conflict("the superadmin role must keep every permission")
	}
	return nil
}
