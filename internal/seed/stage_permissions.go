package seed

import (
	"context"
	"fmt"

	"service_nusantara/internal/model"
	"service_nusantara/internal/modules/role"
)

// seedPermissions reconciles the permission catalogue and grants the whole of
// it to the superadmin role.
//
// Migration 0003 tries to do the grant too, but on a fresh database it cannot:
// migrations run before the seeder, so `roles` is still empty and its
// INSERT ... SELECT WHERE name = 'superadmin' matches nothing -- silently,
// because of ON CONFLICT DO NOTHING. The result was a superadmin with zero
// permissions, which would lock the account out of everything the moment
// RequirePermission is actually wired to a route.
//
// Doing it here, after the roles exist, is the only ordering that works for
// both a fresh database and one that already had roles when 0003 ran.
func (s *Seeder) seedPermissions(ctx context.Context, _ Options) error {
	catalog := role.Catalog()

	// The catalogue is code's to define, so a definition that drifted from the
	// migration seed is corrected here rather than left to disagree.
	rows := make([]model.Permission, 0, len(catalog))
	for _, definition := range catalog {
		rows = append(rows, model.Permission{
			ID:    id("permission", definition.Code),
			Code:  definition.Code,
			Label: definition.Label,
			Group: definition.Group,
		})
	}

	if err := upsertPermissions(ctx, s, rows); err != nil {
		return err
	}

	// Grant everything to superadmin. ON CONFLICT keeps it idempotent, and the
	// join by name means it works whatever id the role was given.
	err := s.db.WithContext(ctx).Exec(`
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT r.id, p.id
		FROM roles r
		CROSS JOIN permissions p
		WHERE LOWER(r.name) = 'superadmin'
		ON CONFLICT DO NOTHING`).Error
	if err != nil {
		return fmt.Errorf("grant permissions to superadmin: %w", err)
	}

	return nil
}

// upsertPermissions writes the catalogue, matching on the natural key rather
// than the id: migration 0003 inserted these rows with ids of its own choosing,
// so upserting by id would create a second copy of every permission.
func upsertPermissions(ctx context.Context, s *Seeder, rows []model.Permission) error {
	for _, row := range rows {
		err := s.db.WithContext(ctx).Exec(`
			INSERT INTO permissions (id, code, label, permission_group)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (code) DO UPDATE
			SET label = EXCLUDED.label, permission_group = EXCLUDED.permission_group`,
			row.ID, row.Code, row.Label, row.Group).Error
		if err != nil {
			return fmt.Errorf("upsert permission %s: %w", row.Code, err)
		}
	}
	return nil
}
