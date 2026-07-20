package keycloakadmin

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/adonese/noebs/internal/tenantcatalog"
	"gopkg.in/yaml.v3"
)

const (
	DesiredStateAPIVersion         = "noebs.sd/keycloak/v1"
	walletAuthorizerClientID       = "noebs-wallet-authorizer"
	walletAuthorizationCallbackURI = "https://api.noebs.sd/wallet/authorizations/oauth/callback"
	googleACR                      = "urn:noebs:acr:google"
	googleTOTPACR                  = "urn:noebs:acr:google-totp"
	acrLoAMap                      = `{"urn:noebs:acr:google":1,"urn:noebs:acr:google-totp":2}`
)

var (
	ErrInvalidConfig       = errors.New("invalid Keycloak reconciler config")
	ErrInvalidDesiredState = errors.New("invalid Keycloak desired state")
)

// Config contains the administrative endpoint and runtime credentials used by
// the reconciler. It belongs in a Kubernetes Secret.
type Config struct {
	BaseURL           string                                `yaml:"base_url"`
	AdminRealm        string                                `yaml:"admin_realm"`
	ClientID          string                                `yaml:"client_id"`
	ClientSecret      string                                `yaml:"client_secret"`
	ClientCredentials map[string]ClientCredential           `yaml:"client_credentials"`
	IdentityProviders map[string]IdentityProviderCredential `yaml:"identity_providers"`
}

type ClientCredential struct {
	ClientSecret string `yaml:"client_secret"`
}

type IdentityProviderCredential struct {
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret"`
}

type DesiredState struct {
	APIVersion         string              `yaml:"api_version"`
	Realm              Realm               `yaml:"realm"`
	Authentication     Authentication      `yaml:"authentication"`
	ReconcilerClient   ReconcilerClient    `yaml:"reconciler_client"`
	ResourceClient     ResourceClient      `yaml:"resource_client"`
	InteractiveClients []InteractiveClient `yaml:"interactive_clients"`
	RealmRoles         []Role              `yaml:"realm_roles"`
	OrganizationClaim  OrganizationClaim   `yaml:"organization_claim"`
	IdentityProviders  []IdentityProvider  `yaml:"identity_providers"`
	Organizations      []Organization      `yaml:"organizations"`
	tenantCatalog      tenantcatalog.Catalog
}

type ReconcilerClient struct {
	ClientID             string   `yaml:"client_id"`
	Name                 string   `yaml:"name"`
	Credential           string   `yaml:"credential"`
	RealmManagementRoles []string `yaml:"realm_management_roles"`
}

type Realm struct {
	Name                         string `yaml:"name"`
	DisplayName                  string `yaml:"display_name"`
	AccessTokenLifespanSeconds   int    `yaml:"access_token_lifespan_seconds"`
	SSOSessionIdleTimeoutSeconds int    `yaml:"sso_session_idle_timeout_seconds"`
	SSOSessionMaxLifespanSeconds int    `yaml:"sso_session_max_lifespan_seconds"`
	RevokeRefreshToken           bool   `yaml:"revoke_refresh_token"`
	RefreshTokenMaxReuse         int    `yaml:"refresh_token_max_reuse"`
}

type Authentication struct {
	BrowserFlow          string                `yaml:"browser_flow"`
	FirstBrokerLoginFlow string                `yaml:"first_broker_login_flow"`
	PostBrokerLoginFlow  string                `yaml:"post_broker_login_flow"`
	Levels               []AuthenticationLevel `yaml:"levels"`
	OTP                  OTPPolicy             `yaml:"otp"`
}

type AuthenticationLevel struct {
	ACR           string `yaml:"acr"`
	Level         int    `yaml:"level"`
	MaxAgeSeconds int    `yaml:"max_age_seconds"`
}

type OTPPolicy struct {
	Type                    string         `yaml:"type"`
	Algorithm               string         `yaml:"algorithm"`
	InitialCounter          int            `yaml:"initial_counter"`
	Digits                  int            `yaml:"digits"`
	LookAheadWindow         int            `yaml:"look_ahead_window"`
	PeriodSeconds           int            `yaml:"period_seconds"`
	Reusable                bool           `yaml:"reusable"`
	ConfigureRequiredAction RequiredAction `yaml:"configure_required_action"`
}

