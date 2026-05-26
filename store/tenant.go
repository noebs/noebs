package store

import "strings"

const reservedTenantID = "default"

func ValidateTenantID(tenantID string) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "", ErrMissingTenantID
	}
	if IsReservedTenantID(tenantID) {
		return "", ErrInvalidTenantID
	}
	return tenantID, nil
}

func IsReservedTenantID(tenantID string) bool {
	return strings.EqualFold(strings.TrimSpace(tenantID), reservedTenantID)
}
