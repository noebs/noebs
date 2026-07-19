package keycloakadmin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"reflect"
	"sort"
	"strings"
)

const (
	managedAttribute        = "noebs.managed"
	managedCredentialHash   = "noebs.credential-sha256"
	managedClientSecretHash = "noebs.client-secret-sha256"
	managedRoleDescription  = "[managed-by:noebs]"
	organizationMapperID    = "oidc-organization-membership-mapper"
	audienceMapperID        = "oidc-audience-mapper"
	audienceMapperName      = "noebs-api-audience"
)

type realmRepresentation struct {
	Realm                 string `json:"realm"`
	DisplayName           string `json:"displayName,omitempty"`
	Enabled               bool   `json:"enabled"`
	OrganizationsEnabled  bool   `json:"organizationsEnabled"`
	RegistrationAllowed   bool   `json:"registrationAllowed"`
	AccessTokenLifespan   int    `json:"accessTokenLifespan"`
	SSOSessionIdleTimeout int    `json:"ssoSessionIdleTimeout"`
	SSOSessionMaxLifespan int    `json:"ssoSessionMaxLifespan"`
	RevokeRefreshToken    bool   `json:"revokeRefreshToken"`
	RefreshTokenMaxReuse  int    `json:"refreshTokenMaxReuse"`
}

type clientRepresentation struct {
	ID                           string            `json:"id,omitempty"`
	ClientID                     string            `json:"clientId"`
	Name                         string            `json:"name"`
	Enabled                      bool              `json:"enabled"`
	Protocol                     string            `json:"protocol"`
	ClientAuthenticatorType      string            `json:"clientAuthenticatorType"`
	Secret                       string            `json:"secret,omitempty"`
	BearerOnly                   bool              `json:"bearerOnly"`
	PublicClient                 bool              `json:"publicClient"`
	ConsentRequired              bool              `json:"consentRequired"`
	StandardFlowEnabled          bool              `json:"standardFlowEnabled"`
	ImplicitFlowEnabled          bool              `json:"implicitFlowEnabled"`
	DirectAccessGrantsEnabled    bool              `json:"directAccessGrantsEnabled"`
	ServiceAccountsEnabled       bool              `json:"serviceAccountsEnabled"`
	AuthorizationServicesEnabled bool              `json:"authorizationServicesEnabled"`
	FullScopeAllowed             bool              `json:"fullScopeAllowed"`
	RedirectURIs                 []string          `json:"redirectUris,omitempty"`
	WebOrigins                   []string          `json:"webOrigins"`
	Attributes                   map[string]string `json:"attributes"`
}

