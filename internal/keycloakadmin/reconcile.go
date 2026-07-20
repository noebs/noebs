package keycloakadmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

const (
	managedAttribute        = "noebs.managed"
	managedCredentialHash   = "noebs.credential-sha256"
	managedClientSecretHash = "noebs.client-secret-sha256"
	managedRoleDescription  = "[managed-by:noebs]"
	organizationMapperID    = "oidc-organization-membership-mapper"
	organizationMapperName  = "organization"
	audienceMapperID        = "oidc-audience-mapper"
	audienceMapperName      = "noebs-api-audience"
	subjectMapperID         = "oidc-sub-mapper"
	subjectMapperName       = "noebs-subject"
)

type realmRepresentation struct {
	Realm                               string             `json:"realm"`
	DisplayName                         string             `json:"displayName"`
	Enabled                             bool               `json:"enabled"`
	OrganizationsEnabled                bool               `json:"organizationsEnabled"`
	RegistrationAllowed                 bool               `json:"registrationAllowed"`
	RegistrationEmailAsUsername         bool               `json:"registrationEmailAsUsername"`
	RememberMe                          bool               `json:"rememberMe"`
	VerifyEmail                         bool               `json:"verifyEmail"`
	LoginWithEmailAllowed               bool               `json:"loginWithEmailAllowed"`
	DuplicateEmailsAllowed              bool               `json:"duplicateEmailsAllowed"`
	ResetPasswordAllowed                bool               `json:"resetPasswordAllowed"`
	EditUsernameAllowed                 bool               `json:"editUsernameAllowed"`
	SSLRequired                         string             `json:"sslRequired"`
	DefaultSignatureAlgorithm           string             `json:"defaultSignatureAlgorithm"`
	AccessTokenLifespan                 int                `json:"accessTokenLifespan"`
	AccessTokenLifespanForImplicitFlow  int                `json:"accessTokenLifespanForImplicitFlow"`
	SSOSessionIdleTimeout               int                `json:"ssoSessionIdleTimeout"`
	SSOSessionMaxLifespan               int                `json:"ssoSessionMaxLifespan"`
	SSOSessionIdleTimeoutRememberMe     int                `json:"ssoSessionIdleTimeoutRememberMe"`
	SSOSessionMaxLifespanRememberMe     int                `json:"ssoSessionMaxLifespanRememberMe"`
	OfflineSessionIdleTimeout           int                `json:"offlineSessionIdleTimeout"`
	OfflineSessionMaxLifespanEnabled    bool               `json:"offlineSessionMaxLifespanEnabled"`
	OfflineSessionMaxLifespan           int                `json:"offlineSessionMaxLifespan"`
	ClientSessionIdleTimeout            int                `json:"clientSessionIdleTimeout"`
	ClientSessionMaxLifespan            int                `json:"clientSessionMaxLifespan"`
	ClientOfflineSessionIdleTimeout     int                `json:"clientOfflineSessionIdleTimeout"`
	ClientOfflineSessionMaxLifespan     int                `json:"clientOfflineSessionMaxLifespan"`
	AccessCodeLifespan                  int                `json:"accessCodeLifespan"`
	AccessCodeLifespanUserAction        int                `json:"accessCodeLifespanUserAction"`
	AccessCodeLifespanLogin             int                `json:"accessCodeLifespanLogin"`
	ActionTokenGeneratedByUserLifespan  int                `json:"actionTokenGeneratedByUserLifespan"`
	ActionTokenGeneratedByAdminLifespan int                `json:"actionTokenGeneratedByAdminLifespan"`
	OAuth2DeviceCodeLifespan            int                `json:"oauth2DeviceCodeLifespan"`
	OAuth2DevicePollingInterval         int                `json:"oauth2DevicePollingInterval"`
	RevokeRefreshToken                  bool               `json:"revokeRefreshToken"`
	RefreshTokenMaxReuse                int                `json:"refreshTokenMaxReuse"`
	BruteForceProtected                 bool               `json:"bruteForceProtected"`
	BruteForceStrategy                  string             `json:"bruteForceStrategy"`
	PermanentLockout                    bool               `json:"permanentLockout"`
	MaxTemporaryLockouts                int                `json:"maxTemporaryLockouts"`
	FailureFactor                       int                `json:"failureFactor"`
	WaitIncrementSeconds                int                `json:"waitIncrementSeconds"`
	QuickLoginCheckMilliSeconds         int                `json:"quickLoginCheckMilliSeconds"`
	MinimumQuickLoginWaitSeconds        int                `json:"minimumQuickLoginWaitSeconds"`
	MaxFailureWaitSeconds               int                `json:"maxFailureWaitSeconds"`
	MaxDeltaTimeSeconds                 int                `json:"maxDeltaTimeSeconds"`
	MaxSecondaryAuthFailures            int                `json:"maxSecondaryAuthFailures"`
	OTPPolicyType                       string             `json:"otpPolicyType"`
	OTPPolicyAlgorithm                  string             `json:"otpPolicyAlgorithm"`
	OTPPolicyInitialCounter             int                `json:"otpPolicyInitialCounter"`
	OTPPolicyDigits                     int                `json:"otpPolicyDigits"`
	OTPPolicyLookAheadWindow            int                `json:"otpPolicyLookAheadWindow"`
	OTPPolicyPeriod                     int                `json:"otpPolicyPeriod"`
	OTPPolicyCodeReusable               bool               `json:"otpPolicyCodeReusable"`
	BrowserFlow                         string             `json:"browserFlow"`
	RegistrationFlow                    string             `json:"registrationFlow"`
	DirectGrantFlow                     string             `json:"directGrantFlow"`
	ResetCredentialsFlow                string             `json:"resetCredentialsFlow"`
	ClientAuthenticationFlow            string             `json:"clientAuthenticationFlow"`
	DockerAuthenticationFlow            string             `json:"dockerAuthenticationFlow"`
	FirstBrokerLoginFlow                string             `json:"firstBrokerLoginFlow"`
	UserManagedAccessAllowed            bool               `json:"userManagedAccessAllowed"`
	AdminPermissionsEnabled             bool               `json:"adminPermissionsEnabled"`
	BrowserSecurityHeaders              map[string]string  `json:"browserSecurityHeaders"`
	Attributes                          map[string]*string `json:"attributes"`
}

type clientRepresentation struct {
	ID                                 string            `json:"id,omitempty"`
	ClientID                           string            `json:"clientId"`
	Name                               string            `json:"name"`
	Enabled                            bool              `json:"enabled"`
	Protocol                           string            `json:"protocol"`
	ClientAuthenticatorType            string            `json:"clientAuthenticatorType"`
	Secret                             string            `json:"secret,omitempty"`
	BearerOnly                         bool              `json:"bearerOnly"`
	PublicClient                       bool              `json:"publicClient"`
	ConsentRequired                    bool              `json:"consentRequired"`
	StandardFlowEnabled                bool              `json:"standardFlowEnabled"`
	ImplicitFlowEnabled                bool              `json:"implicitFlowEnabled"`
	DirectAccessGrantsEnabled          bool              `json:"directAccessGrantsEnabled"`
	ServiceAccountsEnabled             bool              `json:"serviceAccountsEnabled"`
	AuthorizationServicesEnabled       bool              `json:"authorizationServicesEnabled"`
	FullScopeAllowed                   bool              `json:"fullScopeAllowed"`
	RedirectURIs                       []string          `json:"redirectUris"`
	WebOrigins                         []string          `json:"webOrigins"`
	Attributes                         map[string]string `json:"attributes"`
	RootURL                            string            `json:"rootUrl"`
	BaseURL                            string            `json:"baseUrl"`
	AdminURL                           string            `json:"adminUrl"`
	SurrogateAuthRequired              bool              `json:"surrogateAuthRequired"`
	AlwaysDisplayInConsole             bool              `json:"alwaysDisplayInConsole"`
	FrontchannelLogout                 bool              `json:"frontchannelLogout"`
	NodeReRegistrationTimeout          int               `json:"nodeReRegistrationTimeout"`
	NotBefore                          int               `json:"notBefore"`
	AuthenticationFlowBindingOverrides map[string]string `json:"authenticationFlowBindingOverrides"`
}

type credentialRepresentation struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type roleRepresentation struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Composite   bool   `json:"composite,omitempty"`
	ClientRole  bool   `json:"clientRole,omitempty"`
	ContainerID string `json:"containerId,omitempty"`
}

type userRepresentation struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type clientRoleMappingRepresentation struct {
	ID       string               `json:"id"`
	Client   string               `json:"client"`
	Mappings []roleRepresentation `json:"mappings"`
}

type roleMappingsRepresentation struct {
	RealmMappings  []roleRepresentation                       `json:"realmMappings"`
	ClientMappings map[string]clientRoleMappingRepresentation `json:"clientMappings"`
}

type clientScopeRepresentation struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
}

type protocolMapperRepresentation struct {
	ID              string            `json:"id,omitempty"`
	Name            string            `json:"name"`
	Protocol        string            `json:"protocol"`
	ProtocolMapper  string            `json:"protocolMapper"`
	ConsentRequired bool              `json:"consentRequired"`
	Config          map[string]string `json:"config"`
}

type organizationRepresentation struct {
	ID         string              `json:"id,omitempty"`
	Name       string              `json:"name"`
	Alias      string              `json:"alias"`
	Enabled    bool                `json:"enabled"`
	Attributes map[string][]string `json:"attributes"`
}

