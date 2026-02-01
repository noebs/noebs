package store

import (
	"context"
	"database/sql"
)

func (s *Store) GetAdminRoleByName(ctx context.Context, tenantID, roleName string) (*AdminRole, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if roleName == "" {
		return nil, ErrMissingRoleName
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM admin_roles WHERE tenant_id = ? AND role_name = ?")
	var role AdminRole
	if err := db.GetContext(ctx, &role, stmt, tenantID, roleName); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrAdminRoleNotFound
		}
		return nil, err
	}
	return &role, nil
}

func (s *Store) GetAdminUserByEmail(ctx context.Context, tenantID, email string) (*AdminUser, error) {
	if tenantID == "" {
		return nil, ErrMissingTenantID
	}
	if email == "" {
		return nil, ErrMissingAdminEmail
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind("SELECT * FROM admin_users WHERE tenant_id = ? AND email = ?")
	var user AdminUser
	if err := db.GetContext(ctx, &user, stmt, tenantID, email); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrAdminUserNotFound
		}
		return nil, err
	}
	return &user, nil
}
