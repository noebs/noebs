package merchant

import (
	"context"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

func tenantIDFromCtx(c *fiber.Ctx, cfg ebs_fields.NoebsConfig) string {
	if c != nil {
		if t := c.Get("X-Tenant-ID"); t != "" {
			return t
		}
		if v := c.Locals("tenant_id"); v != nil {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	if cfg.DefaultTenantID != "" {
		return cfg.DefaultTenantID
	}
	return store.DefaultTenantID
}

func (s *Service) recordTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	if tenantID == "" {
		tenantID = store.DefaultTenantID
	}
	return s.Store.CreateTransaction(ctx, tenantID, res)
}
