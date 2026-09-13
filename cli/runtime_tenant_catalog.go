package main

import (
	"fmt"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantcatalog"
)

func loadRuntimeTenantCatalog(role serviceRole, cfg ebs_fields.NoebsConfig, path string) (tenantcatalog.Catalog, error) {
	if role != serviceRoleAPIGateway && (role != serviceRoleIdentityAuth || !cfg.AccountEnrollment.Enabled) {
		return tenantcatalog.Catalog{}, nil
	}
	catalog, err := tenantcatalog.LoadFile(path)
	if err != nil {
		return tenantcatalog.Catalog{}, err
	}
	if _, err := catalog.Require(cfg.DefaultTenantID); err != nil {
		return tenantcatalog.Catalog{}, fmt.Errorf("default tenant is not in the runtime tenant catalog: %w", err)
	}
	return catalog, nil
}
