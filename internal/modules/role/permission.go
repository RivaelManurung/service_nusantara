package role

import (
	"context"

	"github.com/google/uuid"
)

// SuperAdminRoleName is the role the whole authorisation scheme bottoms out in.
//
// It is compared case-insensitively, and it is the one role the service refuses
// to leave without permissions: the endpoints that edit permissions themselves
// require it, so an operator who stripped it would lock every account out of
// the only screen that could put it back.
const SuperAdminRoleName = "superadmin"

// Permission is the response shape for one catalogue entry.
//
// Every key is snake_case on the wire, matching web_nusantara's
// src/features/role/types.ts.
type Permission struct {
	ID    uuid.UUID `json:"id"`
	Code  string    `json:"code"`
	Label string    `json:"label"`
	Group string    `json:"group"`
}

// RolePermissions is the response shape for a role's assignment.
type RolePermissions struct {
	RoleID   uuid.UUID `json:"role_id"`
	RoleName string    `json:"role_name"`
	// Codes rather than ids: the client's checkboxes are keyed by code, and a
	// code is stable across environments where the generated ids are not.
	Codes []string `json:"permission_codes"`
}

// PermissionRepository is the persistence port for the catalogue and the join.
type PermissionRepository interface {
	// ListPermissions returns the whole catalogue; it is a couple of dozen rows
	// that change only on deploy, so it is never paginated.
	ListPermissions(ctx context.Context) ([]Permission, error)
	// CodesForRoleID returns the codes granted to one role.
	CodesForRoleID(ctx context.Context, roleID uuid.UUID) ([]string, error)
	// ReplaceForRole swaps a role's grants for exactly this set, in one
	// transaction: a half-applied change would leave a role with a mixture of
	// the old and the new answer.
	ReplaceForRole(ctx context.Context, roleID uuid.UUID, codes []string) error
	// PermissionsFor resolves a role NAME to its permission set, which is what
	// the middleware has on hand -- the JWT carries the name, not the id.
	PermissionsFor(ctx context.Context, roleName string) (map[string]struct{}, error)
}
