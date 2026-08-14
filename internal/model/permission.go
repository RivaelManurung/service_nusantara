package model

import "github.com/google/uuid"

// Permission is one thing a role may be allowed to do.
//
// Code is the identity the Go code compares against ("product.write"); Label
// and Group exist only so the admin UI can render a readable matrix. The rows
// are seeded from the catalogue declared in the role module, never typed by
// hand, so a permission the code checks always exists in the table.
type Permission struct {
	ID    uuid.UUID `gorm:"type:uuid;default:gen_random_uuid();primaryKey"`
	Code  string    `gorm:"type:varchar(100);unique;not null"`
	Label string    `gorm:"type:varchar(150);not null"`
	// Group is stored as permission_group: `group` is a reserved word in
	// PostgreSQL and would need quoting in every hand-written query.
	Group string `gorm:"column:permission_group;type:varchar(100);not null"`
}

// RolePermission joins a role to a permission.
//
// The pair is the primary key, so a grant cannot be recorded twice. Both sides
// cascade on delete: a role that is removed must not leave orphaned grants that
// a later role reusing the id would silently inherit.
type RolePermission struct {
	RoleID       uuid.UUID  `gorm:"type:uuid;primaryKey"`
	PermissionID uuid.UUID  `gorm:"type:uuid;primaryKey;index"`
	Role         Role       `gorm:"foreignKey:RoleID;constraint:OnDelete:CASCADE"`
	Permission   Permission `gorm:"foreignKey:PermissionID;constraint:OnDelete:CASCADE"`
}

// TableName pins the join table's name; GORM would otherwise pluralise the
// struct to the same thing, but stating it removes the guess.
func (RolePermission) TableName() string { return "role_permissions" }