type groupRepresentation struct {
	ID          string              `json:"id,omitempty"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Attributes  map[string][]string `json:"attributes"`
}

type identityProviderRepresentation struct {
	Alias                     string            `json:"alias"`
	DisplayName               string            `json:"displayName"`
	ProviderID                string            `json:"providerId"`
	Enabled                   bool              `json:"enabled"`
	TrustEmail                bool              `json:"trustEmail"`
	StoreToken                bool              `json:"storeToken"`
	AddReadTokenRoleOnCreate  bool              `json:"addReadTokenRoleOnCreate"`
	AuthenticateByDefault     bool              `json:"authenticateByDefault"`
	LinkOnly                  bool              `json:"linkOnly"`
	FirstBrokerLoginFlowAlias string            `json:"firstBrokerLoginFlowAlias"`
	PostBrokerLoginFlowAlias  string            `json:"postBrokerLoginFlowAlias"`
	HideOnLogin               bool              `json:"hideOnLogin"`
	Config                    map[string]string `json:"config"`
}

type identityProviderMapperRepresentation struct {
	ID                     string            `json:"id"`
	Name                   string            `json:"name"`
	IdentityProviderAlias  string            `json:"identityProviderAlias"`
	IdentityProviderMapper string            `json:"identityProviderMapper"`
	Config                 map[string]string `json:"config"`
}

func (r *Reconciler) Reconcile(ctx context.Context, state DesiredState) (Result, error) {
	if err := state.Validate(); err != nil {
		return Result{}, err
	}
	session, err := r.session(ctx)
	if err != nil {
		return Result{}, err
	}
	result := Result{}
	if err := ensureRealm(ctx, session, state, false, &result); err != nil {
		return Result{}, err
	}
	// A newly-created realm adds its management roles to the master admin
	// composite. Refresh the bootstrap token before administering that realm.
	session, err = r.session(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := reconcileHumanAuthentication(ctx, session, state, &result); err != nil {
		return Result{}, err
	}
	if err := ensureRealm(ctx, session, state, true, &result); err != nil {
		return Result{}, err
	}
	reconcilerClient, err := reconcileReconcilerClient(ctx, session, state, r.config.ClientCredentials, &result)
	if err != nil {
		return Result{}, err
	}
	if err := reconcileExactClientProtocolMappers(ctx, session, state.Realm.Name, reconcilerClient, nil, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, reconcilerClient, "default", []string{"roles"}, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, reconcilerClient, "optional", nil, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileRealmRoles(ctx, session, state, &result); err != nil {
		return Result{}, err
	}
	client, err := reconcileResourceClient(ctx, session, state, &result)
	if err != nil {
		return Result{}, err
	}
	if err := reconcileExactClientProtocolMappers(ctx, session, state.Realm.Name, client, nil, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, client, "default", nil, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, client, "optional", nil, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileEmptyClientRoleScopeMappings(ctx, session, state.Realm.Name, client, &result); err != nil {
		return Result{}, err
	}
	clientRoles, err := reconcileClientRoles(ctx, session, state, client, &result)
	if err != nil {
		return Result{}, err
	}
	interactiveClients, err := reconcileInteractiveClients(ctx, session, state, r.config.ClientCredentials, &result)
	if err != nil {
		return Result{}, err
	}
	if err := reconcileInteractiveRoleScopeMappings(ctx, session, state, interactiveClients, &result); err != nil {
		return Result{}, err
	}
	serviceClients, err := reconcileServiceClients(ctx, session, state, r.config.ClientCredentials, &result)
	if err != nil {
		return Result{}, err
	}
	if err := reconcileExactClients(ctx, session, state, interactiveClients, serviceClients, client, reconcilerClient, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileOrganizationMapper(ctx, session, state, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileIdentityProviders(ctx, session, state, r.config.IdentityProviders, &result); err != nil {
		return Result{}, err
	}
	if err := reconcileOrganizations(ctx, session, state, client, clientRoles, &result); err != nil {
		return Result{}, err
	}
	if err := pruneUnmanagedAuthenticationFlows(ctx, session, state.Realm.Name, desiredAuthenticationFlows(state), &result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func reconcileReconcilerClient(ctx context.Context, session *adminSession, state DesiredState, credentials map[string]ClientCredential, result *Result) (clientRepresentation, error) {
	desired := state.ReconcilerClient
	credential, found := credentials[desired.Credential]
	if !found {
		return clientRepresentation{}, fmt.Errorf("%w: client credential %q is required", ErrInvalidConfig, desired.Credential)
	}
	base := realmPath(state.Realm.Name)
	attributes := managedClientAttributes()
	attributes[managedClientSecretHash] = secretHash(credential.ClientSecret)
	wanted := clientRepresentation{
		ClientID:                           desired.ClientID,
		Name:                               desired.Name,
		Enabled:                            true,
		Protocol:                           "openid-connect",
		ClientAuthenticatorType:            "client-secret",
		Secret:                             credential.ClientSecret,
		BearerOnly:                         false,
		PublicClient:                       false,
		ConsentRequired:                    false,
		StandardFlowEnabled:                false,
		ImplicitFlowEnabled:                false,
		DirectAccessGrantsEnabled:          false,
		ServiceAccountsEnabled:             true,
		AuthorizationServicesEnabled:       false,
		FullScopeAllowed:                   false,
		Attributes:                         attributes,
		RedirectURIs:                       []string{},
		WebOrigins:                         []string{},
		NodeReRegistrationTimeout:          -1,
		AuthenticationFlowBindingOverrides: map[string]string{},
	}
	existing, exists, err := findClient(ctx, session, base, desired.ClientID)
	if err != nil {
		return clientRepresentation{}, err
	}
	if !exists {
		if err := session.post(ctx, base+"/clients", wanted); err != nil {
			return clientRepresentation{}, fmt.Errorf("create reconciler client %s: %w", desired.ClientID, err)
		}
		result.Created++
		existing, exists, err = findClient(ctx, session, base, desired.ClientID)
		if err != nil {
			return clientRepresentation{}, err
		}
		if !exists {
			return clientRepresentation{}, fmt.Errorf("%w: created reconciler client %s was not returned by Keycloak", ErrUnexpectedResponse, desired.ClientID)
		}
	}
	secretMatches, err := clientSecretMatches(ctx, session, base, existing, credential.ClientSecret)
	if err != nil {
		return clientRepresentation{}, err
	}
	if !clientMatches(existing, wanted) || !secretMatches {
		update := clientUpdate(existing, wanted)
		if err := session.put(ctx, base+"/clients/"+url.PathEscape(existing.ID), update); err != nil {
			return clientRepresentation{}, fmt.Errorf("update reconciler client %s: %w", desired.ClientID, err)
		}
		result.Updated++
		existing = update
	}
	if !secretMatches {
		if err := verifyClientSecret(ctx, session, base, existing, credential.ClientSecret); err != nil {
			return clientRepresentation{}, err
		}
	}
	if err := reconcileReconcilerRoleMappings(ctx, session, base, existing, desired.RealmManagementRoles, result); err != nil {
		return clientRepresentation{}, err
	}
	return existing, nil
}

func reconcileReconcilerRoleMappings(ctx context.Context, session *adminSession, realmBase string, reconcilerClient clientRepresentation, roleNames []string, result *Result) error {
	var serviceAccount userRepresentation
	found, err := session.get(ctx, realmBase+"/clients/"+url.PathEscape(reconcilerClient.ID)+"/service-account-user", &serviceAccount)
	if err != nil {
		return fmt.Errorf("read reconciler service account: %w", err)
	}
	if !found || serviceAccount.ID == "" {
		return fmt.Errorf("%w: reconciler service account was not returned by Keycloak", ErrUnexpectedResponse)
	}
	managementClient, found, err := findClient(ctx, session, realmBase, "realm-management")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%w: realm-management client does not exist", ErrUnexpectedResponse)
	}

	desiredRoles := make(map[string]roleRepresentation, len(roleNames))
	for _, roleName := range roleNames {
		var role roleRepresentation
		found, err := session.get(ctx, realmBase+"/clients/"+url.PathEscape(managementClient.ID)+"/roles/"+url.PathEscape(roleName), &role)
		if err != nil {
			return fmt.Errorf("read realm-management role %s: %w", roleName, err)
		}
		if !found {
			return fmt.Errorf("%w: realm-management role %s does not exist", ErrUnexpectedResponse, roleName)
		}
		desiredRoles[roleName] = role
	}
	scopeBase := realmBase + "/clients/" + url.PathEscape(reconcilerClient.ID) + "/scope-mappings"
	var scopeMappings roleMappingsRepresentation
	if _, err := session.get(ctx, scopeBase, &scopeMappings); err != nil {
		return fmt.Errorf("list reconciler client scope mappings: %w", err)
	}
	if err := pruneRoleMappings(ctx, session, scopeBase, scopeMappings, map[string]struct{}{"realm-management": {}}, result); err != nil {
		return fmt.Errorf("prune reconciler client scope mappings: %w", err)
	}
	scopePath := scopeBase + "/clients/" + url.PathEscape(managementClient.ID)
	if err := reconcileExactRoles(ctx, session, scopePath, scopeMappings.ClientMappings["realm-management"].Mappings, roleNames, desiredRoles, result); err != nil {
		return fmt.Errorf("reconcile realm-management roles in the reconciler token scope: %w", err)
	}

	roleBase := realmBase + "/users/" + url.PathEscape(serviceAccount.ID) + "/role-mappings"
	var mappings roleMappingsRepresentation
	if _, err := session.get(ctx, roleBase, &mappings); err != nil {
		return fmt.Errorf("list reconciler service-account role mappings: %w", err)
	}
	if err := pruneRoleMappings(ctx, session, roleBase, mappings, map[string]struct{}{"realm-management": {}}, result); err != nil {
		return fmt.Errorf("prune reconciler service-account role mappings: %w", err)
	}

	managementPath := roleBase + "/clients/" + url.PathEscape(managementClient.ID)
	current := mappings.ClientMappings["realm-management"].Mappings
	if err := reconcileExactRoles(ctx, session, managementPath, current, roleNames, desiredRoles, result); err != nil {
		return fmt.Errorf("reconcile reconciler service-account realm-management roles: %w", err)
	}
	return nil
}

func pruneRoleMappings(ctx context.Context, session *adminSession, base string, mappings roleMappingsRepresentation, keepClients map[string]struct{}, result *Result) error {
	if len(mappings.RealmMappings) != 0 {
		if err := session.delete(ctx, base+"/realm", mappings.RealmMappings); err != nil {
			return err
		}
		result.Updated++
	}
	clientIDs := make([]string, 0, len(mappings.ClientMappings))
	for clientID := range mappings.ClientMappings {
		clientIDs = append(clientIDs, clientID)
	}
	sort.Strings(clientIDs)
	for _, clientID := range clientIDs {
		if _, keep := keepClients[clientID]; keep {
			continue
		}
		mapping := mappings.ClientMappings[clientID]
		if len(mapping.Mappings) == 0 {
			continue
		}
		if mapping.ID == "" {
			return fmt.Errorf("%w: role mapping for client %s has no client id", ErrUnexpectedResponse, clientID)
		}
		if err := session.delete(ctx, base+"/clients/"+url.PathEscape(mapping.ID), mapping.Mappings); err != nil {
			return err
		}
		result.Updated++
	}
	return nil
}

func reconcileExactRoles(ctx context.Context, session *adminSession, path string, current []roleRepresentation, roleNames []string, desiredRoles map[string]roleRepresentation, result *Result) error {
	currentNames := make(map[string]struct{}, len(current))
	var remove []roleRepresentation
	for _, role := range current {
		currentNames[role.Name] = struct{}{}
		if _, keep := desiredRoles[role.Name]; !keep {
			remove = append(remove, role)
		}
	}
	if len(remove) != 0 {
		if err := session.delete(ctx, path, remove); err != nil {
			return err
		}
		result.Updated++
	}
	var add []roleRepresentation
	for _, roleName := range roleNames {
		if _, exists := currentNames[roleName]; !exists {
			add = append(add, desiredRoles[roleName])
		}
	}
	if len(add) != 0 {
		if err := session.post(ctx, path, add); err != nil {
			return err
		}
		result.Updated++
	}
	return nil
}

func ensureRealm(ctx context.Context, session *adminSession, state DesiredState, bindAuthentication bool, result *Result) error {
	desired := state.Realm
	path := realmPath(desired.Name)
	wanted := desiredRealmRepresentation(state)
	var existing realmRepresentation
	found, err := session.get(ctx, path, &existing)
	if err != nil {
		return fmt.Errorf("read realm %s: %w", desired.Name, err)
	}
	if !found {
		initial := wanted
		initial.BrowserFlow = "browser"
		initial.FirstBrokerLoginFlow = "first broker login"
		if err := session.post(ctx, "/admin/realms", initial); err != nil {
			return fmt.Errorf("create realm %s: %w", desired.Name, err)
		}
		result.Created++
		return nil
	}
	if !bindAuthentication {
		wanted.BrowserFlow = existing.BrowserFlow
		wanted.FirstBrokerLoginFlow = existing.FirstBrokerLoginFlow
	}
	if !realmMatches(existing, wanted) {
		if err := session.put(ctx, path, realmUpdate(existing, wanted)); err != nil {
			return fmt.Errorf("update realm %s: %w", desired.Name, err)
		}
		result.Updated++
	}
	return nil
}

func desiredRealmRepresentation(state DesiredState) realmRepresentation {
	desired := state.Realm
	otp := state.Authentication.OTP
	return realmRepresentation{
		Realm:                               desired.Name,
		DisplayName:                         desired.DisplayName,
		Enabled:                             true,
		OrganizationsEnabled:                true,
		RegistrationAllowed:                 false,
		RegistrationEmailAsUsername:         false,
		RememberMe:                          false,
		VerifyEmail:                         false,
		LoginWithEmailAllowed:               true,
		DuplicateEmailsAllowed:              false,
		ResetPasswordAllowed:                false,
		EditUsernameAllowed:                 false,
		SSLRequired:                         "all",
		DefaultSignatureAlgorithm:           "RS256",
		AccessTokenLifespan:                 desired.AccessTokenLifespanSeconds,
		AccessTokenLifespanForImplicitFlow:  desired.AccessTokenLifespanSeconds,
		SSOSessionIdleTimeout:               desired.SSOSessionIdleTimeoutSeconds,
		SSOSessionMaxLifespan:               desired.SSOSessionMaxLifespanSeconds,
		OfflineSessionIdleTimeout:           2_592_000,
		OfflineSessionMaxLifespan:           5_184_000,
		AccessCodeLifespan:                  60,
		AccessCodeLifespanUserAction:        300,
		AccessCodeLifespanLogin:             300,
		ActionTokenGeneratedByUserLifespan:  300,
		ActionTokenGeneratedByAdminLifespan: 43_200,
		OAuth2DeviceCodeLifespan:            600,
		OAuth2DevicePollingInterval:         5,
		RevokeRefreshToken:                  desired.RevokeRefreshToken,
		RefreshTokenMaxReuse:                desired.RefreshTokenMaxReuse,
		BruteForceProtected:                 true,
		BruteForceStrategy:                  "MULTIPLE",
		FailureFactor:                       5,
		WaitIncrementSeconds:                60,
		QuickLoginCheckMilliSeconds:         1_000,
		MinimumQuickLoginWaitSeconds:        60,
		MaxFailureWaitSeconds:               900,
		MaxDeltaTimeSeconds:                 43_200,
		MaxSecondaryAuthFailures:            100,
		OTPPolicyType:                       otp.Type,
		OTPPolicyAlgorithm:                  otp.Algorithm,
		OTPPolicyInitialCounter:             otp.InitialCounter,
		OTPPolicyDigits:                     otp.Digits,
		OTPPolicyLookAheadWindow:            otp.LookAheadWindow,
		OTPPolicyPeriod:                     otp.PeriodSeconds,
		OTPPolicyCodeReusable:               otp.Reusable,
		BrowserFlow:                         state.Authentication.BrowserFlow,
		RegistrationFlow:                    "registration",
		DirectGrantFlow:                     "direct grant",
		ResetCredentialsFlow:                "reset credentials",
		ClientAuthenticationFlow:            "clients",
		DockerAuthenticationFlow:            "docker auth",
		FirstBrokerLoginFlow:                state.Authentication.FirstBrokerLoginFlow,
		BrowserSecurityHeaders: map[string]string{
			"contentSecurityPolicy":           "frame-src 'self'; frame-ancestors 'self'; object-src 'none';",
			"contentSecurityPolicyReportOnly": "",
			"referrerPolicy":                  "no-referrer",
			"strictTransportSecurity":         "max-age=31536000; includeSubDomains",
			"xContentTypeOptions":             "nosniff",
			"xFrameOptions":                   "SAMEORIGIN",
			"xRobotsTag":                      "none",
		},
		Attributes: stringPointerMap(map[string]string{
			"acr.loa.map":                      acrLoAMap,
			"cibaAuthRequestedUserHint":        "login_hint",
			"cibaBackchannelTokenDeliveryMode": "poll",
			"cibaExpiresIn":                    "120",
			"cibaInterval":                     "5",
			"clientOfflineSessionIdleTimeout":  "0",
			"clientOfflineSessionMaxLifespan":  "0",
			"clientSessionIdleTimeout":         "0",
			"clientSessionMaxLifespan":         "0",
			"oauth2DeviceCodeLifespan":         "600",
			"oauth2DevicePollingInterval":      "5",
			"parRequestUriLifespan":            "60",
			"realmReusableOtpCode":             "false",
		}),
	}
}

func reconcileRealmRoles(ctx context.Context, session *adminSession, state DesiredState, result *Result) error {
	base := realmPath(state.Realm.Name)
	var existing []roleRepresentation
	if _, err := session.get(ctx, base+"/roles?briefRepresentation=false&first=0&max=1000", &existing); err != nil {
		return fmt.Errorf("list realm roles: %w", err)
	}
	builtins := make(map[string]bool, len(keycloakBuiltinRealmRoleNames(state.Realm.Name)))
	for _, role := range existing {
		for _, builtin := range keycloakBuiltinRealmRoleNames(state.Realm.Name) {
			if role.Name == builtin {
				builtins[builtin] = true
			}
		}
	}
	for _, role := range keycloakBuiltinRealmRoleNames(state.Realm.Name) {
		if !builtins[role] {
			return fmt.Errorf("%w: Keycloak 26.7 built-in realm role %s is missing", ErrUnexpectedResponse, role)
		}
	}
	for _, role := range existing {
		if isKeycloakBuiltinRealmRole(state.Realm.Name, role.Name) {
			continue
		}
		if err := session.delete(ctx, base+"/roles/"+url.PathEscape(role.Name), nil); err != nil {
			return fmt.Errorf("delete realm role %s outside desired state: %w", role.Name, err)
		}
		result.Deleted++
	}
	return nil
}

func keycloakBuiltinRealmRoleNames(realm string) []string {
	return []string{"default-roles-" + realm, "offline_access", "uma_authorization"}
}

func isKeycloakBuiltinRealmRole(realm, role string) bool {
	return role == "default-roles-"+realm || role == "offline_access" || role == "uma_authorization"
}

func reconcileResourceClient(ctx context.Context, session *adminSession, state DesiredState, result *Result) (clientRepresentation, error) {
	base := realmPath(state.Realm.Name)
	attributes := managedClientAttributes()
	attributes["access.token.signed.response.alg"] = "RS256"
	wanted := clientRepresentation{
		ClientID:                           state.ResourceClient.ClientID,
		Name:                               state.ResourceClient.Name,
		Enabled:                            true,
		Protocol:                           "openid-connect",
		ClientAuthenticatorType:            "client-secret",
		BearerOnly:                         true,
		PublicClient:                       false,
		ConsentRequired:                    false,
		StandardFlowEnabled:                false,
		ImplicitFlowEnabled:                false,
		DirectAccessGrantsEnabled:          false,
		ServiceAccountsEnabled:             false,
		AuthorizationServicesEnabled:       false,
		FullScopeAllowed:                   false,
		Attributes:                         attributes,
		RedirectURIs:                       []string{},
		WebOrigins:                         []string{},
		NodeReRegistrationTimeout:          -1,
		AuthenticationFlowBindingOverrides: map[string]string{},
	}
	existing, found, err := findClient(ctx, session, base, state.ResourceClient.ClientID)
	if err != nil {
		return clientRepresentation{}, err
	}
	if !found {
		if err := session.post(ctx, base+"/clients", wanted); err != nil {
			return clientRepresentation{}, fmt.Errorf("create client %s: %w", wanted.ClientID, err)
		}
		result.Created++
		existing, found, err = findClient(ctx, session, base, state.ResourceClient.ClientID)
		if err != nil {
			return clientRepresentation{}, err
		}
		if !found {
			return clientRepresentation{}, fmt.Errorf("%w: created client %s was not returned by Keycloak", ErrUnexpectedResponse, wanted.ClientID)
		}
	}
	if !clientMatches(existing, wanted) {
		update := clientUpdate(existing, wanted)
		if err := session.put(ctx, base+"/clients/"+url.PathEscape(existing.ID), update); err != nil {
			return clientRepresentation{}, fmt.Errorf("update client %s: %w", wanted.ClientID, err)
		}
		result.Updated++
		existing = update
	}

	return existing, nil
}

func managedClientAttributes() map[string]string {
	return map[string]string{
		managedAttribute:                           "true",
		"realm_client":                             "false",
		"backchannel.logout.session.required":      "true",
		"backchannel.logout.revoke.offline.tokens": "false",
	}
}

func reconcileInteractiveClients(ctx context.Context, session *adminSession, state DesiredState, credentials map[string]ClientCredential, result *Result) ([]clientRepresentation, error) {
	base := realmPath(state.Realm.Name)
	clients := make([]clientRepresentation, 0, len(state.InteractiveClients))
	for _, desired := range state.InteractiveClients {
		publicClient := desired.AccessType == "public"
		authenticationACR := state.Authentication.Levels[desired.AuthenticationLevel-1].ACR
		attributes := managedClientAttributes()
		attributes["access.token.signed.response.alg"] = "RS256"
		attributes["id.token.signed.response.alg"] = "RS256"
		attributes["pkce.code.challenge.method"] = "S256"
		// The default handles omitted acr_values; the minimum rejects caller downgrade.
		attributes["default.acr.values"] = authenticationACR
		attributes["minimum.acr.value"] = authenticationACR
		if len(desired.PostLogoutRedirectURIs) != 0 {
			attributes["post.logout.redirect.uris"] = strings.Join(desired.PostLogoutRedirectURIs, "##")
		}
		var secret string
		if !publicClient {
			credential, found := credentials[desired.Credential]
			if !found {
				return nil, fmt.Errorf("%w: client credential %q is required", ErrInvalidConfig, desired.Credential)
			}
			secret = credential.ClientSecret
			attributes[managedClientSecretHash] = secretHash(secret)
		}
		wanted := clientRepresentation{
			ClientID:                           desired.ClientID,
			Name:                               desired.Name,
			Enabled:                            true,
			Protocol:                           "openid-connect",
			ClientAuthenticatorType:            "client-secret",
			Secret:                             secret,
			BearerOnly:                         false,
			PublicClient:                       publicClient,
			ConsentRequired:                    false,
			StandardFlowEnabled:                true,
			ImplicitFlowEnabled:                false,
			DirectAccessGrantsEnabled:          false,
			ServiceAccountsEnabled:             false,
			AuthorizationServicesEnabled:       false,
			FullScopeAllowed:                   false,
			RedirectURIs:                       append([]string(nil), desired.RedirectURIs...),
			WebOrigins:                         append([]string{}, desired.WebOrigins...),
			Attributes:                         attributes,
			NodeReRegistrationTimeout:          -1,
			AuthenticationFlowBindingOverrides: map[string]string{},
		}
		existing, found, err := findClient(ctx, session, base, desired.ClientID)
		if err != nil {
			return nil, err
		}
		if !found {
			if err := session.post(ctx, base+"/clients", wanted); err != nil {
				return nil, fmt.Errorf("create interactive client %s: %w", desired.ClientID, err)
			}
			result.Created++
			existing, found, err = findClient(ctx, session, base, desired.ClientID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("%w: created client %s was not returned by Keycloak", ErrUnexpectedResponse, desired.ClientID)
			}
		}
		secretMatches := publicClient
		if !publicClient {
			secretMatches, err = clientSecretMatches(ctx, session, base, existing, secret)
			if err != nil {
				return nil, err
			}
		}
		if !clientMatches(existing, wanted) || !secretMatches {
			update := clientUpdate(existing, wanted)
			if err := session.put(ctx, base+"/clients/"+url.PathEscape(existing.ID), update); err != nil {
				return nil, fmt.Errorf("update interactive client %s: %w", desired.ClientID, err)
			}
			result.Updated++
			existing = update
		}
		if !secretMatches {
			if err := verifyClientSecret(ctx, session, base, existing, secret); err != nil {
				return nil, err
			}
		}
		mappers := []protocolMapperRepresentation{audienceMapper(state.ResourceClient.ClientID), subjectMapper()}
		optionalScopes := []string{state.OrganizationClaim.ClientScope}
		if desired.ClientID == walletAuthorizerClientID {
			mappers = nil
			optionalScopes = nil
		}
		if err := reconcileExactClientProtocolMappers(ctx, session, state.Realm.Name, existing, mappers, result); err != nil {
			return nil, err
		}
		if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, existing, "default", []string{"acr"}, result); err != nil {
			return nil, err
		}
		if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, existing, "optional", optionalScopes, result); err != nil {
			return nil, err
		}
		clients = append(clients, existing)
	}
	return clients, nil
}

func reconcileServiceClients(ctx context.Context, session *adminSession, state DesiredState, credentials map[string]ClientCredential, result *Result) ([]clientRepresentation, error) {
	base := realmPath(state.Realm.Name)
	clients := make([]clientRepresentation, 0, len(state.ServiceClients))
	for _, desired := range state.ServiceClients {
		credential := credentials[desired.Credential]
		attributes := managedClientAttributes()
		attributes[managedClientSecretHash] = secretHash(credential.ClientSecret)
		wanted := clientRepresentation{
			ClientID:                           desired.ClientID,
			Name:                               desired.Name,
			Enabled:                            true,
			Protocol:                           "openid-connect",
			ClientAuthenticatorType:            "client-secret",
			Secret:                             credential.ClientSecret,
			ServiceAccountsEnabled:             true,
			Attributes:                         attributes,
			RedirectURIs:                       []string{},
			WebOrigins:                         []string{},
			NodeReRegistrationTimeout:          -1,
			AuthenticationFlowBindingOverrides: map[string]string{},
		}
		existing, found, err := findClient(ctx, session, base, desired.ClientID)
		if err != nil {
			return nil, err
		}
		if !found {
			if err := session.post(ctx, base+"/clients", wanted); err != nil {
				return nil, fmt.Errorf("create service client %s: %w", desired.ClientID, err)
			}
			result.Created++
			existing, found, err = findClient(ctx, session, base, desired.ClientID)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("%w: created service client %s was not returned by Keycloak", ErrUnexpectedResponse, desired.ClientID)
			}
		}
		secretMatches, err := clientSecretMatches(ctx, session, base, existing, credential.ClientSecret)
		if err != nil {
			return nil, err
		}
		if !clientMatches(existing, wanted) || !secretMatches {
			update := clientUpdate(existing, wanted)
			if err := session.put(ctx, base+"/clients/"+url.PathEscape(existing.ID), update); err != nil {
				return nil, fmt.Errorf("update service client %s: %w", desired.ClientID, err)
			}
			result.Updated++
			existing = update
		}
		if !secretMatches {
			if err := verifyClientSecret(ctx, session, base, existing, credential.ClientSecret); err != nil {
				return nil, err
			}
		}
		if err := reconcileExactClientProtocolMappers(ctx, session, state.Realm.Name, existing, []protocolMapperRepresentation{
			audienceMapper(desired.Audience), permissionsMapper(desired.Permissions),
		}, result); err != nil {
			return nil, err
		}
		if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, existing, "default", nil, result); err != nil {
			return nil, err
		}
		if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, existing, "optional", nil, result); err != nil {
			return nil, err
		}
		if err := reconcileEmptyClientRoleScopeMappings(ctx, session, state.Realm.Name, existing, result); err != nil {
			return nil, err
		}
		clients = append(clients, existing)
	}
	return clients, nil
}

func clientSecretMatches(ctx context.Context, session *adminSession, realmBase string, client clientRepresentation, wanted string) (bool, error) {
	var credential credentialRepresentation
	found, err := session.get(ctx, realmBase+"/clients/"+url.PathEscape(client.ID)+"/client-secret", &credential)
	if err != nil {
		return false, fmt.Errorf("read client secret for %s: %w", client.ClientID, err)
	}
	if !found || credential.Type != "secret" {
		return false, fmt.Errorf("%w: client secret for %s was not returned", ErrUnexpectedResponse, client.ClientID)
	}
	return secretHash(credential.Value) == secretHash(wanted), nil
}

func verifyClientSecret(ctx context.Context, session *adminSession, realmBase string, client clientRepresentation, wanted string) error {
	matches, err := clientSecretMatches(ctx, session, realmBase, client, wanted)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("%w: client secret for %s differs after reconciliation", ErrUnexpectedResponse, client.ClientID)
	}
	return nil
}

func audienceMapper(audience string) protocolMapperRepresentation {
	return protocolMapperRepresentation{
		Name:            audienceMapperName,
		Protocol:        "openid-connect",
		ProtocolMapper:  audienceMapperID,
		ConsentRequired: false,
		Config: map[string]string{
			"included.client.audience":  audience,
			"id.token.claim":            "false",
			"access.token.claim":        "true",
			"introspection.token.claim": "false",
			"userinfo.token.claim":      "false",
			managedAttribute:            "true",
		},
	}
}

func permissionsMapper(permissions []string) protocolMapperRepresentation {
	value, _ := json.Marshal(permissions)
	return protocolMapperRepresentation{
		Name:            "temporal-permissions",
		Protocol:        "openid-connect",
		ProtocolMapper:  "oidc-hardcoded-claim-mapper",
		ConsentRequired: false,
		Config: map[string]string{
			"claim.name":                "permissions",
			"claim.value":               string(value),
			"jsonType.label":            "JSON",
			"id.token.claim":            "false",
			"access.token.claim":        "true",
			"introspection.token.claim": "false",
			"userinfo.token.claim":      "false",
			managedAttribute:            "true",
		},
	}
}

func subjectMapper() protocolMapperRepresentation {
	return protocolMapperRepresentation{
		Name:            subjectMapperName,
		Protocol:        "openid-connect",
		ProtocolMapper:  subjectMapperID,
		ConsentRequired: false,
		Config: map[string]string{
			"access.token.claim":        "true",
			"introspection.token.claim": "false",
			managedAttribute:            "true",
		},
	}
}

func reconcileExactClientProtocolMappers(ctx context.Context, session *adminSession, realm string, client clientRepresentation, desired []protocolMapperRepresentation, result *Result) error {
	path := realmPath(realm) + "/clients/" + url.PathEscape(client.ID) + "/protocol-mappers/models"
	var mappers []protocolMapperRepresentation
	if _, err := session.get(ctx, path, &mappers); err != nil {
		return fmt.Errorf("list protocol mappers for %s: %w", client.ClientID, err)
	}
	sort.Slice(mappers, func(i, j int) bool {
		return mappers[i].ID < mappers[j].ID
	})
	desiredByName := make(map[string]protocolMapperRepresentation, len(desired))
	for _, mapper := range desired {
		desiredByName[mapper.Name] = mapper
	}
	retained := make(map[string]bool, len(desired))
	for _, mapper := range mappers {
		if mapper.ID == "" {
			return fmt.Errorf("%w: protocol mapper without an id on client %s", ErrUnexpectedResponse, client.ClientID)
		}
		wanted, keep := desiredByName[mapper.Name]
		if keep && !retained[mapper.Name] {
			retained[mapper.Name] = true
			if !mapperMatches(mapper, wanted) {
				wanted.ID = mapper.ID
				if err := session.put(ctx, path+"/"+url.PathEscape(mapper.ID), wanted); err != nil {
					return fmt.Errorf("update protocol mapper %s on %s: %w", mapper.Name, client.ClientID, err)
				}
				result.Updated++
			}
			continue
		}
		if err := session.delete(ctx, path+"/"+url.PathEscape(mapper.ID), nil); err != nil {
			return fmt.Errorf("delete protocol mapper %s from %s: %w", mapper.Name, client.ClientID, err)
		}
		result.Deleted++
	}
	for _, wanted := range desired {
		if retained[wanted.Name] {
			continue
		}
		if err := session.post(ctx, path, wanted); err != nil {
			return fmt.Errorf("create protocol mapper %s on %s: %w", wanted.Name, client.ClientID, err)
		}
		result.Created++
	}
	return nil
}

func reconcileExactClientScopes(ctx context.Context, session *adminSession, realm string, client clientRepresentation, assignment string, desiredNames []string, result *Result) error {
	realmBase := realmPath(realm)
	var scopes []clientScopeRepresentation
	if _, err := session.get(ctx, realmBase+"/client-scopes", &scopes); err != nil {
		return fmt.Errorf("list client scopes: %w", err)
	}
	byName := make(map[string]clientScopeRepresentation, len(scopes))
	for _, scope := range scopes {
		byName[scope.Name] = scope
	}
	desired := make(map[string]clientScopeRepresentation, len(desiredNames))
	for _, name := range desiredNames {
		scope, exists := byName[name]
		if !exists {
			return fmt.Errorf("%w: built-in client scope %q does not exist", ErrUnexpectedResponse, name)
		}
		desired[name] = scope
	}
	if assignment != "default" && assignment != "optional" {
		return fmt.Errorf("%w: unsupported client scope assignment %q", ErrInvalidDesiredState, assignment)
	}
	path := realmBase + "/clients/" + url.PathEscape(client.ID) + "/" + assignment + "-client-scopes"
	var assigned []clientScopeRepresentation
	if _, err := session.get(ctx, path, &assigned); err != nil {
		return fmt.Errorf("list %s client scopes for %s: %w", assignment, client.ClientID, err)
	}
	assignedNames := make(map[string]bool, len(assigned))
	sort.Slice(assigned, func(i, j int) bool { return assigned[i].Name < assigned[j].Name })
	for _, scope := range assigned {
		assignedNames[scope.Name] = true
		if _, keep := desired[scope.Name]; keep {
			continue
		}
		if err := session.delete(ctx, path+"/"+url.PathEscape(scope.ID), nil); err != nil {
			return fmt.Errorf("remove %s client scope %s from %s: %w", assignment, scope.Name, client.ClientID, err)
		}
		result.Updated++
	}
	for _, name := range desiredNames {
		if assignedNames[name] {
			continue
		}
		if err := session.put(ctx, path+"/"+url.PathEscape(desired[name].ID), nil); err != nil {
			return fmt.Errorf("assign %s client scope %s to %s: %w", assignment, name, client.ClientID, err)
		}
		result.Updated++
	}
	return nil
}

func reconcileExactClients(ctx context.Context, session *adminSession, state DesiredState, interactive, service []clientRepresentation, resource, reconciler clientRepresentation, result *Result) error {
	keep := map[string]struct{}{resource.ClientID: {}, reconciler.ClientID: {}}
	for _, client := range interactive {
		keep[client.ClientID] = struct{}{}
	}
	for _, client := range service {
		keep[client.ClientID] = struct{}{}
	}
	base := realmPath(state.Realm.Name)
	var clients []clientRepresentation
	if _, err := session.get(ctx, base+"/clients?first=0&max=1000", &clients); err != nil {
		return fmt.Errorf("list clients: %w", err)
	}
	builtins := keycloakBuiltinClientSpecs(state.Realm.Name)
	foundBuiltins := make(map[string]clientRepresentation, len(builtins))
	for _, client := range clients {
		if isKeycloakBuiltinClient(client.ClientID) {
			foundBuiltins[client.ClientID] = client
		}
	}
	for _, builtin := range builtins {
		current, found := foundBuiltins[builtin.client.ClientID]
		if !found {
			return fmt.Errorf("%w: Keycloak 26.7 built-in client %s is missing", ErrUnexpectedResponse, builtin.client.ClientID)
		}
		if current.ID == "" {
			return fmt.Errorf("%w: Keycloak 26.7 built-in client %s has no id", ErrUnexpectedResponse, builtin.client.ClientID)
		}
		if !clientMatches(current, builtin.client) {
			update := clientUpdate(current, builtin.client)
			if err := session.put(ctx, base+"/clients/"+url.PathEscape(current.ID), update); err != nil {
				return fmt.Errorf("restore Keycloak built-in client %s: %w", builtin.client.ClientID, err)
			}
			result.Updated++
			current = update
			foundBuiltins[builtin.client.ClientID] = update
		}
		if err := reconcileExactClientProtocolMappers(ctx, session, state.Realm.Name, current, builtin.protocolMappers, result); err != nil {
			return err
		}
		if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, current, "default", builtin.defaultScopes, result); err != nil {
			return err
		}
		if err := reconcileExactClientScopes(ctx, session, state.Realm.Name, current, "optional", builtin.optionalScopes, result); err != nil {
			return err
		}
		if err := reconcileBuiltinClientScopeMappings(ctx, session, base, current, foundBuiltins, builtin.scopeRoleMappings, result); err != nil {
			return err
		}
	}
	for _, client := range clients {
		if isKeycloakBuiltinClient(client.ClientID) {
			continue
		}
		if _, exists := keep[client.ClientID]; exists {
			continue
		}
		if err := session.delete(ctx, base+"/clients/"+url.PathEscape(client.ID), nil); err != nil {
			return fmt.Errorf("delete client %s outside desired state: %w", client.ClientID, err)
		}
		result.Deleted++
	}
	return nil
}

func keycloakBuiltinClientIDs() []string {
	specs := keycloakBuiltinClientSpecs("noebs")
	clientIDs := make([]string, 0, len(specs))
	for _, spec := range specs {
		clientIDs = append(clientIDs, spec.client.ClientID)
	}
	return clientIDs
}

func isKeycloakBuiltinClient(clientID string) bool {
	switch clientID {
	case "account", "account-console", "admin-cli", "broker", "realm-management", "security-admin-console":
		return true
	default:
		return false
	}
}

type builtinClientScopeRoleMapping struct {
	clientID string
	roles    []string
}

type builtinClientSpec struct {
	client            clientRepresentation
	protocolMappers   []protocolMapperRepresentation
	defaultScopes     []string
	optionalScopes    []string
	scopeRoleMappings []builtinClientScopeRoleMapping
}

func keycloakBuiltinClientSpecs(realm string) []builtinClientSpec {
	defaultScopes := []string{"web-origins", "acr", "roles", "profile", "basic", "email"}
	optionalScopes := []string{"address", "phone", "organization", "offline_access", "microprofile-jwt"}
	base := func(clientID, name string) clientRepresentation {
		return clientRepresentation{
			ClientID:                           clientID,
			Name:                               name,
			Enabled:                            true,
			Protocol:                           "openid-connect",
			ClientAuthenticatorType:            "client-secret",
			RedirectURIs:                       []string{},
			WebOrigins:                         []string{},
			Attributes:                         map[string]string{},
			AuthenticationFlowBindingOverrides: map[string]string{},
		}
	}
	withScopes := func(client clientRepresentation) builtinClientSpec {
		return builtinClientSpec{
			client:         client,
			defaultScopes:  append([]string(nil), defaultScopes...),
			optionalScopes: append([]string(nil), optionalScopes...),
		}
	}
	inert := func(clientID, name string) builtinClientSpec {
		// Keycloak 26.7 rejects deletion of its fixed clients. Own them as
		// disabled shells with no flows, scopes, mappers, or role mappings.
		client := base(clientID, name)
		client.Enabled = false
		client.Attributes = map[string]string{"realm_client": "false"}
		return builtinClientSpec{client: client}
	}

	broker := base("broker", "${client_broker}")
	broker.BearerOnly = true
	broker.StandardFlowEnabled = true
	broker.Attributes = map[string]string{"realm_client": "true"}

	realmManagement := broker
	realmManagement.ClientID = "realm-management"
	realmManagement.Name = "${client_realm-management}"
	realmManagement.Attributes = map[string]string{"realm_client": "true"}

	brokerSpec := withScopes(broker)
	realmManagementSpec := withScopes(realmManagement)
	return []builtinClientSpec{
		inert("account", "${client_account}"),
		inert("account-console", "${client_account-console}"),
		inert("admin-cli", "${client_admin-cli}"),
		brokerSpec,
		realmManagementSpec,
		inert("security-admin-console", "${client_security-admin-console}"),
	}
}

func reconcileBuiltinClientScopeMappings(ctx context.Context, session *adminSession, realmBase string, client clientRepresentation, clients map[string]clientRepresentation, desired []builtinClientScopeRoleMapping, result *Result) error {
	base := realmBase + "/clients/" + url.PathEscape(client.ID) + "/scope-mappings"
	var mappings roleMappingsRepresentation
	if _, err := session.get(ctx, base, &mappings); err != nil {
		return fmt.Errorf("list role scope mappings for Keycloak built-in client %s: %w", client.ClientID, err)
	}
	keep := make(map[string]struct{}, len(desired))
	for _, mapping := range desired {
		keep[mapping.clientID] = struct{}{}
	}
	if err := pruneRoleMappings(ctx, session, base, mappings, keep, result); err != nil {
		return fmt.Errorf("prune role scope mappings for Keycloak built-in client %s: %w", client.ClientID, err)
	}
	for _, mapping := range desired {
		target, found := clients[mapping.clientID]
		if !found || target.ID == "" {
			return fmt.Errorf("%w: scope target %s for Keycloak built-in client %s is missing", ErrUnexpectedResponse, mapping.clientID, client.ClientID)
		}
		roles := make(map[string]roleRepresentation, len(mapping.roles))
		for _, roleName := range mapping.roles {
			var role roleRepresentation
			found, err := session.get(ctx, realmBase+"/clients/"+url.PathEscape(target.ID)+"/roles/"+url.PathEscape(roleName), &role)
			if err != nil {
				return fmt.Errorf("read built-in client role %s/%s: %w", mapping.clientID, roleName, err)
			}
			if !found {
				return fmt.Errorf("%w: Keycloak built-in client role %s/%s is missing", ErrUnexpectedResponse, mapping.clientID, roleName)
			}
			roles[roleName] = role
		}
		path := base + "/clients/" + url.PathEscape(target.ID)
		if err := reconcileExactRoles(ctx, session, path, mappings.ClientMappings[mapping.clientID].Mappings, mapping.roles, roles, result); err != nil {
			return fmt.Errorf("reconcile role scope mapping %s on Keycloak built-in client %s: %w", mapping.clientID, client.ClientID, err)
		}
	}
	return nil
}

func reconcileInteractiveRoleScopeMappings(ctx context.Context, session *adminSession, state DesiredState, clients []clientRepresentation, result *Result) error {
	for _, client := range clients {
		if err := reconcileEmptyClientRoleScopeMappings(ctx, session, state.Realm.Name, client, result); err != nil {
			return err
		}
	}
	return nil
}

func reconcileEmptyClientRoleScopeMappings(ctx context.Context, session *adminSession, realm string, client clientRepresentation, result *Result) error {
	base := realmPath(realm) + "/clients/" + url.PathEscape(client.ID) + "/scope-mappings"
	var mappings roleMappingsRepresentation
	if _, err := session.get(ctx, base, &mappings); err != nil {
		return fmt.Errorf("list role scope mappings for %s: %w", client.ClientID, err)
	}
	if err := pruneRoleMappings(ctx, session, base, mappings, nil, result); err != nil {
		return fmt.Errorf("remove role scope mappings from %s: %w", client.ClientID, err)
	}
	return nil
}

func findClient(ctx context.Context, session *adminSession, realmBase, clientID string) (clientRepresentation, bool, error) {
	path := realmBase + "/clients?clientId=" + url.QueryEscape(clientID)
	var clients []clientRepresentation
	if _, err := session.get(ctx, path, &clients); err != nil {
		return clientRepresentation{}, false, fmt.Errorf("find client %s: %w", clientID, err)
	}
	for _, client := range clients {
		if client.ClientID == clientID {
			return client, true, nil
		}
	}
	return clientRepresentation{}, false, nil
}

func reconcileClientRoles(ctx context.Context, session *adminSession, state DesiredState, client clientRepresentation, result *Result) (map[string]roleRepresentation, error) {
	base := realmPath(state.Realm.Name) + "/clients/" + url.PathEscape(client.ID) + "/roles"
	var existing []roleRepresentation
	if _, err := session.get(ctx, base+"?briefRepresentation=false", &existing); err != nil {
		return nil, fmt.Errorf("list client roles for %s: %w", client.ClientID, err)
	}
	existingByName := make(map[string]roleRepresentation, len(existing))
	for _, role := range existing {
		existingByName[role.Name] = role
	}
	desired := make(map[string]roleRepresentation, len(state.ResourceClient.Roles))
	for _, role := range state.ResourceClient.Roles {
		wanted := roleRepresentation{Name: role.Name, Description: managedDescription(role.Description)}
		desired[role.Name] = wanted
		current, found := existingByName[role.Name]
		if !found {
			if err := session.post(ctx, base, wanted); err != nil {
				return nil, fmt.Errorf("create client role %s: %w", role.Name, err)
			}
			result.Created++
			continue
		}
		if current.Description != wanted.Description {
			wanted.ID = current.ID
			if err := session.put(ctx, base+"/"+url.PathEscape(role.Name), wanted); err != nil {
				return nil, fmt.Errorf("update client role %s: %w", role.Name, err)
			}
			result.Updated++
		}
	}
	for _, role := range existing {
		if _, keep := desired[role.Name]; !keep {
			if err := session.delete(ctx, base+"/"+url.PathEscape(role.Name), nil); err != nil {
				return nil, fmt.Errorf("delete client role %s: %w", role.Name, err)
			}
			result.Deleted++
		}
	}

	existing = nil
	if _, err := session.get(ctx, base+"?briefRepresentation=false", &existing); err != nil {
		return nil, fmt.Errorf("reload client roles for %s: %w", client.ClientID, err)
	}
	roles := make(map[string]roleRepresentation, len(existing))
	for _, role := range existing {
		if _, wanted := desired[role.Name]; wanted {
			var composites []roleRepresentation
			compositePath := base + "/" + url.PathEscape(role.Name) + "/composites"
			if _, err := session.get(ctx, compositePath, &composites); err != nil {
				return nil, fmt.Errorf("list composites for client role %s: %w", role.Name, err)
			}
			if len(composites) != 0 {
				if err := session.delete(ctx, compositePath, composites); err != nil {
					return nil, fmt.Errorf("remove composites from client role %s: %w", role.Name, err)
				}
				result.Updated++
				role.Composite = false
			}
			roles[role.Name] = role
		}
	}
	if len(roles) != len(desired) {
		return nil, fmt.Errorf("%w: Keycloak did not return all desired roles for client %s", ErrUnexpectedResponse, client.ClientID)
	}
	return roles, nil
}

func reconcileOrganizationMapper(ctx context.Context, session *adminSession, state DesiredState, result *Result) error {
	base := realmPath(state.Realm.Name)
	var scopes []clientScopeRepresentation
	if _, err := session.get(ctx, base+"/client-scopes", &scopes); err != nil {
		return fmt.Errorf("list client scopes: %w", err)
	}
	var scope clientScopeRepresentation
	for _, candidate := range scopes {
		if candidate.Name == state.OrganizationClaim.ClientScope {
			scope = candidate
			break
		}
	}
	if scope.ID == "" {
		return fmt.Errorf("%w: built-in client scope %q does not exist", ErrUnexpectedResponse, state.OrganizationClaim.ClientScope)
	}
	path := base + "/client-scopes/" + url.PathEscape(scope.ID) + "/protocol-mappers/models"
	var mappers []protocolMapperRepresentation
	if _, err := session.get(ctx, path, &mappers); err != nil {
		return fmt.Errorf("list protocol mappers on scope %s: %w", scope.Name, err)
	}
	sort.Slice(mappers, func(i, j int) bool { return mappers[i].ID < mappers[j].ID })
	membershipIndex := -1
	for index, mapper := range mappers {
		if mapper.ID == "" {
			return fmt.Errorf("%w: protocol mapper without an id on scope %s", ErrUnexpectedResponse, scope.Name)
		}
		if mapper.ProtocolMapper != organizationMapperID {
			continue
		}
		if membershipIndex == -1 || (mapper.Name == organizationMapperName && mappers[membershipIndex].Name != organizationMapperName) {
			membershipIndex = index
		}
	}
	if membershipIndex == -1 {
		return fmt.Errorf("%w: organization scope is missing its built-in %s mapper", ErrUnexpectedResponse, organizationMapperID)
	}
	wantedMembership := organizationMembershipMapper()
	wantedMembership.ID = mappers[membershipIndex].ID
	if !mapperMatches(mappers[membershipIndex], wantedMembership) {
		if err := session.put(ctx, path+"/"+url.PathEscape(wantedMembership.ID), wantedMembership); err != nil {
			return fmt.Errorf("update built-in organization membership mapper: %w", err)
		}
		result.Updated++
	}
	config := make(map[string]string, len(state.OrganizationClaim.Config)+1)
	for key, value := range state.OrganizationClaim.Config {
		config[key] = value
	}
	config[managedAttribute] = "true"
	wanted := protocolMapperRepresentation{
		Name:            state.OrganizationClaim.MapperName,
		Protocol:        "openid-connect",
		ProtocolMapper:  state.OrganizationClaim.ProtocolMapper,
		ConsentRequired: false,
		Config:          config,
	}
	foundIndex := -1
	for index, mapper := range mappers {
		if index == membershipIndex || mapper.Name != wanted.Name {
			continue
		}
		foundIndex = index
		break
	}
	if foundIndex != -1 {
		mapper := mappers[foundIndex]
		wanted.ID = mapper.ID
		if !mapperMatches(mapper, wanted) {
			if err := session.put(ctx, path+"/"+url.PathEscape(mapper.ID), wanted); err != nil {
				return fmt.Errorf("update organization mapper %s: %w", wanted.Name, err)
			}
			result.Updated++
		}
	} else {
		if err := session.post(ctx, path, wanted); err != nil {
			return fmt.Errorf("create organization mapper %s: %w", wanted.Name, err)
		}
		result.Created++
	}
	for index, mapper := range mappers {
		if index == membershipIndex || index == foundIndex {
			continue
		}
		if err := session.delete(ctx, path+"/"+url.PathEscape(mapper.ID), nil); err != nil {
			return fmt.Errorf("delete protocol mapper %s from organization scope: %w", mapper.Name, err)
		}
		result.Deleted++
	}
	return nil
}

func organizationMembershipMapper() protocolMapperRepresentation {
	return protocolMapperRepresentation{
		Name:            organizationMapperName,
		Protocol:        "openid-connect",
		ProtocolMapper:  organizationMapperID,
		ConsentRequired: false,
		Config: map[string]string{
			"claim.name":                "organization",
			"jsonType.label":            "JSON",
			"id.token.claim":            "false",
			"access.token.claim":        "true",
			"lightweight.claim":         "false",
			"userinfo.token.claim":      "false",
			"introspection.token.claim": "false",
			"multivalued":               "true",
			"addOrganizationId":         "true",
			"addOrganizationAttributes": "false",
			"addOrganizationDomain":     "false",
		},
	}
}

func reconcileIdentityProviders(ctx context.Context, session *adminSession, state DesiredState, credentials map[string]IdentityProviderCredential, result *Result) error {
	base := realmPath(state.Realm.Name) + "/identity-provider/instances"
	var existing []identityProviderRepresentation
	if _, err := session.get(ctx, base, &existing); err != nil {
		return fmt.Errorf("list identity providers: %w", err)
	}
	byAlias := make(map[string]identityProviderRepresentation, len(existing))
	for _, provider := range existing {
		byAlias[provider.Alias] = provider
	}
	desiredAliases := make(map[string]struct{}, len(state.IdentityProviders))
	for _, desired := range state.IdentityProviders {
		desiredAliases[desired.Alias] = struct{}{}
		credential, found := credentials[desired.Credential]
		if !found {
			return fmt.Errorf("%w: identity provider credential %q is required", ErrInvalidConfig, desired.Credential)
		}
		config := cloneStringMap(desired.Config)
		config["clientId"] = credential.ClientID
		config["clientSecret"] = credential.ClientSecret
		config[managedAttribute] = "true"
		config[managedCredentialHash] = credentialHash(credential)
		wanted := identityProviderRepresentation{
			Alias:                     desired.Alias,
			DisplayName:               desired.DisplayName,
			ProviderID:                desired.ProviderID,
			Enabled:                   true,
			TrustEmail:                true,
			StoreToken:                false,
			AddReadTokenRoleOnCreate:  false,
			AuthenticateByDefault:     false,
			LinkOnly:                  false,
			HideOnLogin:               false,
			FirstBrokerLoginFlowAlias: state.Authentication.FirstBrokerLoginFlow,
			PostBrokerLoginFlowAlias:  state.Authentication.PostBrokerLoginFlow,
			Config:                    config,
		}
		current, exists := byAlias[desired.Alias]
		if !exists {
			if err := session.post(ctx, base, wanted); err != nil {
				return fmt.Errorf("create identity provider %s: %w", desired.Alias, err)
			}
			result.Created++
		} else {
			// Keycloak deliberately masks broker secrets in every Admin REST read.
			// Reasserting the secret is the only API-level way to make it authoritative.
			if err := session.put(ctx, base+"/"+url.PathEscape(desired.Alias), wanted); err != nil {
				return fmt.Errorf("update identity provider %s: %w", desired.Alias, err)
			}
			if !identityProviderMatches(current, wanted) {
				result.Updated++
			}
		}
		if err := reconcileEmptyIdentityProviderMappers(ctx, session, base, desired.Alias, result); err != nil {
			return err
		}
	}
	for _, provider := range existing {
		if _, keep := desiredAliases[provider.Alias]; keep {
			continue
		}
		if err := session.delete(ctx, base+"/"+url.PathEscape(provider.Alias), nil); err != nil {
			return fmt.Errorf("delete identity provider %s: %w", provider.Alias, err)
		}
		result.Deleted++
	}
	return nil
}

func reconcileEmptyIdentityProviderMappers(ctx context.Context, session *adminSession, base, alias string, result *Result) error {
	path := base + "/" + url.PathEscape(alias) + "/mappers"
	var mappers []identityProviderMapperRepresentation
	if _, err := session.get(ctx, path, &mappers); err != nil {
		return fmt.Errorf("list identity provider mappers for %s: %w", alias, err)
	}
	sort.Slice(mappers, func(i, j int) bool { return mappers[i].ID < mappers[j].ID })
	for _, mapper := range mappers {
		if mapper.ID == "" {
			return fmt.Errorf("%w: identity provider mapper %s has no id", ErrUnexpectedResponse, mapper.Name)
		}
		if err := session.delete(ctx, path+"/"+url.PathEscape(mapper.ID), nil); err != nil {
			return fmt.Errorf("delete identity provider mapper %s from %s: %w", mapper.Name, alias, err)
		}
		result.Deleted++
	}
	return nil
}

func reconcileOrganizations(ctx context.Context, session *adminSession, state DesiredState, client clientRepresentation, clientRoles map[string]roleRepresentation, result *Result) error {
	base := realmPath(state.Realm.Name)
	organizations, err := listOrganizations(ctx, session, base)
	if err != nil {
		return err
	}
	byAlias := make(map[string]organizationRepresentation, len(organizations))
	for _, organization := range organizations {
		byAlias[organization.Alias] = organization
	}
	desiredAliases := make(map[string]struct{}, len(state.Organizations))
	for _, desired := range state.Organizations {
		desiredAliases[desired.Alias] = struct{}{}
		wanted := organizationRepresentation{
			Name:       desired.Name,
			Alias:      desired.Alias,
			Enabled:    true,
			Attributes: managedAttributes(desired.Attributes),
		}
		existing, found := byAlias[desired.Alias]
		if !found {
			if err := session.post(ctx, base+"/organizations", wanted); err != nil {
				return fmt.Errorf("create organization %s: %w", desired.Alias, err)
			}
			result.Created++
			organizations, err = listOrganizations(ctx, session, base)
			if err != nil {
				return err
			}
			existing = organizationRepresentation{}
			for _, candidate := range organizations {
				if candidate.Alias == desired.Alias {
					existing = candidate
					break
				}
			}
			if existing.ID == "" {
				return fmt.Errorf("%w: created organization %s was not returned by Keycloak", ErrUnexpectedResponse, desired.Alias)
			}
		} else if !organizationMatches(existing, wanted) {
			wanted.ID = existing.ID
			if err := session.put(ctx, base+"/organizations/"+url.PathEscape(existing.ID), wanted); err != nil {
				return fmt.Errorf("update organization %s: %w", desired.Alias, err)
			}
			result.Updated++
			existing = wanted
		}
		if err := reconcileOrganizationGroups(ctx, session, base, existing, desired, client, clientRoles, result); err != nil {
			return err
		}
	}
	for _, organization := range organizations {
		if _, keep := desiredAliases[organization.Alias]; !keep {
			if err := session.delete(ctx, base+"/organizations/"+url.PathEscape(organization.ID), nil); err != nil {
				return fmt.Errorf("delete organization %s: %w", organization.Alias, err)
			}
			result.Deleted++
		}
	}
	return nil
}

func listOrganizations(ctx context.Context, session *adminSession, realmBase string) ([]organizationRepresentation, error) {
	var organizations []organizationRepresentation
	if _, err := session.get(ctx, realmBase+"/organizations?briefRepresentation=false&first=0&max=1000", &organizations); err != nil {
		return nil, fmt.Errorf("list organizations: %w", err)
	}
	return organizations, nil
}

func reconcileOrganizationGroups(ctx context.Context, session *adminSession, realmBase string, organization organizationRepresentation, desired Organization, client clientRepresentation, clientRoles map[string]roleRepresentation, result *Result) error {
	base := realmBase + "/organizations/" + url.PathEscape(organization.ID) + "/groups"
	var groups []groupRepresentation
	if _, err := session.get(ctx, base+"?briefRepresentation=false&populateHierarchy=false&first=0&max=1000", &groups); err != nil {
		return fmt.Errorf("list groups for organization %s: %w", desired.Alias, err)
	}
	byName := make(map[string]groupRepresentation, len(groups))
	for _, group := range groups {
		byName[group.Name] = group
	}
	desiredNames := make(map[string]struct{}, len(desired.Groups))
	for _, desiredGroup := range desired.Groups {
		desiredNames[desiredGroup.Name] = struct{}{}
		wanted := groupRepresentation{
			Name:        desiredGroup.Name,
			Description: desiredGroup.Description,
			Attributes:  managedAttributes(desiredGroup.Attributes),
		}
		existing, found := byName[desiredGroup.Name]
		if !found {
			if err := session.post(ctx, base, wanted); err != nil {
				return fmt.Errorf("create group %s in organization %s: %w", desiredGroup.Name, desired.Alias, err)
			}
			result.Created++
			groups = nil
			if _, err := session.get(ctx, base+"?briefRepresentation=false&populateHierarchy=false&first=0&max=1000", &groups); err != nil {
				return fmt.Errorf("reload groups for organization %s: %w", desired.Alias, err)
			}
			existing = groupRepresentation{}
			for _, candidate := range groups {
				if candidate.Name == desiredGroup.Name {
					existing = candidate
					break
				}
			}
			if existing.ID == "" {
				return fmt.Errorf("%w: created group %s was not returned by Keycloak", ErrUnexpectedResponse, desiredGroup.Name)
			}
		} else if !groupMatches(existing, wanted) {
			wanted.ID = existing.ID
			if err := session.put(ctx, base+"/"+url.PathEscape(existing.ID), wanted); err != nil {
				return fmt.Errorf("update group %s in organization %s: %w", desiredGroup.Name, desired.Alias, err)
			}
			result.Updated++
			existing = wanted
		}
		if err := reconcileGroupRoleMappings(ctx, session, realmBase, organization.ID, existing.ID, desiredGroup, client, clientRoles, result); err != nil {
			return fmt.Errorf("reconcile roles for group %s in organization %s: %w", desiredGroup.Name, desired.Alias, err)
		}
		if err := deleteOrganizationGroupDescendants(ctx, session, base, existing, desired.Alias, result); err != nil {
			return err
		}
	}
	for _, group := range groups {
		if _, keep := desiredNames[group.Name]; !keep {
			if err := session.delete(ctx, base+"/"+url.PathEscape(group.ID), nil); err != nil {
				return fmt.Errorf("delete group %s in organization %s: %w", group.Name, desired.Alias, err)
			}
			result.Deleted++
		}
	}
	return nil
}

func deleteOrganizationGroupDescendants(ctx context.Context, session *adminSession, groupBase string, parent groupRepresentation, organizationAlias string, result *Result) error {
	children, err := listOrganizationGroupChildren(ctx, session, groupBase, parent)
	if err != nil {
		return fmt.Errorf("list descendants of group %s in organization %s: %w", parent.Name, organizationAlias, err)
	}
	sort.Slice(children, func(i, j int) bool { return children[i].ID < children[j].ID })
	for _, child := range children {
		if child.ID == "" {
			return fmt.Errorf("%w: child group %s in organization %s has no id", ErrUnexpectedResponse, child.Name, organizationAlias)
		}
		if err := deleteOrganizationGroupDescendants(ctx, session, groupBase, child, organizationAlias, result); err != nil {
			return err
		}
		if err := session.delete(ctx, groupBase+"/"+url.PathEscape(child.ID), nil); err != nil {
			return fmt.Errorf("delete undeclared child group %s in organization %s: %w", child.Name, organizationAlias, err)
		}
		result.Deleted++
	}
	return nil
}

func listOrganizationGroupChildren(ctx context.Context, session *adminSession, groupBase string, parent groupRepresentation) ([]groupRepresentation, error) {
	path := groupBase + "/" + url.PathEscape(parent.ID) + "/children?briefRepresentation=false&first=0&max=1000"
	var children []groupRepresentation
	found, err := session.get(ctx, path, &children)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: child-group endpoint is missing for group %s", ErrUnexpectedResponse, parent.Name)
	}
	return children, nil
}

func reconcileGroupRoleMappings(ctx context.Context, session *adminSession, realmBase, organizationID, groupID string, desired OrganizationGroup, client clientRepresentation, clientRoles map[string]roleRepresentation, result *Result) error {
	roleBase := realmBase + "/organizations/" + url.PathEscape(organizationID) + "/groups/" + url.PathEscape(groupID) + "/role-mappings"
	var mappings roleMappingsRepresentation
	if _, err := session.get(ctx, roleBase, &mappings); err != nil {
		return err
	}
	if err := pruneRoleMappings(ctx, session, roleBase, mappings, map[string]struct{}{client.ClientID: {}}, result); err != nil {
		return err
	}
	clientPath := roleBase + "/clients/" + url.PathEscape(client.ID)
	existing := mappings.ClientMappings[client.ClientID].Mappings
	desiredNames := make(map[string]struct{}, len(desired.ClientRoles))
	for _, role := range desired.ClientRoles {
		desiredNames[role] = struct{}{}
	}
	var remove []roleRepresentation
	existingNames := make(map[string]struct{}, len(existing))
	for _, role := range existing {
		existingNames[role.Name] = struct{}{}
		if _, keep := desiredNames[role.Name]; !keep {
			remove = append(remove, role)
		}
	}
	if len(remove) != 0 {
		if err := session.delete(ctx, clientPath, remove); err != nil {
			return err
		}
		result.Updated++
	}
	var add []roleRepresentation
	for _, roleName := range desired.ClientRoles {
		if _, exists := existingNames[roleName]; !exists {
			add = append(add, clientRoles[roleName])
		}
	}
	if len(add) != 0 {
		if err := session.post(ctx, clientPath, add); err != nil {
			return err
		}
		result.Updated++
	}

	return nil
}

func realmPath(realm string) string {
	return "/admin/realms/" + url.PathEscape(realm)
}

func managedDescription(description string) string {
	if description == "" {
		return managedRoleDescription
	}
	return managedRoleDescription + " " + description
}

func managedAttributes(attributes map[string][]string) map[string][]string {
	result := make(map[string][]string, len(attributes)+1)
	for key, values := range attributes {
		result[key] = append([]string(nil), values...)
	}
	result[managedAttribute] = []string{"true"}
	return result
}

func realmMatches(existing, wanted realmRepresentation) bool {
	if !equalStringPointerMap(existing.Attributes, wanted.Attributes) ||
		!equalStringMap(existing.BrowserSecurityHeaders, wanted.BrowserSecurityHeaders) {
		return false
	}
	existing.Attributes = nil
	wanted.Attributes = nil
	existing.BrowserSecurityHeaders = nil
	wanted.BrowserSecurityHeaders = nil
	return reflect.DeepEqual(existing, wanted)
}

func equalStringPointerMap(left, right map[string]*string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValue := range left {
		rightValue, found := right[key]
		if !found || leftValue == nil || rightValue == nil || *leftValue != *rightValue {
			return false
		}
	}
	return true
}

func realmUpdate(existing, wanted realmRepresentation) realmRepresentation {
	wanted.Attributes = cloneStringPointerMap(wanted.Attributes)
	for key := range existing.Attributes {
		if _, desired := wanted.Attributes[key]; !desired {
			wanted.Attributes[key] = nil
		}
	}
	return wanted
}

func stringPointerMap(source map[string]string) map[string]*string {
	result := make(map[string]*string, len(source))
	for key, value := range source {
		value := value
		result[key] = &value
	}
	return result
}

func cloneStringPointerMap(source map[string]*string) map[string]*string {
	result := make(map[string]*string, len(source))
	for key, value := range source {
		if value == nil {
			result[key] = nil
			continue
		}
		copy := *value
		result[key] = &copy
	}
	return result
}

func clientMatches(existing, wanted clientRepresentation) bool {
	if existing.ClientID != wanted.ClientID ||
		existing.Name != wanted.Name ||
		existing.Enabled != wanted.Enabled ||
		existing.Protocol != wanted.Protocol ||
		existing.ClientAuthenticatorType != wanted.ClientAuthenticatorType ||
		existing.BearerOnly != wanted.BearerOnly ||
		existing.PublicClient != wanted.PublicClient ||
		existing.ConsentRequired != wanted.ConsentRequired ||
		existing.StandardFlowEnabled != wanted.StandardFlowEnabled ||
		existing.ImplicitFlowEnabled != wanted.ImplicitFlowEnabled ||
		existing.DirectAccessGrantsEnabled != wanted.DirectAccessGrantsEnabled ||
		existing.ServiceAccountsEnabled != wanted.ServiceAccountsEnabled ||
		existing.AuthorizationServicesEnabled != wanted.AuthorizationServicesEnabled ||
		existing.FullScopeAllowed != wanted.FullScopeAllowed ||
		!equalStrings(existing.RedirectURIs, wanted.RedirectURIs) ||
		!equalStrings(existing.WebOrigins, wanted.WebOrigins) ||
		existing.RootURL != wanted.RootURL ||
		existing.BaseURL != wanted.BaseURL ||
		existing.AdminURL != wanted.AdminURL ||
		existing.SurrogateAuthRequired != wanted.SurrogateAuthRequired ||
		existing.AlwaysDisplayInConsole != wanted.AlwaysDisplayInConsole ||
		existing.FrontchannelLogout != wanted.FrontchannelLogout ||
		existing.NodeReRegistrationTimeout != wanted.NodeReRegistrationTimeout ||
		existing.NotBefore != wanted.NotBefore ||
		!equalStringMap(existing.AuthenticationFlowBindingOverrides, wanted.AuthenticationFlowBindingOverrides) {
		return false
	}
	return clientAttributesMatch(existing.Attributes, wanted.Attributes)
}

func clientAttributesMatch(existing, wanted map[string]string) bool {
	for key, value := range wanted {
		if existing[key] != value {
			return false
		}
	}
	for key, value := range existing {
		if _, desired := wanted[key]; desired {
			continue
		}
		if key != "client.secret.creation.time" {
			return false
		}
		created, err := strconv.ParseInt(value, 10, 64)
		if err != nil || created <= 0 {
			return false
		}
	}
	return true
}

func clientUpdate(existing, wanted clientRepresentation) clientRepresentation {
	wanted.ID = existing.ID
	wanted.Attributes = cloneStringMap(wanted.Attributes)
	for key, value := range existing.Attributes {
		if _, desired := wanted.Attributes[key]; desired || isKeycloakOwnedClientAttribute(key, value) {
			continue
		}
		// Keycloak merges ClientRepresentation attributes. An explicit empty value
		// removes an attribute; omitting it leaves stale security settings behind.
		wanted.Attributes[key] = ""
	}
	return wanted
}

func isKeycloakOwnedClientAttribute(key, value string) bool {
	if key != "client.secret.creation.time" {
		return false
	}
	created, err := strconv.ParseInt(value, 10, 64)
	return err == nil && created > 0
}

func mapperMatches(existing, wanted protocolMapperRepresentation) bool {
	return existing.Name == wanted.Name &&
		existing.Protocol == wanted.Protocol &&
		existing.ProtocolMapper == wanted.ProtocolMapper &&
		existing.ConsentRequired == wanted.ConsentRequired &&
		reflect.DeepEqual(existing.Config, wanted.Config)
}

func organizationMatches(existing, wanted organizationRepresentation) bool {
	return existing.Name == wanted.Name &&
		existing.Alias == wanted.Alias &&
		existing.Enabled == wanted.Enabled &&
		equalAttributes(existing.Attributes, wanted.Attributes)
}

func groupMatches(existing, wanted groupRepresentation) bool {
	return existing.Name == wanted.Name &&
		existing.Description == wanted.Description &&
		equalAttributes(existing.Attributes, wanted.Attributes)
}

func identityProviderMatches(existing, wanted identityProviderRepresentation) bool {
	if existing.Alias != wanted.Alias ||
		existing.DisplayName != wanted.DisplayName ||
		existing.ProviderID != wanted.ProviderID ||
		existing.Enabled != wanted.Enabled ||
		existing.TrustEmail != wanted.TrustEmail ||
		existing.StoreToken != wanted.StoreToken ||
		existing.AddReadTokenRoleOnCreate != wanted.AddReadTokenRoleOnCreate ||
		existing.AuthenticateByDefault != wanted.AuthenticateByDefault ||
		existing.LinkOnly != wanted.LinkOnly ||
		existing.HideOnLogin != wanted.HideOnLogin ||
		existing.FirstBrokerLoginFlowAlias != wanted.FirstBrokerLoginFlowAlias ||
		existing.PostBrokerLoginFlowAlias != wanted.PostBrokerLoginFlowAlias {
		return false
	}
	existingConfig := cloneStringMap(existing.Config)
	wantedConfig := cloneStringMap(wanted.Config)
	delete(existingConfig, "clientSecret")
	delete(wantedConfig, "clientSecret")
	return reflect.DeepEqual(existingConfig, wantedConfig)
}

func credentialHash(credential IdentityProviderCredential) string {
	sum := sha256.Sum256([]byte(credential.ClientID + "\x00" + credential.ClientSecret))
	return hex.EncodeToString(sum[:])
}

func secretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		if right[key] != value {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	return reflect.DeepEqual(leftCopy, rightCopy)
}

func equalAttributes(left, right map[string][]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, leftValues := range left {
		rightValues, exists := right[key]
		if !exists || len(leftValues) != len(rightValues) {
			return false
		}
		leftCopy := append([]string(nil), leftValues...)
		rightCopy := append([]string(nil), rightValues...)
		sort.Strings(leftCopy)
		sort.Strings(rightCopy)
		if !reflect.DeepEqual(leftCopy, rightCopy) {
			return false
		}
	}
	return true
}