type RequiredAction struct {
	Alias         string `yaml:"alias"`
	Enabled       bool   `yaml:"enabled"`
	DefaultAction bool   `yaml:"default_action"`
	Priority      int    `yaml:"priority"`
}

type ResourceClient struct {
	ClientID string `yaml:"client_id"`
	Name     string `yaml:"name"`
	Roles    []Role `yaml:"roles"`
}

type InteractiveClient struct {
	ClientID               string   `yaml:"client_id"`
	Name                   string   `yaml:"name"`
	AccessType             string   `yaml:"access_type"`
	Credential             string   `yaml:"credential"`
	AuthenticationLevel    int      `yaml:"authentication_level"`
	RedirectURIs           []string `yaml:"redirect_uris"`
	PostLogoutRedirectURIs []string `yaml:"post_logout_redirect_uris"`
	WebOrigins             []string `yaml:"web_origins"`
}

type Role struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type OrganizationClaim struct {
	ClientScope    string            `yaml:"client_scope"`
	MapperName     string            `yaml:"mapper_name"`
	ProtocolMapper string            `yaml:"protocol_mapper"`
	Config         map[string]string `yaml:"config"`
}

type IdentityProvider struct {
	Alias       string            `yaml:"alias"`
	DisplayName string            `yaml:"display_name"`
	ProviderID  string            `yaml:"provider_id"`
	Credential  string            `yaml:"credential"`
	Config      map[string]string `yaml:"config"`
}

type Organization struct {
	Alias      string              `yaml:"alias"`
	Name       string              `yaml:"name"`
	Attributes map[string][]string `yaml:"attributes"`
	Groups     []OrganizationGroup `yaml:"groups"`
}

type OrganizationGroup struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Attributes  map[string][]string `yaml:"attributes"`
	ClientRoles []string            `yaml:"client_roles"`
}