type roleRepresentation struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
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
	Config                    map[string]string `json:"config"`
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
	if err := ensureRealm(ctx, session, state.Realm, &result); err != nil {
		return Result{}, err
	}
	reconcilerClient, err := reconcileReconcilerClient(ctx, session, state, r.config.ClientCredentials, &result)
	if err != nil {
		return Result{}, err
	}
	if err := reconcileRealmRoles(ctx, session, state, &result); err != nil {
		return Result{}, err
	}
	client, err := reconcileResourceClient(ctx, session, state, &result)
	if err != nil {
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
	if err := reconcileInteractiveRealmScopes(ctx, session, state, interactiveClients, &result); err != nil {
		return Result{}, err
	}
	if err := pruneManagedClients(ctx, session, state, interactiveClients, client, reconcilerClient, &result); err != nil {
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
	return result, nil
}

func reconcileReconcilerClient(ctx context.Context, session *adminSession, state DesiredState, credentials map[string]ClientCredential, result *Result) (clientRepresentation, error) {
	desired := state.ReconcilerClient
	credential, found := credentials[desired.Credential]
	if !found {
		return clientRepresentation{}, fmt.Errorf("%w: client credential %q is required", ErrInvalidConfig, desired.Credential)
	}
	base := realmPath(state.Realm.Name)
	wanted := clientRepresentation{
		ClientID:                     desired.ClientID,
		Name:                         desired.Name,
		Enabled:                      true,
		Protocol:                     "openid-connect",
		ClientAuthenticatorType:      "client-secret",
		Secret:                       credential.ClientSecret,
		BearerOnly:                   false,
		PublicClient:                 false,
		ConsentRequired:              false,
		StandardFlowEnabled:          false,
		ImplicitFlowEnabled:          false,
		DirectAccessGrantsEnabled:    false,
		ServiceAccountsEnabled:       true,
		AuthorizationServicesEnabled: false,
		FullScopeAllowed:             false,
		Attributes: map[string]string{
			managedAttribute:        "true",
			managedClientSecretHash: secretHash(credential.ClientSecret),
		},
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
	} else if !clientMatches(existing, wanted) {
		wanted.ID = existing.ID
		if err := session.put(ctx, base+"/clients/"+url.PathEscape(existing.ID), wanted); err != nil {
			return clientRepresentation{}, fmt.Errorf("update reconciler client %s: %w", desired.ClientID, err)
		}
		result.Updated++
		existing = wanted
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
	scopePath := realmBase + "/clients/" + url.PathEscape(reconcilerClient.ID) + "/scope-mappings/clients/" + url.PathEscape(managementClient.ID)
	var scopedRoles []roleRepresentation
	if _, err := session.get(ctx, scopePath, &scopedRoles); err != nil {
		return fmt.Errorf("list reconciler client scope mappings: %w", err)
	}
	if err := reconcileExactRoles(ctx, session, scopePath, scopedRoles, roleNames, desiredRoles, result); err != nil {
		return fmt.Errorf("reconcile realm-management roles in the reconciler token scope: %w", err)
	}

	roleBase := realmBase + "/users/" + url.PathEscape(serviceAccount.ID) + "/role-mappings"
	var mappings roleMappingsRepresentation
	if _, err := session.get(ctx, roleBase, &mappings); err != nil {
		return fmt.Errorf("list reconciler service-account role mappings: %w", err)
	}
	if len(mappings.RealmMappings) != 0 {
		if err := session.delete(ctx, roleBase+"/realm", mappings.RealmMappings); err != nil {
			return fmt.Errorf("remove reconciler realm role mappings: %w", err)
		}
		result.Updated++
	}
	for clientID, mapping := range mappings.ClientMappings {
		if clientID == "realm-management" {
			continue
		}
		if len(mapping.Mappings) == 0 {
			continue
		}
		if err := session.delete(ctx, roleBase+"/clients/"+url.PathEscape(mapping.ID), mapping.Mappings); err != nil {
			return fmt.Errorf("remove reconciler role mappings for client %s: %w", clientID, err)
		}
		result.Updated++
	}

	managementPath := roleBase + "/clients/" + url.PathEscape(managementClient.ID)
	current := mappings.ClientMappings["realm-management"].Mappings
	if err := reconcileExactRoles(ctx, session, managementPath, current, roleNames, desiredRoles, result); err != nil {
		return fmt.Errorf("reconcile reconciler service-account realm-management roles: %w", err)
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

func ensureRealm(ctx context.Context, session *adminSession, desired Realm, result *Result) error {
	path := realmPath(desired.Name)
	wanted := realmRepresentation{
		Realm:                 desired.Name,
		DisplayName:           desired.DisplayName,
		Enabled:               true,
		OrganizationsEnabled:  true,
		RegistrationAllowed:   false,
		AccessTokenLifespan:   desired.AccessTokenLifespanSeconds,
		SSOSessionIdleTimeout: desired.SSOSessionIdleTimeoutSeconds,
		SSOSessionMaxLifespan: desired.SSOSessionMaxLifespanSeconds,
		RevokeRefreshToken:    desired.RevokeRefreshToken,
		RefreshTokenMaxReuse:  desired.RefreshTokenMaxReuse,
	}
	var existing realmRepresentation
	found, err := session.get(ctx, path, &existing)
	if err != nil {
		return fmt.Errorf("read realm %s: %w", desired.Name, err)
	}
	if !found {
		if err := session.post(ctx, "/admin/realms", wanted); err != nil {
			return fmt.Errorf("create realm %s: %w", desired.Name, err)
		}
		result.Created++
		return nil
	}
	if existing != wanted {
		if err := session.put(ctx, path, wanted); err != nil {
			return fmt.Errorf("update realm %s: %w", desired.Name, err)
		}
		result.Updated++
	}
	return nil
}

func reconcileRealmRoles(ctx context.Context, session *adminSession, state DesiredState, result *Result) error {
	base := realmPath(state.Realm.Name)
	desired := make(map[string]roleRepresentation, len(state.RealmRoles))
	for _, role := range state.RealmRoles {
		wanted := roleRepresentation{Name: role.Name, Description: managedDescription(role.Description)}
		desired[role.Name] = wanted
		path := base + "/roles/" + url.PathEscape(role.Name)
		var existing roleRepresentation
		found, err := session.get(ctx, path, &existing)
		if err != nil {
			return fmt.Errorf("read realm role %s: %w", role.Name, err)
		}
		if !found {
			if err := session.post(ctx, base+"/roles", wanted); err != nil {
				return fmt.Errorf("create realm role %s: %w", role.Name, err)
			}
			result.Created++
			continue
		}
		if existing.Name != wanted.Name || existing.Description != wanted.Description {
			wanted.ID = existing.ID
			if err := session.put(ctx, path, wanted); err != nil {
				return fmt.Errorf("update realm role %s: %w", role.Name, err)
			}
			result.Updated++
		}
	}

	var existing []roleRepresentation
	if _, err := session.get(ctx, base+"/roles?briefRepresentation=false&first=0&max=1000", &existing); err != nil {
		return fmt.Errorf("list realm roles: %w", err)
	}
	for _, role := range existing {
		if strings.HasPrefix(role.Description, managedRoleDescription) {
			if _, keep := desired[role.Name]; !keep {
				if err := session.delete(ctx, base+"/roles/"+url.PathEscape(role.Name), nil); err != nil {
					return fmt.Errorf("delete realm role %s: %w", role.Name, err)
				}
				result.Deleted++
			}
		}
	}
	return nil
}

func reconcileResourceClient(ctx context.Context, session *adminSession, state DesiredState, result *Result) (clientRepresentation, error) {
	base := realmPath(state.Realm.Name)
	wanted := clientRepresentation{
		ClientID:                     state.ResourceClient.ClientID,
		Name:                         state.ResourceClient.Name,
		Enabled:                      true,
		Protocol:                     "openid-connect",
		ClientAuthenticatorType:      "client-secret",
		BearerOnly:                   true,
		PublicClient:                 false,
		ConsentRequired:              false,
		StandardFlowEnabled:          false,
		ImplicitFlowEnabled:          false,
		DirectAccessGrantsEnabled:    false,
		ServiceAccountsEnabled:       false,
		AuthorizationServicesEnabled: false,
		FullScopeAllowed:             false,
		Attributes: map[string]string{
			managedAttribute:                   "true",
			"access.token.signed.response.alg": "RS256",
		},
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
	} else if !clientMatches(existing, wanted) {
		wanted.ID = existing.ID
		if err := session.put(ctx, base+"/clients/"+url.PathEscape(existing.ID), wanted); err != nil {
			return clientRepresentation{}, fmt.Errorf("update client %s: %w", wanted.ClientID, err)
		}
		result.Updated++
		existing = wanted
	}

	return existing, nil
}

func reconcileInteractiveClients(ctx context.Context, session *adminSession, state DesiredState, credentials map[string]ClientCredential, result *Result) ([]clientRepresentation, error) {
	base := realmPath(state.Realm.Name)
	clients := make([]clientRepresentation, 0, len(state.InteractiveClients))
	for _, desired := range state.InteractiveClients {
		publicClient := desired.AccessType == "public"
		attributes := map[string]string{
			managedAttribute:                   "true",
			"access.token.signed.response.alg": "RS256",
		}
		if len(desired.PostLogoutRedirectURIs) != 0 {
			attributes["post.logout.redirect.uris"] = strings.Join(desired.PostLogoutRedirectURIs, "##")
		}
		var secret string
		if publicClient {
			attributes["pkce.code.challenge.method"] = "S256"
		} else {
			credential, found := credentials[desired.Credential]
			if !found {
				return nil, fmt.Errorf("%w: client credential %q is required", ErrInvalidConfig, desired.Credential)
			}
			secret = credential.ClientSecret
			attributes[managedClientSecretHash] = secretHash(secret)
		}
		wanted := clientRepresentation{
			ClientID:                     desired.ClientID,
			Name:                         desired.Name,
			Enabled:                      true,
			Protocol:                     "openid-connect",
			ClientAuthenticatorType:      "client-secret",
			Secret:                       secret,
			BearerOnly:                   false,
			PublicClient:                 publicClient,
			ConsentRequired:              false,
			StandardFlowEnabled:          true,
			ImplicitFlowEnabled:          false,
			DirectAccessGrantsEnabled:    false,
			ServiceAccountsEnabled:       false,
			AuthorizationServicesEnabled: false,
			FullScopeAllowed:             false,
			RedirectURIs:                 append([]string(nil), desired.RedirectURIs...),
			WebOrigins:                   append([]string{}, desired.WebOrigins...),
			Attributes:                   attributes,
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
		} else if !clientMatches(existing, wanted) {
			wanted.ID = existing.ID
			if err := session.put(ctx, base+"/clients/"+url.PathEscape(existing.ID), wanted); err != nil {
				return nil, fmt.Errorf("update interactive client %s: %w", desired.ClientID, err)
			}
			result.Updated++
			existing = wanted
		}
		if err := reconcileAudienceMapper(ctx, session, base, existing, state.ResourceClient.ClientID, result); err != nil {
			return nil, err
		}
		if err := ensureOptionalClientScope(ctx, session, base, existing, state.OrganizationClaim.ClientScope, result); err != nil {
			return nil, err
		}
		clients = append(clients, existing)
	}
	return clients, nil
}

func reconcileAudienceMapper(ctx context.Context, session *adminSession, realmBase string, client clientRepresentation, audience string, result *Result) error {
	path := realmBase + "/clients/" + url.PathEscape(client.ID) + "/protocol-mappers/models"
	var mappers []protocolMapperRepresentation
	if _, err := session.get(ctx, path, &mappers); err != nil {
		return fmt.Errorf("list protocol mappers for %s: %w", client.ClientID, err)
	}
	wanted := protocolMapperRepresentation{
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
	found := false
	for _, mapper := range mappers {
		if mapper.Name == wanted.Name {
			found = true
			if !mapperMatches(mapper, wanted) {
				wanted.ID = mapper.ID
				if err := session.put(ctx, path+"/"+url.PathEscape(mapper.ID), wanted); err != nil {
					return fmt.Errorf("update audience mapper for %s: %w", client.ClientID, err)
				}
				result.Updated++
			}
			continue
		}
		if mapper.Config[managedAttribute] == "true" {
			if err := session.delete(ctx, path+"/"+url.PathEscape(mapper.ID), nil); err != nil {
				return fmt.Errorf("delete managed mapper %s from %s: %w", mapper.Name, client.ClientID, err)
			}
			result.Deleted++
		}
	}
	if !found {
		if err := session.post(ctx, path, wanted); err != nil {
			return fmt.Errorf("create audience mapper for %s: %w", client.ClientID, err)
		}
		result.Created++
	}
	return nil
}

func ensureOptionalClientScope(ctx context.Context, session *adminSession, realmBase string, client clientRepresentation, scopeName string, result *Result) error {
	var scopes []clientScopeRepresentation
	if _, err := session.get(ctx, realmBase+"/client-scopes", &scopes); err != nil {
		return fmt.Errorf("list client scopes: %w", err)
	}
	var desiredScope clientScopeRepresentation
	for _, scope := range scopes {
		if scope.Name == scopeName {
			desiredScope = scope
			break
		}
	}
	if desiredScope.ID == "" {
		return fmt.Errorf("%w: built-in client scope %q does not exist", ErrUnexpectedResponse, scopeName)
	}
	path := realmBase + "/clients/" + url.PathEscape(client.ID) + "/optional-client-scopes"
	var assigned []clientScopeRepresentation
	if _, err := session.get(ctx, path, &assigned); err != nil {
		return fmt.Errorf("list optional client scopes for %s: %w", client.ClientID, err)
	}
	for _, scope := range assigned {
		if scope.ID == desiredScope.ID {
			return nil
		}
	}
	if err := session.put(ctx, path+"/"+url.PathEscape(desiredScope.ID), nil); err != nil {
		return fmt.Errorf("assign organization scope to %s: %w", client.ClientID, err)
	}
	result.Updated++
	return nil
}

func pruneManagedClients(ctx context.Context, session *adminSession, state DesiredState, interactive []clientRepresentation, resource, reconciler clientRepresentation, result *Result) error {
	keep := map[string]struct{}{resource.ClientID: {}, reconciler.ClientID: {}}
	for _, client := range interactive {
		keep[client.ClientID] = struct{}{}
	}
	base := realmPath(state.Realm.Name)
	var clients []clientRepresentation
	if _, err := session.get(ctx, base+"/clients?first=0&max=1000", &clients); err != nil {
		return fmt.Errorf("list clients: %w", err)
	}
	for _, client := range clients {
		if client.Attributes[managedAttribute] != "true" {
			continue
		}
		if _, exists := keep[client.ClientID]; exists {
			continue
		}
		if err := session.delete(ctx, base+"/clients/"+url.PathEscape(client.ID), nil); err != nil {
			return fmt.Errorf("delete managed client %s: %w", client.ClientID, err)
		}
		result.Deleted++
	}
	return nil
}

func reconcileInteractiveRealmScopes(ctx context.Context, session *adminSession, state DesiredState, clients []clientRepresentation, result *Result) error {
	base := realmPath(state.Realm.Name)
	var platformRole roleRepresentation
	found, err := session.get(ctx, base+"/roles/platform-admin", &platformRole)
	if err != nil {
		return fmt.Errorf("read platform-admin realm role: %w", err)
	}
	if !found {
		return fmt.Errorf("%w: platform-admin realm role is missing after reconciliation", ErrUnexpectedResponse)
	}
	for _, client := range clients {
		path := base + "/clients/" + url.PathEscape(client.ID) + "/scope-mappings/realm"
		var existing []roleRepresentation
		if _, err := session.get(ctx, path, &existing); err != nil {
			return fmt.Errorf("list realm scope mappings for %s: %w", client.ClientID, err)
		}
		var remove []roleRepresentation
		hasPlatformRole := false
		for _, role := range existing {
			if role.Name == platformRole.Name {
				hasPlatformRole = true
				continue
			}
			remove = append(remove, role)
		}
		if len(remove) != 0 {
			if err := session.delete(ctx, path, remove); err != nil {
				return fmt.Errorf("remove extra realm scope mappings from %s: %w", client.ClientID, err)
			}
			result.Updated++
		}
		if !hasPlatformRole {
			if err := session.post(ctx, path, []roleRepresentation{platformRole}); err != nil {
				return fmt.Errorf("map platform-admin into %s token scope: %w", client.ClientID, err)
			}
			result.Updated++
		}
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
	membershipCount := 0
	for _, mapper := range mappers {
		if mapper.ProtocolMapper != organizationMapperID {
			continue
		}
		membershipCount++
		wantedMembership := mapper
		wantedMembership.Config = cloneStringMap(mapper.Config)
		for key, value := range map[string]string{
			"id.token.claim":            "false",
			"access.token.claim":        "true",
			"lightweight.claim":         "false",
			"userinfo.token.claim":      "false",
			"introspection.token.claim": "false",
			"multivalued":               "true",
			"addOrganizationId":         "true",
			"addOrganizationAttributes": "false",
			"addOrganizationDomain":     "false",
		} {
			wantedMembership.Config[key] = value
		}
		if !reflect.DeepEqual(mapper.Config, wantedMembership.Config) {
			if err := session.put(ctx, path+"/"+url.PathEscape(mapper.ID), wantedMembership); err != nil {
				return fmt.Errorf("update built-in organization membership mapper: %w", err)
			}
			result.Updated++
		}
	}
	if membershipCount != 1 {
		return fmt.Errorf("%w: organization scope must contain exactly one %s mapper", ErrUnexpectedResponse, organizationMapperID)
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
	found := false
	for _, mapper := range mappers {
		if mapper.Name == wanted.Name {
			found = true
			if !mapperMatches(mapper, wanted) {
				wanted.ID = mapper.ID
				if err := session.put(ctx, path+"/"+url.PathEscape(mapper.ID), wanted); err != nil {
					return fmt.Errorf("update organization mapper %s: %w", wanted.Name, err)
				}
				result.Updated++
			}
			continue
		}
		if mapper.Config[managedAttribute] == "true" {
			if err := session.delete(ctx, path+"/"+url.PathEscape(mapper.ID), nil); err != nil {
				return fmt.Errorf("delete managed protocol mapper %s: %w", mapper.Name, err)
			}
			result.Deleted++
		}
	}
	if !found {
		if err := session.post(ctx, path, wanted); err != nil {
			return fmt.Errorf("create organization mapper %s: %w", wanted.Name, err)
		}
		result.Created++
	}
	return nil
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
			FirstBrokerLoginFlowAlias: "first broker login",
			Config:                    config,
		}
		current, exists := byAlias[desired.Alias]
		if !exists {
			if err := session.post(ctx, base, wanted); err != nil {
				return fmt.Errorf("create identity provider %s: %w", desired.Alias, err)
			}
			result.Created++
			continue
		}
		if !identityProviderMatches(current, wanted) {
			if err := session.put(ctx, base+"/"+url.PathEscape(desired.Alias), wanted); err != nil {
				return fmt.Errorf("update identity provider %s: %w", desired.Alias, err)
			}
			result.Updated++
		}
	}
	for _, provider := range existing {
		if provider.Config[managedAttribute] != "true" {
			continue
		}
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
		if isManaged(organization.Attributes) {
			if _, keep := desiredAliases[organization.Alias]; !keep {
				if err := session.delete(ctx, base+"/organizations/"+url.PathEscape(organization.ID), nil); err != nil {
					return fmt.Errorf("delete organization %s: %w", organization.Alias, err)
				}
				result.Deleted++
			}
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
	}
	for _, group := range groups {
		if isManaged(group.Attributes) {
			if _, keep := desiredNames[group.Name]; !keep {
				if err := session.delete(ctx, base+"/"+url.PathEscape(group.ID), nil); err != nil {
					return fmt.Errorf("delete group %s in organization %s: %w", group.Name, desired.Alias, err)
				}
				result.Deleted++
			}
		}
	}
	return nil
}

func reconcileGroupRoleMappings(ctx context.Context, session *adminSession, realmBase, organizationID, groupID string, desired OrganizationGroup, client clientRepresentation, clientRoles map[string]roleRepresentation, result *Result) error {
	roleBase := realmBase + "/organizations/" + url.PathEscape(organizationID) + "/groups/" + url.PathEscape(groupID) + "/role-mappings"
	clientPath := roleBase + "/clients/" + url.PathEscape(client.ID)
	var existing []roleRepresentation
	if _, err := session.get(ctx, clientPath, &existing); err != nil {
		return err
	}
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

	realmPath := roleBase + "/realm"
	var realmRoles []roleRepresentation
	if _, err := session.get(ctx, realmPath, &realmRoles); err != nil {
		return err
	}
	if len(realmRoles) != 0 {
		if err := session.delete(ctx, realmPath, realmRoles); err != nil {
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

func isManaged(attributes map[string][]string) bool {
	values := attributes[managedAttribute]
	return len(values) == 1 && values[0] == "true"
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
		!equalStrings(existing.WebOrigins, wanted.WebOrigins) {
		return false
	}
	for key, value := range wanted.Attributes {
		if existing.Attributes[key] != value {
			return false
		}
	}
	for _, ownedKey := range []string{"pkce.code.challenge.method", "post.logout.redirect.uris", managedClientSecretHash} {
		if _, wantedOwns := wanted.Attributes[ownedKey]; !wantedOwns && existing.Attributes[ownedKey] != "" {
			return false
		}
	}
	return true
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
		existing.FirstBrokerLoginFlowAlias != wanted.FirstBrokerLoginFlowAlias {
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
