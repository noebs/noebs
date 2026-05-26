package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

type appConfigResponse struct {
	TenantID string          `json:"tenant_id"`
	Wallet   appWalletConfig `json:"wallet"`
	OAuth    appOAuthConfig  `json:"oauth,omitempty"`
}

type appWalletConfig struct {
	Enabled         bool   `json:"enabled"`
	DefaultCurrency string `json:"default_currency"`
	PINRequired     bool   `json:"pin_required"`
}

type appOAuthConfig struct {
	GoogleClientID string `json:"google_client_id,omitempty"`
}

func publicAppConfig(cfg ebs_fields.NoebsConfig) (appConfigResponse, error) {
	tenantID, err := configuredTenantID(cfg)
	if err != nil {
		return appConfigResponse{}, err
	}
	return appConfigResponse{
		TenantID: tenantID,
		Wallet: appWalletConfig{
			Enabled:         cfg.WalletEnabled,
			DefaultCurrency: strings.TrimSpace(cfg.WalletDefaultCurrency),
			PINRequired:     cfg.WalletPINRequired,
		},
		OAuth: appOAuthConfig{
			GoogleClientID: strings.TrimSpace(cfg.GoogleClientID),
		},
	}, nil
}

func configuredTenantID(cfg ebs_fields.NoebsConfig) (string, error) {
	return validateTenantID(cfg.DefaultTenantID)
}

func validateTenantID(tenantID string) (string, error) {
	return store.ValidateTenantID(tenantID)
}

func ensureNoReservedTenant(ctx context.Context, s *store.Store) error {
	if s == nil {
		return nil
	}
	tenants, err := s.ListTenants(ctx)
	if err != nil {
		return err
	}
	for _, tenantID := range tenants {
		if store.IsReservedTenantID(tenantID) {
			return fmt.Errorf("%w: reserved tenant_id %q exists", store.ErrInvalidTenantID, strings.TrimSpace(tenantID))
		}
	}
	return nil
}

func appConfigHandler(c *fiber.Ctx) error {
	cfg, err := publicAppConfig(noebsConfig)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
			"code":    "invalid_tenant_id",
			"message": err.Error(),
		})
	}
	return c.Status(http.StatusOK).JSON(cfg)
}
