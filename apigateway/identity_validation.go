package gateway

import (
	"errors"

	basestore "github.com/adonese/noebs/store"
)

var (
	ErrMissingTenantID     = errors.New("missing tenant_id")
	ErrInvalidTenantID     = errors.New("invalid tenant_id")
	ErrInvalidUserIdentity = errors.New("invalid user identity")
)

func validateTenantID(tenantID string) (string, error) {
	tenantID, err := basestore.ValidateTenantID(tenantID)
	switch {
	case err == nil:
		return tenantID, nil
	case errors.Is(err, basestore.ErrMissingTenantID):
		return "", ErrMissingTenantID
	case errors.Is(err, basestore.ErrInvalidTenantID):
		return "", ErrInvalidTenantID
	default:
		return "", err
	}
}
