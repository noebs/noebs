package store

import "github.com/adonese/noebs/internal/tenantcatalog"

func ValidateTenantID(tenantID string) (string, error) {
	if tenantID == "" {
		return "", ErrMissingTenantID
	}
	id, err := tenantcatalog.ParseID(tenantID)
	if err != nil {
		return "", ErrInvalidTenantID
	}
	return string(id), nil
}