func LoadConfig(reader io.Reader) (Config, error) {
	var config Config
	if err := decodeYAML(reader, &config); err != nil {
		return Config{}, fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (c Config) Validate() error {
	if err := validateValue("base_url", c.BaseURL); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	parsed, err := url.Parse(c.BaseURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.HasSuffix(parsed.Path, "/") {
		return fmt.Errorf("%w: base_url must be an absolute HTTPS URL without credentials, query, fragment, or trailing slash", ErrInvalidConfig)
	}
	for _, field := range []namedValue{
		{name: "admin_realm", value: c.AdminRealm},
		{name: "client_id", value: c.ClientID},
		{name: "client_secret", value: c.ClientSecret},
	} {
		if err := validateValue(field.name, field.value); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
	}
	switch c.AdminRealm {
	case "master":
		if c.ClientID != BootstrapClientID {
			return fmt.Errorf("%w: the master realm requires temporary client %q", ErrInvalidConfig, BootstrapClientID)
		}
	case "noebs":
		if c.ClientID != "noebs-keycloak-reconciler" {
			return fmt.Errorf("%w: the noebs realm requires client %q", ErrInvalidConfig, "noebs-keycloak-reconciler")
		}
	default:
		return fmt.Errorf("%w: admin_realm must be master or noebs", ErrInvalidConfig)
	}
	providerNames := make(map[string]struct{}, len(c.IdentityProviders))
	for name := range c.IdentityProviders {
		providerNames[name] = struct{}{}
	}
	if !exactStringSet(providerNames, "google") {
		return fmt.Errorf("%w: identity_providers must contain only google", ErrInvalidConfig)
	}
	for _, name := range []string{"google"} {
		credential := c.IdentityProviders[name]
		if err := validateValue("identity_providers."+name+".client_id", credential.ClientID); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if err := validateValue("identity_providers."+name+".client_secret", credential.ClientSecret); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
	}
	clientNames := make(map[string]struct{}, len(c.ClientCredentials))
	for name := range c.ClientCredentials {
		clientNames[name] = struct{}{}
	}
	if !exactStringSet(clientNames, "noebs-keycloak-reconciler", "noebs-backoffice", walletAuthorizerClientID) {
		return fmt.Errorf("%w: client_credentials must contain only noebs-keycloak-reconciler, noebs-backoffice, and %s", ErrInvalidConfig, walletAuthorizerClientID)
	}
	for _, name := range []string{"noebs-keycloak-reconciler", "noebs-backoffice", walletAuthorizerClientID} {
		credential := c.ClientCredentials[name]
		if err := validateValue("client_credentials."+name+".client_secret", credential.ClientSecret); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
	}
	return nil
}

func LoadDesiredState(reader io.Reader, catalog tenantcatalog.Catalog) (DesiredState, error) {
	var state DesiredState
	if err := decodeYAML(reader, &state); err != nil {
		return DesiredState{}, fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
	}
	state.tenantCatalog = catalog
	if err := state.Validate(); err != nil {
		return DesiredState{}, err
	}
	return state, nil
}

func (s DesiredState) Validate() error {
	if s.APIVersion != DesiredStateAPIVersion {
		return fmt.Errorf("%w: api_version must be %q", ErrInvalidDesiredState, DesiredStateAPIVersion)
	}
	for _, field := range []namedValue{
		{name: "realm.name", value: s.Realm.Name},
		{name: "realm.display_name", value: s.Realm.DisplayName},
		{name: "authentication.browser_flow", value: s.Authentication.BrowserFlow},
		{name: "authentication.first_broker_login_flow", value: s.Authentication.FirstBrokerLoginFlow},
		{name: "authentication.post_broker_login_flow", value: s.Authentication.PostBrokerLoginFlow},
		{name: "authentication.otp.type", value: s.Authentication.OTP.Type},
		{name: "authentication.otp.algorithm", value: s.Authentication.OTP.Algorithm},
		{name: "authentication.otp.configure_required_action.alias", value: s.Authentication.OTP.ConfigureRequiredAction.Alias},
		{name: "reconciler_client.client_id", value: s.ReconcilerClient.ClientID},
		{name: "reconciler_client.name", value: s.ReconcilerClient.Name},
		{name: "reconciler_client.credential", value: s.ReconcilerClient.Credential},
		{name: "resource_client.client_id", value: s.ResourceClient.ClientID},
		{name: "resource_client.name", value: s.ResourceClient.Name},
		{name: "organization_claim.client_scope", value: s.OrganizationClaim.ClientScope},
		{name: "organization_claim.mapper_name", value: s.OrganizationClaim.MapperName},
		{name: "organization_claim.protocol_mapper", value: s.OrganizationClaim.ProtocolMapper},
	} {
		if err := validateValue(field.name, field.value); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
	}
	if len(s.OrganizationClaim.Config) == 0 {
		return fmt.Errorf("%w: organization_claim.config is required", ErrInvalidDesiredState)
	}
	if s.Realm.Name != "noebs" {
		return fmt.Errorf("%w: realm.name must be %q", ErrInvalidDesiredState, "noebs")
	}
	if s.Realm.AccessTokenLifespanSeconds <= 0 || s.Realm.SSOSessionIdleTimeoutSeconds <= 0 ||
		s.Realm.SSOSessionMaxLifespanSeconds <= s.Realm.SSOSessionIdleTimeoutSeconds {
		return fmt.Errorf("%w: realm token and SSO lifetimes must be positive and the absolute lifetime must exceed the idle lifetime", ErrInvalidDesiredState)
	}
	if !s.Realm.RevokeRefreshToken || s.Realm.RefreshTokenMaxReuse != 0 {
		return fmt.Errorf("%w: realm refresh tokens must rotate with zero reuse", ErrInvalidDesiredState)
	}
	if s.Authentication.BrowserFlow != "noebs-browser" ||
		s.Authentication.FirstBrokerLoginFlow != "noebs-first-broker-login" ||
		s.Authentication.PostBrokerLoginFlow != "noebs-google-post-broker" {
		return fmt.Errorf("%w: authentication must bind the repository-owned browser, first-broker, and Google post-broker flows", ErrInvalidDesiredState)
	}
	levels := s.Authentication.Levels
	if len(levels) != 2 ||
		levels[0].ACR != googleACR || levels[0].Level != 1 || levels[0].MaxAgeSeconds != s.Realm.SSOSessionMaxLifespanSeconds ||
		levels[1].ACR != googleTOTPACR || levels[1].Level != 2 || levels[1].MaxAgeSeconds != 0 {
		return fmt.Errorf("%w: authentication.levels must declare reusable LoA1 followed by one-request LoA2 with max age zero", ErrInvalidDesiredState)
	}
	otp := s.Authentication.OTP
	if otp.Type != "totp" || otp.Algorithm != "HmacSHA256" || otp.InitialCounter != 0 ||
		otp.Digits != 6 || otp.LookAheadWindow != 1 || otp.PeriodSeconds != 30 || otp.Reusable {
		return fmt.Errorf("%w: authentication.otp must use the exact non-reusable TOTP SHA-256 policy", ErrInvalidDesiredState)
	}
	action := otp.ConfigureRequiredAction
	if action.Alias != "CONFIGURE_TOTP" || !action.Enabled || !action.DefaultAction || action.Priority != 10 {
		return fmt.Errorf("%w: authentication.otp.configure_required_action must enforce first-use OTP setup", ErrInvalidDesiredState)
	}
	if s.ResourceClient.ClientID != "noebs-api" {
		return fmt.Errorf("%w: resource_client.client_id must be %q", ErrInvalidDesiredState, "noebs-api")
	}
	if s.ReconcilerClient.ClientID != "noebs-keycloak-reconciler" || s.ReconcilerClient.Credential != "noebs-keycloak-reconciler" {
		return fmt.Errorf("%w: reconciler client_id and credential must both be %q", ErrInvalidDesiredState, "noebs-keycloak-reconciler")
	}
	reconcilerRoles, err := validateStrings("reconciler_client.realm_management_roles", s.ReconcilerClient.RealmManagementRoles)
	if err != nil {
		return err
	}
	if !exactStringSet(reconcilerRoles, "realm-admin") {
		return fmt.Errorf("%w: reconciler_client.realm_management_roles must contain only realm-admin", ErrInvalidDesiredState)
	}
	if s.OrganizationClaim.ClientScope != "organization" ||
		s.OrganizationClaim.MapperName != "noebs-organization-groups" ||
		s.OrganizationClaim.ProtocolMapper != "oidc-organization-group-membership-mapper" {
		return fmt.Errorf("%w: organization claim must use the exact organization scope and Noebs group mapper", ErrInvalidDesiredState)
	}
	if !exactStringMap(s.OrganizationClaim.Config, map[string]string{
		"id.token.claim":            "false",
		"access.token.claim":        "true",
		"lightweight.claim":         "false",
		"userinfo.token.claim":      "false",
		"introspection.token.claim": "false",
		"addGroupRoleMappings":      "true",
	}) {
		return fmt.Errorf("%w: organization_claim.config must emit organization group role mappings in access tokens only", ErrInvalidDesiredState)
	}

	if len(s.RealmRoles) != 0 {
		return fmt.Errorf("%w: realm_roles must be empty; tenant authority belongs to organization groups", ErrInvalidDesiredState)
	}
	clientRoles, err := validateRoles("resource_client.roles", s.ResourceClient.Roles)
	if err != nil {
		return err
	}
	if !exactStringSet(clientRoles,
		"user", "backoffice", "tenant-admin",
		"reporting:read", "wallet:read", "wallet:audit:read", "wallet:manual:create",
		"wallet:fees:write", "wallet:rates:write", "wallet:workflow:approve", "wallet:workflow:reject",
	) {
		return fmt.Errorf("%w: resource_client.roles must contain the exact membership and route permission vocabulary", ErrInvalidDesiredState)
	}
	if len(s.InteractiveClients) == 0 {
		return fmt.Errorf("%w: interactive_clients is required", ErrInvalidDesiredState)
	}
	interactiveClientIDs := make(map[string]struct{}, len(s.InteractiveClients))
	for clientIndex, client := range s.InteractiveClients {
		prefix := fmt.Sprintf("interactive_clients[%d]", clientIndex)
		if err := validateValue(prefix+".client_id", client.ClientID); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
		if err := validateValue(prefix+".name", client.Name); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
		switch client.AccessType {
		case "public":
			if client.Credential != "" {
				return fmt.Errorf("%w: %s.credential must be empty for a public client", ErrInvalidDesiredState, prefix)
			}
		case "confidential":
			if err := validateValue(prefix+".credential", client.Credential); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
			}
		default:
			return fmt.Errorf("%w: %s.access_type must be public or confidential", ErrInvalidDesiredState, prefix)
		}
		if _, exists := interactiveClientIDs[client.ClientID]; exists {
			return fmt.Errorf("%w: duplicate interactive client %q", ErrInvalidDesiredState, client.ClientID)
		}
		if client.ClientID == s.ResourceClient.ClientID {
			return fmt.Errorf("%w: interactive client %q conflicts with the resource client", ErrInvalidDesiredState, client.ClientID)
		}
		interactiveClientIDs[client.ClientID] = struct{}{}
		if err := validateRedirectURIs(prefix+".redirect_uris", client.RedirectURIs); err != nil {
			return err
		}
		if err := validateOptionalRedirectURIs(prefix+".post_logout_redirect_uris", client.PostLogoutRedirectURIs); err != nil {
			return err
		}
		if err := validateOrigins(prefix+".web_origins", client.WebOrigins); err != nil {
			return err
		}
	}
	for _, requiredClient := range []string{"noebs-mobile", "noebs-backoffice", walletAuthorizerClientID} {
		if _, exists := interactiveClientIDs[requiredClient]; !exists {
			return fmt.Errorf("%w: interactive client %q is required", ErrInvalidDesiredState, requiredClient)
		}
	}
	if len(interactiveClientIDs) != 3 {
		return fmt.Errorf("%w: interactive_clients must contain only noebs-mobile, noebs-backoffice, and %s", ErrInvalidDesiredState, walletAuthorizerClientID)
	}
	for _, client := range s.InteractiveClients {
		switch client.ClientID {
		case "noebs-mobile":
			if client.Name != "Noebs Mobile" || client.AccessType != "public" || client.Credential != "" || client.AuthenticationLevel != 1 ||
				!equalStrings(client.RedirectURIs, []string{"https://api.noebs.sd/mobile/oauth/callback"}) ||
				len(client.PostLogoutRedirectURIs) != 0 || len(client.WebOrigins) != 0 {
				return fmt.Errorf("%w: noebs-mobile must declare the exact public LoA1 client", ErrInvalidDesiredState)
			}
		case "noebs-backoffice":
			if client.Name != "Noebs Backoffice" || client.AccessType != "confidential" || client.Credential != "noebs-backoffice" || client.AuthenticationLevel != 1 ||
				!equalStrings(client.RedirectURIs, []string{"https://api.noebs.sd/backoffice/oauth/callback"}) ||
				!equalStrings(client.PostLogoutRedirectURIs, []string{"https://api.noebs.sd/backoffice/oauth/logout/callback"}) || len(client.WebOrigins) != 0 {
				return fmt.Errorf("%w: noebs-backoffice must declare the exact confidential LoA1 client", ErrInvalidDesiredState)
			}
		case walletAuthorizerClientID:
			if client.Name != "Noebs Wallet Authorizer" || client.AccessType != "confidential" || client.Credential != walletAuthorizerClientID || client.AuthenticationLevel != 2 ||
				!equalStrings(client.RedirectURIs, []string{walletAuthorizationCallbackURI}) ||
				len(client.PostLogoutRedirectURIs) != 0 || len(client.WebOrigins) != 0 {
				return fmt.Errorf("%w: %s must declare the exact confidential one-request LoA2 client", ErrInvalidDesiredState, walletAuthorizerClientID)
			}
		}
	}
	if len(s.IdentityProviders) == 0 {
		return fmt.Errorf("%w: identity_providers is required", ErrInvalidDesiredState)
	}
	providerAliases := make(map[string]struct{}, len(s.IdentityProviders))
	for providerIndex, provider := range s.IdentityProviders {
		prefix := fmt.Sprintf("identity_providers[%d]", providerIndex)
		for _, field := range []namedValue{
			{name: prefix + ".alias", value: provider.Alias},
			{name: prefix + ".display_name", value: provider.DisplayName},
			{name: prefix + ".provider_id", value: provider.ProviderID},
			{name: prefix + ".credential", value: provider.Credential},
		} {
			if err := validateValue(field.name, field.value); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
			}
		}
		if _, exists := providerAliases[provider.Alias]; exists {
			return fmt.Errorf("%w: duplicate identity provider alias %q", ErrInvalidDesiredState, provider.Alias)
		}
		providerAliases[provider.Alias] = struct{}{}
		if len(provider.Config) == 0 {
			return fmt.Errorf("%w: %s.config is required", ErrInvalidDesiredState, prefix)
		}
		for key, value := range provider.Config {
			if key == "clientId" || key == "clientSecret" || key == managedAttribute || key == managedCredentialHash {
				return fmt.Errorf("%w: %s.config.%s is reserved", ErrInvalidDesiredState, prefix, key)
			}
			if err := validateValue(prefix+".config key", key); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
			}
			if err := validateValue(prefix+".config."+key, value); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
			}
		}
	}
	if !exactStringSet(providerAliases, "google") {
		return fmt.Errorf("%w: identity_providers must contain only google", ErrInvalidDesiredState)
	}
	provider := s.IdentityProviders[0]
	if provider.Alias != "google" || provider.ProviderID != "google" || provider.Credential != "google" {
		return fmt.Errorf("%w: google identity provider alias, provider_id, and credential must all be %q", ErrInvalidDesiredState, "google")
	}
	if !exactStringMap(provider.Config, map[string]string{
		"defaultScope":      "openid profile email",
		"forwardParameters": "login_hint",
		"issuer":            "https://accounts.google.com",
		"syncMode":          "IMPORT",
	}) {
		return fmt.Errorf("%w: google identity provider config must declare the exact issuer, scopes, forwarding allowlist, and import mode", ErrInvalidDesiredState)
	}
	if len(s.Organizations) == 0 {
		return fmt.Errorf("%w: organizations is required", ErrInvalidDesiredState)
	}

	aliases := make(map[string]struct{}, len(s.Organizations))
	for organizationIndex, organization := range s.Organizations {
		prefix := fmt.Sprintf("organizations[%d]", organizationIndex)
		if err := validateValue(prefix+".alias", organization.Alias); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
		if err := validateValue(prefix+".name", organization.Name); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
		if _, exists := aliases[organization.Alias]; exists {
			return fmt.Errorf("%w: duplicate organization alias %q", ErrInvalidDesiredState, organization.Alias)
		}
		aliases[organization.Alias] = struct{}{}
		if err := validateAttributes(prefix+".attributes", organization.Attributes); err != nil {
			return err
		}
		if len(organization.Groups) == 0 {
			return fmt.Errorf("%w: %s.groups is required", ErrInvalidDesiredState, prefix)
		}
		groups := make(map[string]struct{}, len(organization.Groups))
		for groupIndex, group := range organization.Groups {
			groupPrefix := fmt.Sprintf("%s.groups[%d]", prefix, groupIndex)
			if err := validateValue(groupPrefix+".name", group.Name); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
			}
			if _, exists := groups[group.Name]; exists {
				return fmt.Errorf("%w: duplicate group %q in organization %q", ErrInvalidDesiredState, group.Name, organization.Alias)
			}
			groups[group.Name] = struct{}{}
			if err := validateAttributes(groupPrefix+".attributes", group.Attributes); err != nil {
				return err
			}
			if len(group.ClientRoles) == 0 {
				return fmt.Errorf("%w: %s.client_roles is required", ErrInvalidDesiredState, groupPrefix)
			}
			mapped := make(map[string]struct{}, len(group.ClientRoles))
			for roleIndex, role := range group.ClientRoles {
				if err := validateValue(fmt.Sprintf("%s.client_roles[%d]", groupPrefix, roleIndex), role); err != nil {
					return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
				}
				if _, exists := clientRoles[role]; !exists {
					return fmt.Errorf("%w: organization group role %q is not declared by resource_client.roles", ErrInvalidDesiredState, role)
				}
				if _, exists := mapped[role]; exists {
					return fmt.Errorf("%w: duplicate client role %q on group %q", ErrInvalidDesiredState, role, group.Name)
				}
				mapped[role] = struct{}{}
			}
		}
		if !exactStringSet(groups, "user", "backoffice", "tenant-admin") {
			return fmt.Errorf("%w: organization %q must contain only user, backoffice, and tenant-admin groups", ErrInvalidDesiredState, organization.Alias)
		}
		for _, group := range organization.Groups {
			mapped := stringSliceSet(group.ClientRoles)
			switch group.Name {
			case "user":
				if !exactStringSet(mapped, "user") {
					return fmt.Errorf("%w: organization user group must map only user", ErrInvalidDesiredState)
				}
			case "backoffice":
				if !exactStringSet(mapped, "backoffice", "reporting:read", "wallet:read", "wallet:audit:read") {
					return fmt.Errorf("%w: organization backoffice group must map the read permission set", ErrInvalidDesiredState)
				}
			case "tenant-admin":
				if !exactStringSet(mapped,
					"tenant-admin", "reporting:read", "wallet:read", "wallet:audit:read", "wallet:manual:create",
					"wallet:fees:write", "wallet:rates:write", "wallet:workflow:approve", "wallet:workflow:reject",
				) {
					return fmt.Errorf("%w: organization tenant-admin group must map every route permission", ErrInvalidDesiredState)
				}
			}
		}
	}
	wantedTenants := s.tenantCatalog.All()
	if len(wantedTenants) == 0 {
		return fmt.Errorf("%w: tenant catalog is required", ErrInvalidDesiredState)
	}
	if len(s.Organizations) != len(wantedTenants) {
		return fmt.Errorf("%w: organization aliases and names must exactly match the tenant catalog", ErrInvalidDesiredState)
	}
	organizationsByAlias := make(map[string]Organization, len(s.Organizations))
	for _, organization := range s.Organizations {
		organizationsByAlias[organization.Alias] = organization
	}
	for _, tenant := range wantedTenants {
		organization, exists := organizationsByAlias[string(tenant.ID)]
		if !exists || organization.Name != tenant.Name {
			return fmt.Errorf("%w: organization aliases and names must exactly match the tenant catalog", ErrInvalidDesiredState)
		}
	}
	return nil
}

