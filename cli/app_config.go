package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/transactionauth"
	"github.com/adonese/noebs/store"
	"github.com/gofiber/fiber/v2"
)

type appConfigResponse struct {
	TenantID string           `json:"tenant_id"`
	Wallet   appWalletConfig  `json:"wallet"`
	OAuth    appOAuthConfig   `json:"oauth,omitempty"`
	Features appFeatureConfig `json:"features"`
}

type appWalletConfig struct {
	Enabled                  bool                               `json:"enabled"`
	DefaultCurrency          string                             `json:"default_currency"`
	TransactionAuthorization *appTransactionAuthorizationConfig `json:"transaction_authorization,omitempty"`
}

type appTransactionAuthorizationConfig struct {
	BeginPath        string                                       `json:"begin_path"`
	CredentialHeader string                                       `json:"credential_header"`
	RequiredACR      string                                       `json:"required_acr"`
	LifetimeSeconds  int64                                        `json:"lifetime_seconds"`
	Operations       []appTransactionAuthorizationOperationConfig `json:"operations"`
}

type appTransactionAuthorizationOperationConfig struct {
	Operation transactionauth.Operation `json:"operation"`
	Method    string                    `json:"method"`
	Path      string                    `json:"path"`
}

type appOAuthConfig struct {
	Issuer      string   `json:"issuer"`
	ClientID    string   `json:"client_id"`
	Audience    string   `json:"audience"`
	Scopes      []string `json:"scopes"`
	RedirectURI string   `json:"redirect_uri"`
}

type appFeatureConfig struct {
	OpaqueCardManagement bool `json:"opaque_card_management"`
	OpaqueBalance        bool `json:"opaque_balance"`
	Chat                 bool `json:"chat"`
}

func publicAppConfig(cfg ebs_fields.NoebsConfig) (appConfigResponse, error) {
	tenantID, err := configuredTenantID(cfg)
	if err != nil {
		return appConfigResponse{}, err
	}
	walletConfig := appWalletConfig{
		Enabled:         cfg.WalletEnabled,
		DefaultCurrency: strings.TrimSpace(cfg.WalletDefaultCurrency),
	}
	if cfg.WalletEnabled {
		walletConfig.TransactionAuthorization = &appTransactionAuthorizationConfig{
			BeginPath:        "/wallet/authorizations",
			CredentialHeader: walletAuthorizationHeader,
			RequiredACR:      walletAuthorizerRequiredACR,
			LifetimeSeconds:  int64(walletAuthorizationTTL / time.Second),
			Operations: []appTransactionAuthorizationOperationConfig{
				{Operation: transactionauth.OperationWalletP2P, Method: http.MethodPost, Path: "/wallet/p2p"},
				{Operation: transactionauth.OperationWalletWithdrawal, Method: http.MethodPost, Path: "/wallet/withdrawals"},
			},
		}
	}
	return appConfigResponse{
		TenantID: tenantID,
		Wallet:   walletConfig,
		OAuth: appOAuthConfig{
			Issuer:      strings.TrimSpace(cfg.OIDC.Issuer),
			ClientID:    "noebs-mobile",
			Audience:    strings.TrimSpace(cfg.OIDC.Audience),
			Scopes:      []string{"openid", "organization:*"},
			RedirectURI: "https://api.noebs.sd/mobile/oauth/callback",
		},
		Features: appFeatureConfig{
			OpaqueCardManagement: cfg.OpaqueCardManagementEnabled,
			OpaqueBalance:        cfg.OpaqueBalanceEnabled,
			Chat:                 cfg.ChatEnabled,
		},
	}, nil
}

func configuredTenantID(cfg ebs_fields.NoebsConfig) (string, error) {
	return validateTenantID(cfg.DefaultTenantID)
}

func validateTenantID(tenantID string) (string, error) {
	return store.ValidateTenantID(tenantID)
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
