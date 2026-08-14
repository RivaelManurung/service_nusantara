package role

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"service_nusantara/internal/model"
)

// GormPermissionRepository is the PostgreSQL implementation of
// PermissionRepository.
type GormPermissionRepository struct {
	db *gorm.DB
}

func NewGormPermissionRepository(db *gorm.DB) *GormPermissionRepository {
	return &GormPermissionRepository{db: db}
}

func (r *GormPermissionRepository) ListPermissions(ctx context.Context) ([]Permission, error) {
	var rows []model.Permission
	// Ordering by group then code keeps the admin matrix stable between loads;
	// the client groups on `group` and would otherwise reshuffle its sections.
	err := r.db.WithContext(ctx).Model(&model.Permission{}).
		Order("permission_group ASC, code ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("list permissions: %w", err)
	}

	items := make([]Permission, 0, len(rows))
	for _, row := range rows {
		items = append(items, Permission{
			ID:    row.ID,
			Code:  row.Code,
			Label: row.Label,
			Group: row.Group,
		})
	}
	return items, nil
}

func (r *GormPermissionRepository) CodesForRoleID(ctx context.Context, roleID uuid.UUID) ([]string, error) {
	codes := make([]string, 0)
	err := r.db.WithContext(ctx).
		Raw(`SELECT p.code
		     FROM permissions p
		     JOIN role_permissions rp ON rp.permission_id = p.id
		     WHERE rp.role_id = ?
		     ORDER BY p.permission_group ASC, p.code ASC`, roleID).
		Scan(&codes).Error
	if err != nil {
		return nil, fmt.Errorf("read role permissions: %w", err)
	}
	return codes, nil
}

func (r *GormPermissionRepository) ReplaceForRole(ctx context.Context, roleID uuid.UUID, codes []string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("role_id = ?", roleID).
			Delete(&model.RolePermission{}).Error; err != nil {
			return fmt.Errorf("clear role permissions: %w", err)
		}

		if len(codes) == 0 {
			return nil
		}

		// Insert by selecting the ids the codes name, so an unknown code
		// inserts nothing rather than a row pointing at a permission that does
		// not exist. The service has already rejected unknown codes; this is
		// the second line of defence.
		err := tx.Exec(`INSERT INTO role_permissions (role_id, permission_id)
		                SELECT ?, p.id FROM permissions p WHERE p.code IN ?
		                ON CONFLICT DO NOTHING`, roleID, codes).Error
		if err != nil {
			return fmt.Errorf("grant role permissions: %w", err)
		}
		return nil
	})
}

func (r *GormPermissionRepository) PermissionsFor(ctx context.Context, roleName string) (map[string]struct{}, error) {
	codes := make([]string, 0)
	err := r.db.WithContext(ctx).
		Raw(`SELECT p.code
		     FROM permissions p
		     JOIN role_permissions rp ON rp.permission_id = p.id
		     JOIN roles r ON r.id = rp.role_id
		     WHERE LOWER(r.name) = LOWER(?)`, roleName).
		Scan(&codes).Error
	if err != nil {
		return nil, fmt.Errorf("resolve role permissions: %w", err)
	}

	set := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		set[code] = struct{}{}
	}
	return set, nil
}