func validateRedirectURIs(path string, values []string) error {
	if len(values) == 0 {
		return fmt.Errorf("%w: %s is required", ErrInvalidDesiredState, path)
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.Contains(value, "*") {
			return fmt.Errorf("%w: %s[%d] must not contain a wildcard", ErrInvalidDesiredState, path, index)
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("%w: %s[%d] must be an absolute HTTPS URL without credentials, query, or fragment", ErrInvalidDesiredState, path, index)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: duplicate redirect URI %q", ErrInvalidDesiredState, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateOptionalRedirectURIs(path string, values []string) error {
	if len(values) == 0 {
		return nil
	}
	return validateRedirectURIs(path, values)
}

func validateOrigins(path string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.Contains(value, "*") {
			return fmt.Errorf("%w: %s[%d] must not contain a wildcard", ErrInvalidDesiredState, path, index)
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
			return fmt.Errorf("%w: %s[%d] must be an HTTPS origin", ErrInvalidDesiredState, path, index)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("%w: duplicate web origin %q", ErrInvalidDesiredState, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func decodeYAML(reader io.Reader, target any) error {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func validateRoles(path string, roles []Role) (map[string]struct{}, error) {
	if len(roles) == 0 {
		return nil, fmt.Errorf("%w: %s is required", ErrInvalidDesiredState, path)
	}
	result := make(map[string]struct{}, len(roles))
	for index, role := range roles {
		if err := validateValue(fmt.Sprintf("%s[%d].name", path, index), role.Name); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
		if _, exists := result[role.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate role %q in %s", ErrInvalidDesiredState, role.Name, path)
		}
		result[role.Name] = struct{}{}
	}
	return result, nil
}

func validateStrings(path string, values []string) (map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%w: %s is required", ErrInvalidDesiredState, path)
	}
	result := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := validateValue(fmt.Sprintf("%s[%d]", path, index), value); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
		if _, exists := result[value]; exists {
			return nil, fmt.Errorf("%w: duplicate value %q in %s", ErrInvalidDesiredState, value, path)
		}
		result[value] = struct{}{}
	}
	return result, nil
}

func stringSliceSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func validateAttributes(path string, attributes map[string][]string) error {
	for key, values := range attributes {
		if key == managedAttribute {
			return fmt.Errorf("%w: %s.%s is reserved", ErrInvalidDesiredState, path, key)
		}
		if err := validateValue(path+" key", key); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
		}
		if len(values) == 0 {
			return fmt.Errorf("%w: %s.%s must have at least one value", ErrInvalidDesiredState, path, key)
		}
		for index, value := range values {
			if err := validateValue(fmt.Sprintf("%s.%s[%d]", path, key, index), value); err != nil {
				return fmt.Errorf("%w: %v", ErrInvalidDesiredState, err)
			}
		}
	}
	return nil
}

func validateValue(name, value string) error {
	if value == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be a non-empty, whitespace-normalized string", name)
	}
	return nil
}

type namedValue struct {
	name  string
	value string
}

func exactStringSet(values map[string]struct{}, expected ...string) bool {
	if len(values) != len(expected) {
		return false
	}
	for _, value := range expected {
		if _, exists := values[value]; !exists {
			return false
		}
	}
	return true
}

func exactStringMap(values, expected map[string]string) bool {
	if len(values) != len(expected) {
		return false
	}
	for key, value := range expected {
		if values[key] != value {
			return false
		}
	}
	return true
}
