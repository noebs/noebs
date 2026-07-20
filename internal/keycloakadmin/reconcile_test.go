package keycloakadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestReconcileEmptyRealmThenNoOp(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	first, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	if !first.Changed() || fake.writeCount() == 0 {
		t.Fatalf("first Reconcile() result = %#v, writes = %d", first, fake.writeCount())
	}
	firstWrites := fake.writeCount()

	second, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if second.Changed() {
		t.Fatalf("second Reconcile() result = %#v, want no changes", second)
	}
	if got := fake.writeCount() - firstWrites; got != len(state.IdentityProviders) {
		t.Fatalf("second Reconcile() issued %d writes, want one masked-secret assertion per identity provider", got)
	}

	backoffice, ok := fake.clientByClientID("noebs-backoffice")
	if !ok || backoffice.PublicClient || backoffice.Secret != "backoffice-secret" {
		t.Fatalf("backoffice client = %#v", backoffice)
	}
	if backoffice.Attributes["post.logout.redirect.uris"] != "https://api.noebs.sd/backoffice/oauth/logout/callback" {
		t.Fatalf("backoffice post logout URI = %q", backoffice.Attributes["post.logout.redirect.uris"])
	}
	if backoffice.Attributes["pkce.code.challenge.method"] != "S256" {
		t.Fatalf("backoffice PKCE method = %q", backoffice.Attributes["pkce.code.challenge.method"])
	}
	if len(backoffice.WebOrigins) != 0 {
		t.Fatalf("backoffice web origins = %v", backoffice.WebOrigins)
	}
	assertWalletAuthorizerExact(t, fake, state)
	if !fake.serviceAccountHasRole("noebs-keycloak-reconciler", "realm-management", "realm-admin") {
		t.Fatal("steady reconciler service account does not have realm-management/realm-admin")
	}
	if !fake.clientScopeHasRole("noebs-keycloak-reconciler", "realm-management", "realm-admin") {
		t.Fatal("realm-management/realm-admin is not in the steady reconciler token scope")
	}
	if got := fake.organizationGroupRoles("tenant-cutover", "tenant-admin"); !equalStrings(got, []string{
		"tenant-admin", "reporting:read", "wallet:read", "wallet:audit:read", "wallet:manual:create",
		"wallet:fees:write", "wallet:rates:write", "wallet:workflow:approve", "wallet:workflow:reject",
	}) {
		t.Fatalf("tenant-admin mapped roles = %v", got)
	}
}

func TestReconcileFinalizesClientCreateNormalization(t *testing.T) {
	fake := newFakeKeycloak()
	fake.normalizeClientCreates = true
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("first Reconcile() error = %v", err)
	}
	writes := fake.writeCount()

	second, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if second.Changed() {
		t.Fatalf("second Reconcile() result = %#v, want create normalization finalized in the first pass", second)
	}
	if got := fake.writeCount() - writes; got != len(state.IdentityProviders) {
		t.Fatalf("second Reconcile() issued %d writes, want one masked-secret assertion per identity provider", got)
	}
}

func TestReconcileDeletesOrganizationsAndGroupsOutsideDesiredState(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.addUnmanagedOrganization("tenant-unmanaged", "Unmanaged Tenant")
	fake.addUnmanagedOrganizationGroup("tenant-cutover", "unmanaged-group")

	writes := fake.writeCount()
	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("authoritative Reconcile() error = %v", err)
	}
	if result != (Result{Deleted: 2}) {
		t.Fatalf("authoritative Reconcile() result = %#v, want two deletions", result)
	}
	if fake.hasOrganization("tenant-unmanaged") {
		t.Fatal("organization outside desired state was retained")
	}
	if fake.hasOrganizationGroup("tenant-cutover", "unmanaged-group") {
		t.Fatal("organization group outside desired state was retained")
	}
	if got := fake.writeCount() - writes; got != 2+len(state.IdentityProviders) {
		t.Fatalf("authoritative Reconcile() writes = %d, want two deletions plus identity-provider secret assertions", got)
	}

	writes = fake.writeCount()
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady-state Reconcile() error = %v", err)
	}
	if result.Changed() || fake.writeCount()-writes != len(state.IdentityProviders) {
		t.Fatalf("steady-state Reconcile() result = %#v, writes = %d", result, fake.writeCount()-writes)
	}
}

func TestReconcilePrunesKeycloakStateOutsideExactAuthority(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}

	fake.addUnmanagedClient("rogue-client")
	fake.addUnmanagedIdentityProvider("github")
	fake.addIdentityProviderMapper("google", "injected-group-mapper")
	fake.addClientMapper("noebs-keycloak-reconciler", "injected-reconciler-claim", "oidc-hardcoded-claim-mapper")
	fake.addClientMapper("noebs-api", "injected-api-claim", "oidc-hardcoded-claim-mapper")
	fake.addClientMapper("noebs-mobile", "injected-mobile-claim", "oidc-hardcoded-claim-mapper")
	fake.addClientMapper("noebs-backoffice", "injected-audience", audienceMapperID)
	fake.addOrganizationScopeMapper("injected-organization-claim", "oidc-hardcoded-claim-mapper")
	fake.addOrganizationScopeMapper("duplicate-organization-membership", organizationMapperID)

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("exact Reconcile() error = %v", err)
	}
	if result != (Result{Deleted: 9}) {
		t.Fatalf("exact Reconcile() result = %#v, want nine deletions", result)
	}
	if _, exists := fake.clientByClientID("rogue-client"); exists {
		t.Fatal("client outside desired state was retained")
	}
	if fake.hasIdentityProvider("github") {
		t.Fatal("identity provider outside desired state was retained")
	}
	if mappers := fake.identityProviderMapperNames("google"); len(mappers) != 0 {
		t.Fatalf("google identity provider mappers = %v", mappers)
	}
	for _, clientID := range keycloakBuiltinClientIDs() {
		if _, exists := fake.clientByClientID(clientID); !exists {
			t.Fatalf("Keycloak built-in client %s was deleted", clientID)
		}
	}
	for clientID, want := range map[string][]string{
		"noebs-keycloak-reconciler": {},
		"noebs-api":                 {},
		"noebs-mobile":              {audienceMapperName, subjectMapperName},
		"noebs-backoffice":          {audienceMapperName, subjectMapperName},
		walletAuthorizerClientID:    {},
	} {
		if got := fake.clientMapperNames(clientID); !equalStrings(got, want) {
			t.Fatalf("client %s protocol mappers = %v, want %v", clientID, got, want)
		}
	}
	if got := fake.organizationScopeMapperNames(); !equalStrings(got, []string{organizationMapperName, state.OrganizationClaim.MapperName}) {
		t.Fatalf("organization scope protocol mappers = %v", got)
	}

	writes := fake.writeCount()
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady-state Reconcile() error = %v", err)
	}
	if result.Changed() || fake.writeCount()-writes != len(state.IdentityProviders) {
		t.Fatalf("steady-state Reconcile() result = %#v, writes = %d", result, fake.writeCount()-writes)
	}
}

func TestReconcileOverwritesProtocolMapperConfigDrift(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectClientMapperConfig("noebs-mobile", audienceMapperName, "injected", "true")
	fake.injectOrganizationScopeMapperConfig(organizationMapperName, "injected", "true")
	fake.injectOrganizationScopeMapperConfig(state.OrganizationClaim.MapperName, "injected", "true")

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("drift Reconcile() error = %v", err)
	}
	if result != (Result{Updated: 3}) {
		t.Fatalf("drift Reconcile() result = %#v, want three updates", result)
	}
	if mapper, exists := fake.clientMapper("noebs-mobile", audienceMapperName); !exists || !mapperMatches(mapper, audienceMapper(state.ResourceClient.ClientID)) {
		t.Fatalf("mobile audience mapper = %#v", mapper)
	}
	if mapper, exists := fake.organizationScopeMapper(organizationMapperName); !exists || !mapperMatches(mapper, organizationMembershipMapper()) {
		t.Fatalf("organization membership mapper = %#v", mapper)
	}
	wantedGroupMapper := protocolMapperRepresentation{
		Name:            state.OrganizationClaim.MapperName,
		Protocol:        "openid-connect",
		ProtocolMapper:  state.OrganizationClaim.ProtocolMapper,
		ConsentRequired: false,
		Config:          cloneStringMap(state.OrganizationClaim.Config),
	}
	wantedGroupMapper.Config[managedAttribute] = "true"
	if mapper, exists := fake.organizationScopeMapper(state.OrganizationClaim.MapperName); !exists || !mapperMatches(mapper, wantedGroupMapper) {
		t.Fatalf("organization group mapper = %#v", mapper)
	}
}

func TestReconcilePrunesScopeAndRoleMappingDrift(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}

	fake.addRealmRole("platform-admin")
	for _, clientID := range []string{"noebs-mobile", "noebs-backoffice", walletAuthorizerClientID} {
		fake.assignClientScope(clientID, "default", "email")
		fake.assignClientScope(clientID, "optional", "offline_access")
	}
	fake.assignClientScope("noebs-api", "default", "email")
	fake.assignClientScope("noebs-api", "optional", "organization")
	fake.assignClientScope("noebs-keycloak-reconciler", "default", "basic")
	fake.assignClientScope("noebs-keycloak-reconciler", "optional", "organization")
	fake.addClientRealmScopeRole("noebs-mobile", "platform-admin")
	fake.addClientScopeRole("noebs-mobile", "realm-management", "realm-admin")
	fake.addClientScopeRole("noebs-backoffice", "realm-management", "realm-admin")
	fake.addClientScopeRole("noebs-api", "realm-management", "realm-admin")
	fake.addClientRealmScopeRole("noebs-keycloak-reconciler", "platform-admin")
	fake.addClientScopeRole("noebs-keycloak-reconciler", "noebs-api", "tenant-admin")
	fake.addOrganizationGroupClientRole("tenant-cutover", "user", "realm-management", "realm-admin")

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("exact Reconcile() error = %v", err)
	}
	if result.Deleted != 1 || result.Updated == 0 {
		t.Fatalf("exact Reconcile() result = %#v", result)
	}
	if fake.hasRealmRole("platform-admin") {
		t.Fatal("stale platform-admin realm role was retained")
	}
	for clientID, assignments := range map[string]struct {
		defaults []string
		optional []string
	}{
		"noebs-keycloak-reconciler": {defaults: []string{"roles"}},
		"noebs-api":                 {},
		"noebs-mobile":              {defaults: []string{"acr"}, optional: []string{"organization"}},
		"noebs-backoffice":          {defaults: []string{"acr"}, optional: []string{"organization"}},
		walletAuthorizerClientID:    {defaults: []string{"acr"}},
	} {
		if got := fake.clientScopeNames(clientID, "default"); !equalStrings(got, assignments.defaults) {
			t.Fatalf("client %s default scopes = %v, want %v", clientID, got, assignments.defaults)
		}
		if got := fake.clientScopeNames(clientID, "optional"); !equalStrings(got, assignments.optional) {
			t.Fatalf("client %s optional scopes = %v, want %v", clientID, got, assignments.optional)
		}
	}
	for _, clientID := range []string{"noebs-api", "noebs-mobile", "noebs-backoffice", walletAuthorizerClientID} {
		mappings := fake.clientScopeRoleMappings(clientID)
		if len(mappings.RealmMappings) != 0 || len(mappings.ClientMappings) != 0 {
			t.Fatalf("client %s role scope mappings = %#v", clientID, mappings)
		}
	}
	reconcilerMappings := fake.clientScopeRoleMappings("noebs-keycloak-reconciler")
	if len(reconcilerMappings.RealmMappings) != 0 || !equalStrings(roleNames(reconcilerMappings.ClientMappings["realm-management"].Mappings), []string{"realm-admin"}) || len(reconcilerMappings.ClientMappings) != 1 {
		t.Fatalf("reconciler role scope mappings = %#v", reconcilerMappings)
	}
	if clients := fake.organizationGroupRoleMappingClients("tenant-cutover", "user"); !equalStrings(clients, []string{"noebs-api"}) {
		t.Fatalf("organization group role-mapping clients = %v", clients)
	}

	writes := fake.writeCount()
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady-state Reconcile() error = %v", err)
	}
	if result.Changed() || fake.writeCount()-writes != len(state.IdentityProviders) {
		t.Fatalf("steady-state Reconcile() result = %#v, writes = %d", result, fake.writeCount()-writes)
	}
}

func TestReconcileRemovesClientRoleComposites(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.addClientRoleComposite("noebs-api", "user", "tenant-admin")

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("composite Reconcile() error = %v", err)
	}
	if result != (Result{Updated: 1}) {
		t.Fatalf("composite Reconcile() result = %#v, want one update", result)
	}
	if got := fake.clientRoleCompositeNames("noebs-api", "user"); len(got) != 0 {
		t.Fatalf("user role composites = %v", got)
	}
}

func TestReconcilePrunesUndeclaredClientAttributes(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectClientAttribute("noebs-mobile", "standard.token.exchange.enabled", "true")
	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("drift Reconcile() error = %v", err)
	}
	if result != (Result{Updated: 1}) {
		t.Fatalf("drift Reconcile() result = %#v, want one update", result)
	}
	client, found := fake.clientByClientID("noebs-mobile")
	if !found {
		t.Fatal("noebs-mobile client does not exist")
	}
	if _, found := client.Attributes["standard.token.exchange.enabled"]; found {
		t.Fatalf("undeclared client attribute survived reconciliation: %#v", client.Attributes)
	}
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady-state Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("steady-state Reconcile() result = %#v", result)
	}
}

func TestReconcileRejectsMissingPinnedKeycloakBuiltinClient(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()

	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.removeClient("broker")
	if _, err := reconciler.Reconcile(context.Background(), state); !errors.Is(err, ErrUnexpectedResponse) {
		t.Fatalf("Reconcile() error = %v, want ErrUnexpectedResponse", err)
	}
}

func TestPinnedKeycloakBuiltinClientsDoNotIncludeDesiredClients(t *testing.T) {
	state := repositoryDesiredState(t)
	desired := []string{state.ReconcilerClient.ClientID, state.ResourceClient.ClientID}
	for _, client := range state.InteractiveClients {
		desired = append(desired, client.ClientID)
	}
	for _, clientID := range desired {
		if isKeycloakBuiltinClient(clientID) {
			t.Fatalf("desired client %s is classified as a Keycloak built-in", clientID)
		}
	}
}

func TestClientMatchesPinnedKeycloakAttributeNormalization(t *testing.T) {
	wanted := clientRepresentation{ClientID: "noebs-mobile", Attributes: managedClientAttributes()}
	existing := wanted
	existing.Attributes = cloneStringMap(wanted.Attributes)
	existing.Attributes["client.secret.creation.time"] = "1784499356"
	if !clientMatches(existing, wanted) {
		t.Fatal("pinned Keycloak 26.7 normalized attributes did not match")
	}
	existing.Attributes["standard.token.exchange.enabled"] = "true"
	if clientMatches(existing, wanted) {
		t.Fatal("undeclared client attribute matched desired state")
	}
	update := clientUpdate(existing, wanted)
	if update.Attributes["standard.token.exchange.enabled"] != "" {
		t.Fatalf("clientUpdate() undeclared attribute = %q, want explicit empty value", update.Attributes["standard.token.exchange.enabled"])
	}
	if _, found := update.Attributes["client.secret.creation.time"]; found {
		t.Fatal("clientUpdate() attempted to overwrite Keycloak-owned secret creation time")
	}
}

func TestReconcileRestoresSecurityRelevantRealmDrift(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectRealmDrift()

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("drift Reconcile() error = %v", err)
	}
	if result != (Result{Updated: 1}) {
		t.Fatalf("drift Reconcile() result = %#v, want one realm update", result)
	}
	if !fake.realmMatches(desiredRealmRepresentation(state)) {
		t.Fatal("realm security settings differ after reconciliation")
	}
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("steady Reconcile() result = %#v", result)
	}
}

func TestReconcileRestoresKeycloakBuiltinClientDrift(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectBuiltinClientDrift()

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("drift Reconcile() error = %v", err)
	}
	if !result.Changed() {
		t.Fatal("built-in client drift was not reported")
	}
	assertBuiltinClientsExact(t, fake, state.Realm.Name)
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("steady Reconcile() result = %#v", result)
	}
}

func TestReconcileOwnsGoogleLoAAndTOTP(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	assertHumanAuthenticationExact(t, fake, state)
}

func TestReconcileRepairsHostileAuthenticationDriftThenConverges(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectAuthenticationDrift(state)

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("drift Reconcile() error = %v", err)
	}
	if !result.Changed() {
		t.Fatal("hostile authentication drift was not reported")
	}
	assertHumanAuthenticationExact(t, fake, state)
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("steady Reconcile() result = %#v", result)
	}
}

func TestReconcileRepairsWalletAuthorizerDriftThenConverges(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectWalletAuthorizerDrift()
	fake.addClientMapper(walletAuthorizerClientID, "hostile-wallet-claim", "oidc-hardcoded-claim-mapper")
	fake.assignClientScope(walletAuthorizerClientID, "default", "email")
	fake.assignClientScope(walletAuthorizerClientID, "optional", "organization")
	fake.addClientScopeRole(walletAuthorizerClientID, "realm-management", "realm-admin")

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("drift Reconcile() error = %v", err)
	}
	if !result.Changed() {
		t.Fatal("wallet authorizer drift was not reported")
	}
	assertWalletAuthorizerExact(t, fake, state)
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("steady Reconcile() result = %#v", result)
	}
}

func TestReconcileReadsAndRestoresManagedClientSecrets(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectClientSecret("noebs-keycloak-reconciler", "hostile-reconciler-secret")
	fake.injectClientSecret("noebs-backoffice", "hostile-backoffice-secret")
	fake.injectClientSecret(walletAuthorizerClientID, "hostile-wallet-authorizer-secret")

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("secret Reconcile() error = %v", err)
	}
	if result != (Result{Updated: 3}) {
		t.Fatalf("secret Reconcile() result = %#v, want three client updates", result)
	}
	if got := fake.clientSecret("noebs-keycloak-reconciler"); got != "steady-reconciler-secret" {
		t.Fatal("reconciler client secret was not restored")
	}
	if got := fake.clientSecret("noebs-backoffice"); got != "backoffice-secret" {
		t.Fatal("backoffice client secret was not restored")
	}
	if got := fake.clientSecret(walletAuthorizerClientID); got != "wallet-authorizer-secret" {
		t.Fatal("wallet authorizer client secret was not restored")
	}
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("steady Reconcile() result = %#v", result)
	}
}

func TestReconcileReassertsMaskedIdentityProviderSecret(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	fake.injectIdentityProviderSecret("google", "hostile-google-secret")
	writes := fake.writeCount()

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("secret Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("masked-secret assertion was reported as semantic drift: %#v", result)
	}
	if got := fake.writeCount() - writes; got != 1 {
		t.Fatalf("masked-secret assertions = %d, want one", got)
	}
	if got := fake.identityProviderSecret("google"); got != "google-secret" {
		t.Fatal("identity-provider secret was not restored")
	}
}

func TestReconcileDeletesUndeclaredOrganizationDescendants(t *testing.T) {
	fake := newFakeKeycloak()
	server := httptest.NewTLSServer(fake)
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	state := repositoryDesiredState(t)
	if _, err := reconciler.Reconcile(context.Background(), state); err != nil {
		t.Fatalf("initial Reconcile() error = %v", err)
	}
	childID := fake.addUnmanagedOrganizationChildGroup("tenant-cutover", "user", "rogue-child")
	grandchildID := fake.addUnmanagedOrganizationChildGroup("tenant-cutover", "rogue-child", "rogue-grandchild")
	fake.addOrganizationGroupClientRole("tenant-cutover", "rogue-grandchild", "noebs-api", "tenant-admin")
	fake.addOrganizationGroupMember(grandchildID, "hostile-member")

	result, err := reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("descendant Reconcile() error = %v", err)
	}
	if result != (Result{Deleted: 2}) {
		t.Fatalf("descendant Reconcile() result = %#v, want child and grandchild deletion", result)
	}
	if fake.hasOrganizationGroupID(childID) || fake.hasOrganizationGroupID(grandchildID) {
		t.Fatal("undeclared organization descendant survived reconciliation")
	}
	if fake.hasOrganizationGroupMember(grandchildID, "hostile-member") {
		t.Fatal("undeclared descendant membership survived deletion")
	}
	if got := fake.organizationGroupRoleMappingClients("tenant-cutover", "rogue-grandchild"); len(got) != 0 {
		t.Fatalf("undeclared descendant role mappings survived deletion: %v", got)
	}
	result, err = reconciler.Reconcile(context.Background(), state)
	if err != nil {
		t.Fatalf("steady Reconcile() error = %v", err)
	}
	if result.Changed() {
		t.Fatalf("steady Reconcile() result = %#v", result)
	}
}

func assertBuiltinClientsExact(t *testing.T, fake *fakeKeycloak, realm string) {
	t.Helper()
	for _, spec := range keycloakBuiltinClientSpecs(realm) {
		client, found := fake.clientByClientID(spec.client.ClientID)
		if !found || !clientMatches(client, spec.client) {
			t.Fatalf("Keycloak built-in client %s = %#v", spec.client.ClientID, client)
		}
		if got := fake.clientMapperNames(spec.client.ClientID); !equalStrings(got, mapperNames(spec.protocolMappers)) {
			t.Fatalf("Keycloak built-in client %s mappers = %v", spec.client.ClientID, got)
		}
		for _, wanted := range spec.protocolMappers {
			mapper, found := fake.clientMapper(spec.client.ClientID, wanted.Name)
			if !found || !mapperMatches(mapper, wanted) {
				t.Fatalf("Keycloak built-in client %s mapper %s = %#v", spec.client.ClientID, wanted.Name, mapper)
			}
		}
		if got := fake.clientScopeNames(spec.client.ClientID, "default"); !equalStrings(got, spec.defaultScopes) {
			t.Fatalf("Keycloak built-in client %s default scopes = %v", spec.client.ClientID, got)
		}
		if got := fake.clientScopeNames(spec.client.ClientID, "optional"); !equalStrings(got, spec.optionalScopes) {
			t.Fatalf("Keycloak built-in client %s optional scopes = %v", spec.client.ClientID, got)
		}
		mappings := fake.clientScopeRoleMappings(spec.client.ClientID)
		if len(mappings.RealmMappings) != 0 || len(mappings.ClientMappings) != len(spec.scopeRoleMappings) {
			t.Fatalf("Keycloak built-in client %s role scope mappings = %#v", spec.client.ClientID, mappings)
		}
		for _, wanted := range spec.scopeRoleMappings {
			if got := roleNames(mappings.ClientMappings[wanted.clientID].Mappings); !equalStrings(got, wanted.roles) {
				t.Fatalf("Keycloak built-in client %s mapping to %s = %v", spec.client.ClientID, wanted.clientID, got)
			}
		}
	}
}

func assertHumanAuthenticationExact(t *testing.T, fake *fakeKeycloak, state DesiredState) {
	t.Helper()
	wantedRealm := desiredRealmRepresentation(state)
	if !fake.realmMatches(wantedRealm) {
		t.Fatal("realm authentication and OTP policy differ from desired state")
	}
	customTopLevel := make(map[string]bool)
	for _, flow := range fake.authenticationFlows {
		if flow.TopLevel && !flow.BuiltIn {
			customTopLevel[flow.Alias] = true
		}
	}
	for _, flow := range desiredAuthenticationFlows(state) {
		customTopLevel[flow.Alias] = false
		assertManagedAuthenticationFlow(t, fake, flow)
	}
	for alias, unexpected := range customTopLevel {
		if unexpected {
			t.Fatalf("authentication flow %s is outside desired state", alias)
		}
	}
	assertLoAConditionExact(t, fake, googleLoA1FlowAlias, "1", "28800")
	assertLoAConditionExact(t, fake, googleTOTPLoA2FlowAlias, "2", "0")
	assertLoAConditionExact(t, fake, googlePostBrokerLoA1FlowAlias, "1", "28800")
	assertLoAConditionExact(t, fake, googlePostBrokerLoA2FlowAlias, "2", "0")
	for _, executions := range fake.authenticationExecutions {
		for _, execution := range executions {
			if execution.ProviderID == "auth-username-password-form" || execution.ProviderID == "idp-username-password-form" {
				t.Fatalf("password authenticator %s survived", execution.ProviderID)
			}
		}
	}
	action := fake.requiredActions[configureTOTPProvider]
	wantedAction := state.Authentication.OTP.ConfigureRequiredAction
	if action.ProviderID != configureTOTPProvider || action.Name != "Configure OTP" ||
		action.Enabled != wantedAction.Enabled || action.DefaultAction != wantedAction.DefaultAction ||
		action.Priority != wantedAction.Priority || len(action.Config) != 0 {
		t.Fatalf("Configure OTP required action = %#v", action)
	}
	for alias, action := range fake.requiredActions {
		if alias != configureTOTPProvider && (action.Enabled || action.DefaultAction) {
			t.Fatalf("required action %s remains enabled/default: %#v", alias, action)
		}
	}
	for _, clientID := range []string{"account", "account-console", "admin-cli", "security-admin-console"} {
		client, found := fake.clientByClientID(clientID)
		if !found || client.Enabled || client.StandardFlowEnabled || client.ImplicitFlowEnabled ||
			client.DirectAccessGrantsEnabled || client.ServiceAccountsEnabled || client.FullScopeAllowed ||
			len(client.RedirectURIs) != 0 || len(client.WebOrigins) != 0 ||
			len(fake.clientMapperNames(clientID)) != 0 || len(fake.clientScopeNames(clientID, "default")) != 0 ||
			len(fake.clientScopeNames(clientID, "optional")) != 0 {
			t.Fatalf("unused stock client %s is not inert: %#v", clientID, client)
		}
	}
	provider := fake.identityProviders["google"]
	if provider.FirstBrokerLoginFlowAlias != state.Authentication.FirstBrokerLoginFlow ||
		provider.PostBrokerLoginFlowAlias != state.Authentication.PostBrokerLoginFlow {
		t.Fatalf("Google broker flow bindings = %#v", provider)
	}
	if provider.Config["forwardParameters"] != "login_hint" {
		t.Fatalf("Google forwarded parameters = %q", provider.Config["forwardParameters"])
	}
}

func assertLoAConditionExact(t *testing.T, fake *fakeKeycloak, flowAlias, level, maxAge string) {
	t.Helper()
	flowID := fake.authenticationFlowIDByAlias(flowAlias)
	for _, execution := range fake.authenticationExecutions[flowID] {
		if execution.ProviderID != "conditional-level-of-authentication" {
			continue
		}
		config := fake.authenticatorConfigs[execution.AuthenticationConfig]
		if execution.Requirement != "REQUIRED" || config.Alias != flowAlias || !equalStringMap(config.Config, map[string]string{
			"loa-condition-level": level,
			"loa-max-age":         maxAge,
		}) {
			t.Fatalf("LoA condition %s = execution %#v config %#v", flowAlias, execution, config)
		}
		return
	}
	t.Fatalf("LoA condition %s is missing", flowAlias)
}

func assertWalletAuthorizerExact(t *testing.T, fake *fakeKeycloak, state DesiredState) {
	t.Helper()
	client, found := fake.clientByClientID(walletAuthorizerClientID)
	if !found {
		t.Fatal("wallet authorizer client is missing")
	}
	attributes := managedClientAttributes()
	attributes["access.token.signed.response.alg"] = "RS256"
	attributes["id.token.signed.response.alg"] = "RS256"
	attributes["pkce.code.challenge.method"] = "S256"
	attributes["default.acr.values"] = googleTOTPACR
	attributes["minimum.acr.value"] = googleTOTPACR
	attributes[managedClientSecretHash] = secretHash("wallet-authorizer-secret")
	wanted := clientRepresentation{
		ClientID:                           walletAuthorizerClientID,
		Name:                               "Noebs Wallet Authorizer",
		Enabled:                            true,
		Protocol:                           "openid-connect",
		ClientAuthenticatorType:            "client-secret",
		StandardFlowEnabled:                true,
		RedirectURIs:                       []string{walletAuthorizationCallbackURI},
		WebOrigins:                         []string{},
		Attributes:                         attributes,
		NodeReRegistrationTimeout:          -1,
		AuthenticationFlowBindingOverrides: map[string]string{},
	}
	if !clientMatches(client, wanted) {
		t.Fatalf("wallet authorizer client = %#v, want %#v", client, wanted)
	}
	if fake.clientSecret(walletAuthorizerClientID) != "wallet-authorizer-secret" {
		t.Fatal("wallet authorizer secret differs from authority")
	}
	if mappers := fake.clientMapperNames(walletAuthorizerClientID); len(mappers) != 0 {
		t.Fatalf("wallet authorizer protocol mappers = %v, want none", mappers)
	}
	if scopes := fake.clientScopeNames(walletAuthorizerClientID, "default"); !equalStrings(scopes, []string{"acr"}) {
		t.Fatalf("wallet authorizer default scopes = %v, want acr", scopes)
	}
	if scopes := fake.clientScopeNames(walletAuthorizerClientID, "optional"); len(scopes) != 0 {
		t.Fatalf("wallet authorizer optional scopes = %v, want none", scopes)
	}
	mappings := fake.clientScopeRoleMappings(walletAuthorizerClientID)
	if len(mappings.RealmMappings) != 0 || len(mappings.ClientMappings) != 0 {
		t.Fatalf("wallet authorizer role scope mappings = %#v, want none", mappings)
	}
	if state.Authentication.Levels[1].MaxAgeSeconds != 0 {
		t.Fatalf("wallet authorizer LoA2 max age = %d, want zero", state.Authentication.Levels[1].MaxAgeSeconds)
	}
}

func assertManagedAuthenticationFlow(t *testing.T, fake *fakeKeycloak, desired managedAuthenticationFlow) {
	t.Helper()
	flowID := fake.authenticationFlowIDByAlias(desired.Alias)
	if flowID == "" {
		t.Fatalf("authentication flow %s is missing", desired.Alias)
	}
	current := fake.authenticationFlows[flowID]
	if current.Alias != desired.Alias || current.Description != desired.Description || current.ProviderID != "basic-flow" || current.BuiltIn {
		t.Fatalf("authentication flow %s = %#v", desired.Alias, current)
	}
	executions := append([]authenticationExecutionInfoRepresentation(nil), fake.authenticationExecutions[flowID]...)
	sort.Slice(executions, func(i, j int) bool { return executions[i].Priority < executions[j].Priority })
	if len(executions) != len(desired.Executions) {
		t.Fatalf("authentication flow %s executions = %#v", desired.Alias, executions)
	}
	for index, wanted := range desired.Executions {
		execution := executions[index]
		if execution.Requirement != wanted.Requirement || execution.Priority != wanted.Priority ||
			!authenticationExecutionIdentityMatches(execution, wanted) {
			t.Fatalf("authentication flow %s execution %d = %#v", desired.Alias, index, execution)
		}
		if wanted.Config != nil {
			config := fake.authenticatorConfigs[execution.AuthenticationConfig]
			if config.Alias != wanted.ConfigAlias || !equalStringMap(config.Config, wanted.Config) {
				t.Fatalf("authentication flow %s config = %#v", desired.Alias, config)
			}
		} else if execution.AuthenticationConfig != "" {
			t.Fatalf("authentication flow %s has unexpected config %s", desired.Alias, execution.AuthenticationConfig)
		}
		if wanted.Flow != nil {
			assertManagedAuthenticationFlow(t, fake, *wanted.Flow)
		}
	}
}

func mapperNames(mappers []protocolMapperRepresentation) []string {
	result := make([]string, 0, len(mappers))
	for _, mapper := range mappers {
		result = append(result, mapper.Name)
	}
	return result
}

func TestDeleteBootstrapClient(t *testing.T) {
	deleted := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasSuffix(request.URL.Path, "/protocol/openid-connect/token"):
			writeJSON(writer, http.StatusOK, map[string]string{"access_token": "admin-token", "token_type": "Bearer"})
		case request.Method == http.MethodGet && request.URL.Path == "/admin/realms/master/clients" && request.URL.Query().Get("clientId") == BootstrapClientID:
			writeJSON(writer, http.StatusOK, []clientRepresentation{{ID: "bootstrap-id", ClientID: BootstrapClientID}})
		case request.Method == http.MethodDelete && request.URL.Path == "/admin/realms/master/clients/bootstrap-id":
			deleted = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.Error(writer, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	reconciler, err := New(validTestConfig(server.URL), server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.DeleteBootstrapClient(context.Background()); err != nil {
		t.Fatalf("DeleteBootstrapClient() error = %v", err)
	}
	if !deleted {
		t.Fatal("bootstrap client was not deleted")
	}
}

type fakeKeycloak struct {
	mu sync.Mutex

	nextID                 int
	writes                 int
	realm                  *realmRepresentation
	normalizeClientCreates bool

	realmRoles                map[string]roleRepresentation
	clients                   map[string]clientRepresentation
	clientRoles               map[string]map[string]roleRepresentation
	clientRoleComposites      map[string]map[string][]roleRepresentation
	clientMappers             map[string]map[string]protocolMapperRepresentation
	clientScopes              map[string]clientScopeRepresentation
	scopeMappers              map[string]map[string]protocolMapperRepresentation
	defaultScopes             map[string]map[string]bool
	optionalScopes            map[string]map[string]bool
	clientRealmMappings       map[string][]roleRepresentation
	clientClientScopeMappings map[string]map[string][]roleRepresentation
	serviceAccounts           map[string]userRepresentation
	userClientMappings        map[string]map[string][]roleRepresentation
	userRealmMappings         map[string][]roleRepresentation
	identityProviders         map[string]identityProviderRepresentation
	identityProviderMappers   map[string]map[string]identityProviderMapperRepresentation
	organizations             map[string]organizationRepresentation
	groups                    map[string]map[string]groupRepresentation
	groupParents              map[string]string
	groupMembers              map[string]map[string]bool
	groupClientMappings       map[string][]roleRepresentation
	groupRealmMappings        map[string][]roleRepresentation
	authenticationFlows       map[string]authenticationFlowRepresentation
	authenticationExecutions  map[string][]authenticationExecutionInfoRepresentation
	authenticatorConfigs      map[string]authenticatorConfigRepresentation
	requiredActions           map[string]requiredActionProviderRepresentation
}

func newFakeKeycloak() *fakeKeycloak {
	return &fakeKeycloak{
		realmRoles:                map[string]roleRepresentation{},
		clients:                   map[string]clientRepresentation{},
		clientRoles:               map[string]map[string]roleRepresentation{},
		clientRoleComposites:      map[string]map[string][]roleRepresentation{},
		clientMappers:             map[string]map[string]protocolMapperRepresentation{},
		clientScopes:              map[string]clientScopeRepresentation{},
		scopeMappers:              map[string]map[string]protocolMapperRepresentation{},
		defaultScopes:             map[string]map[string]bool{},
		optionalScopes:            map[string]map[string]bool{},
		clientRealmMappings:       map[string][]roleRepresentation{},
		clientClientScopeMappings: map[string]map[string][]roleRepresentation{},
		serviceAccounts:           map[string]userRepresentation{},
		userClientMappings:        map[string]map[string][]roleRepresentation{},
		userRealmMappings:         map[string][]roleRepresentation{},
		identityProviders:         map[string]identityProviderRepresentation{},
		identityProviderMappers:   map[string]map[string]identityProviderMapperRepresentation{},
		organizations:             map[string]organizationRepresentation{},
		groups:                    map[string]map[string]groupRepresentation{},
		groupParents:              map[string]string{},
		groupMembers:              map[string]map[string]bool{},
		groupClientMappings:       map[string][]roleRepresentation{},
		groupRealmMappings:        map[string][]roleRepresentation{},
		authenticationFlows:       map[string]authenticationFlowRepresentation{},
		authenticationExecutions:  map[string][]authenticationExecutionInfoRepresentation{},
		authenticatorConfigs:      map[string]authenticatorConfigRepresentation{},
		requiredActions:           map[string]requiredActionProviderRepresentation{},
	}
}

func (f *fakeKeycloak) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasSuffix(request.URL.Path, "/protocol/openid-connect/token") {
		writeJSON(writer, http.StatusOK, map[string]string{"access_token": "admin-token", "token_type": "Bearer"})
		return
	}
	if request.Header.Get("Authorization") != "Bearer admin-token" {
		http.Error(writer, "missing admin token", http.StatusUnauthorized)
		return
	}
	if request.URL.Path == "/admin/realms" && request.Method == http.MethodPost {
		var realm realmRepresentation
		if !decodeFakeRequest(writer, request, &realm) {
			return
		}
		f.realm = &realm
		f.initializeRealmBuiltins()
		f.mutated(writer, http.StatusCreated)
		return
	}
	const prefix = "/admin/realms/noebs"
	if !strings.HasPrefix(request.URL.Path, prefix) {
		http.Error(writer, "unknown realm", http.StatusNotFound)
		return
	}
	if request.URL.Path == prefix {
		if f.realm == nil {
			http.Error(writer, "not found", http.StatusNotFound)
			return
		}
		switch request.Method {
		case http.MethodGet:
			writeJSON(writer, http.StatusOK, f.realm)
		case http.MethodPut:
			var realm realmRepresentation
			if decodeFakeRequest(writer, request, &realm) {
				attributes := cloneStringPointerMap(f.realm.Attributes)
				for key, value := range realm.Attributes {
					if value == nil {
						delete(attributes, key)
						continue
					}
					copy := *value
					attributes[key] = &copy
				}
				realm.Attributes = attributes
				f.realm = &realm
				f.mutated(writer, http.StatusNoContent)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if f.realm == nil {
		http.Error(writer, "realm missing", http.StatusNotFound)
		return
	}
	tail := strings.Split(strings.TrimPrefix(request.URL.Path, prefix+"/"), "/")
	switch tail[0] {
	case "roles":
		f.handleRealmRoles(writer, request, tail)
	case "clients":
		f.handleClients(writer, request, tail)
	case "client-scopes":
		f.handleClientScopes(writer, request, tail)
	case "identity-provider":
		f.handleIdentityProviders(writer, request, tail)
	case "organizations":
		f.handleOrganizations(writer, request, tail)
	case "users":
		f.handleUsers(writer, request, tail)
	case "authentication":
		f.handleAuthentication(writer, request, tail)
	default:
		http.Error(writer, "unhandled "+request.Method+" "+request.URL.RequestURI(), http.StatusNotFound)
	}
}

func (f *fakeKeycloak) initializeRealmBuiltins() {
	for _, alias := range []string{"browser", "direct grant", "registration", "reset credentials", "clients", "first broker login", "docker auth"} {
		id := f.id("authentication-flow")
		f.authenticationFlows[id] = authenticationFlowRepresentation{
			ID: id, Alias: alias, Description: "Keycloak built-in", ProviderID: "basic-flow", TopLevel: true, BuiltIn: true,
		}
		f.authenticationExecutions[id] = []authenticationExecutionInfoRepresentation{}
	}
	f.requiredActions[configureTOTPProvider] = requiredActionProviderRepresentation{
		Alias: configureTOTPProvider, Name: "Configure OTP", ProviderID: configureTOTPProvider,
		Enabled: true, DefaultAction: false, Priority: 54, Config: map[string]string{},
	}
	f.requiredActions["UPDATE_PASSWORD"] = requiredActionProviderRepresentation{
		Alias: "UPDATE_PASSWORD", Name: "Update Password", ProviderID: "UPDATE_PASSWORD",
		Enabled: true, DefaultAction: false, Priority: 40, Config: map[string]string{},
	}
	for _, roleName := range keycloakBuiltinRealmRoleNames("noebs") {
		f.realmRoles[roleName] = roleRepresentation{ID: f.id("realm-role"), Name: roleName, Composite: roleName == "default-roles-noebs"}
	}
	clientIDs := make(map[string]string, len(keycloakBuiltinClientIDs()))
	for _, spec := range keycloakBuiltinClientSpecs("noebs") {
		id := f.id("client")
		client := spec.client
		client.ID = id
		f.clients[id] = client
		clientIDs[client.ClientID] = id
		f.clientRoles[id] = map[string]roleRepresentation{}
		f.clientRoleComposites[id] = map[string][]roleRepresentation{}
		f.clientMappers[id] = map[string]protocolMapperRepresentation{}
		f.defaultScopes[id] = map[string]bool{}
		f.optionalScopes[id] = map[string]bool{}
		f.clientClientScopeMappings[id] = map[string][]roleRepresentation{}
		if client.ClientID == "account" {
			for _, roleName := range []string{"manage-account", "view-groups"} {
				f.clientRoles[id][roleName] = roleRepresentation{ID: f.id("role"), Name: roleName, ClientRole: true, ContainerID: id}
			}
		}
		if client.ClientID == "realm-management" {
			f.clientRoles[id]["realm-admin"] = roleRepresentation{ID: f.id("role"), Name: "realm-admin", ClientRole: true, ContainerID: id}
		}
	}
	for _, clientID := range []string{"account", "account-console", "admin-cli", "security-admin-console"} {
		id := clientIDs[clientID]
		client := f.clients[id]
		client.Enabled = true
		client.PublicClient = true
		client.StandardFlowEnabled = true
		client.DirectAccessGrantsEnabled = clientID == "admin-cli"
		client.FullScopeAllowed = true
		f.clients[id] = client
	}
	for _, scopeName := range []string{"acr", "address", "basic", "email", "microprofile-jwt", "offline_access", "organization", "phone", "profile", "roles", "web-origins"} {
		scopeID := f.id("scope")
		f.clientScopes[scopeID] = clientScopeRepresentation{ID: scopeID, Name: scopeName, Protocol: "openid-connect"}
		f.scopeMappers[scopeID] = map[string]protocolMapperRepresentation{}
		if scopeName == "organization" {
			mapperID := f.id("mapper")
			f.scopeMappers[scopeID][mapperID] = protocolMapperRepresentation{
				ID: mapperID, Name: organizationMapperName, Protocol: "openid-connect",
				ProtocolMapper: organizationMapperID, Config: map[string]string{},
			}
		}
	}
	for _, spec := range keycloakBuiltinClientSpecs("noebs") {
		clientID := clientIDs[spec.client.ClientID]
		for _, scopeName := range spec.defaultScopes {
			f.defaultScopes[clientID][f.clientScopeID(scopeName)] = true
		}
		for _, scopeName := range spec.optionalScopes {
			f.optionalScopes[clientID][f.clientScopeID(scopeName)] = true
		}
		for _, mapper := range spec.protocolMappers {
			mapper.ID = f.id("mapper")
			f.clientMappers[clientID][mapper.ID] = mapper
		}
		for _, mapping := range spec.scopeRoleMappings {
			targetID := clientIDs[mapping.clientID]
			for _, roleName := range mapping.roles {
				f.clientClientScopeMappings[clientID][targetID] = append(f.clientClientScopeMappings[clientID][targetID], f.clientRoles[targetID][roleName])
			}
		}
	}
}

func (f *fakeKeycloak) clientScopeID(name string) string {
	for id, scope := range f.clientScopes {
		if scope.Name == name {
			return id
		}
	}
	return ""
}

func (f *fakeKeycloak) handleAuthentication(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) < 2 {
		http.Error(writer, "authentication path", http.StatusNotFound)
		return
	}
	switch tail[1] {
	case "flows":
		f.handleAuthenticationFlows(writer, request, tail[2:])
	case "executions":
		f.handleAuthenticationExecutions(writer, request, tail[2:])
	case "config":
		f.handleAuthenticatorConfigs(writer, request, tail[2:])
	case "required-actions":
		f.handleRequiredActions(writer, request, tail[2:])
	case "register-required-action":
		if request.Method != http.MethodPost {
			http.Error(writer, "method", http.StatusMethodNotAllowed)
			return
		}
		var payload map[string]string
		if !decodeFakeRequest(writer, request, &payload) {
			return
		}
		provider := payload["providerId"]
		f.requiredActions[provider] = requiredActionProviderRepresentation{
			Alias: provider, Name: payload["name"], ProviderID: provider, Config: map[string]string{},
		}
		f.mutated(writer, http.StatusNoContent)
	default:
		http.Error(writer, "authentication path", http.StatusNotFound)
	}
}

func (f *fakeKeycloak) handleAuthenticationFlows(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) == 0 {
		switch request.Method {
		case http.MethodGet:
			flows := make([]authenticationFlowRepresentation, 0, len(f.authenticationFlows))
			for _, flow := range f.authenticationFlows {
				if flow.TopLevel {
					flows = append(flows, flow)
				}
			}
			sort.Slice(flows, func(i, j int) bool { return flows[i].Alias < flows[j].Alias })
			writeJSON(writer, http.StatusOK, flows)
		case http.MethodPost:
			var flow authenticationFlowRepresentation
			if !decodeFakeRequest(writer, request, &flow) {
				return
			}
			flow.ID = f.id("authentication-flow")
			f.authenticationFlows[flow.ID] = flow
			f.authenticationExecutions[flow.ID] = []authenticationExecutionInfoRepresentation{}
			f.mutated(writer, http.StatusCreated)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) == 1 {
		flow, found := f.authenticationFlows[tail[0]]
		if !found {
			http.Error(writer, "flow", http.StatusNotFound)
			return
		}
		switch request.Method {
		case http.MethodGet:
			writeJSON(writer, http.StatusOK, flow)
		case http.MethodPut:
			var update authenticationFlowRepresentation
			if decodeFakeRequest(writer, request, &update) {
				update.ID = flow.ID
				f.authenticationFlows[flow.ID] = update
				f.mutated(writer, http.StatusNoContent)
			}
		case http.MethodDelete:
			if flow.BuiltIn {
				http.Error(writer, "built-in", http.StatusBadRequest)
				return
			}
			f.deleteAuthenticationFlow(flow.ID)
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) < 2 || tail[1] != "executions" {
		http.Error(writer, "flow path", http.StatusNotFound)
		return
	}
	flowID := f.authenticationFlowIDByAlias(tail[0])
	if flowID == "" {
		http.Error(writer, "flow", http.StatusNotFound)
		return
	}
	if len(tail) == 2 {
		switch request.Method {
		case http.MethodGet:
			writeJSON(writer, http.StatusOK, f.flattenAuthenticationExecutions(flowID, 0))
		case http.MethodPut:
			var update authenticationExecutionInfoRepresentation
			if !decodeFakeRequest(writer, request, &update) {
				return
			}
			if !f.updateAuthenticationExecution(update) {
				http.Error(writer, "execution", http.StatusNotFound)
				return
			}
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) != 3 || request.Method != http.MethodPost {
		http.Error(writer, "execution path", http.StatusNotFound)
		return
	}
	var payload map[string]any
	if !decodeFakeRequest(writer, request, &payload) {
		return
	}
	priority, _ := payload["priority"].(float64)
	execution := authenticationExecutionInfoRepresentation{
		ID: f.id("authentication-execution"), Requirement: "DISABLED", Level: 0,
		Priority: int(priority),
	}
	switch tail[2] {
	case "execution":
		execution.ProviderID, _ = payload["provider"].(string)
		execution.DisplayName = execution.ProviderID
	case "flow":
		alias, _ := payload["alias"].(string)
		description, _ := payload["description"].(string)
		childID := f.id("authentication-flow")
		f.authenticationFlows[childID] = authenticationFlowRepresentation{
			ID: childID, Alias: alias, Description: description, ProviderID: "basic-flow", TopLevel: false, BuiltIn: false,
		}
		f.authenticationExecutions[childID] = []authenticationExecutionInfoRepresentation{}
		execution.AuthenticationFlow = true
		execution.DisplayName = alias
		execution.Description = description
		execution.FlowID = childID
	default:
		http.Error(writer, "execution type", http.StatusNotFound)
		return
	}
	f.authenticationExecutions[flowID] = append(f.authenticationExecutions[flowID], execution)
	f.mutated(writer, http.StatusCreated)
}

func (f *fakeKeycloak) handleAuthenticationExecutions(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) == 1 && request.Method == http.MethodDelete {
		if !f.deleteAuthenticationExecution(tail[0]) {
			http.Error(writer, "execution", http.StatusNotFound)
			return
		}
		f.mutated(writer, http.StatusNoContent)
		return
	}
	if len(tail) == 2 && tail[1] == "config" && request.Method == http.MethodPost {
		var config authenticatorConfigRepresentation
		if !decodeFakeRequest(writer, request, &config) {
			return
		}
		config.ID = f.id("authenticator-config")
		f.authenticatorConfigs[config.ID] = config
		if !f.setAuthenticationExecutionConfig(tail[0], config.ID) {
			http.Error(writer, "execution", http.StatusNotFound)
			return
		}
		f.mutated(writer, http.StatusCreated)
		return
	}
	http.Error(writer, "execution path", http.StatusNotFound)
}

func (f *fakeKeycloak) handleAuthenticatorConfigs(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) != 1 {
		http.Error(writer, "config path", http.StatusNotFound)
		return
	}
	config, found := f.authenticatorConfigs[tail[0]]
	if !found {
		http.Error(writer, "config", http.StatusNotFound)
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, config)
	case http.MethodPut:
		var update authenticatorConfigRepresentation
		if decodeFakeRequest(writer, request, &update) {
			update.ID = config.ID
			f.authenticatorConfigs[config.ID] = update
			f.mutated(writer, http.StatusNoContent)
		}
	case http.MethodDelete:
		delete(f.authenticatorConfigs, config.ID)
		for flowID, executions := range f.authenticationExecutions {
			for index := range executions {
				if executions[index].AuthenticationConfig == config.ID {
					executions[index].AuthenticationConfig = ""
				}
			}
			f.authenticationExecutions[flowID] = executions
		}
		f.mutated(writer, http.StatusNoContent)
	default:
		http.Error(writer, "method", http.StatusMethodNotAllowed)
	}
}

func (f *fakeKeycloak) handleRequiredActions(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) == 0 && request.Method == http.MethodGet {
		actions := make([]requiredActionProviderRepresentation, 0, len(f.requiredActions))
		for _, action := range f.requiredActions {
			actions = append(actions, action)
		}
		sort.Slice(actions, func(i, j int) bool { return actions[i].Priority < actions[j].Priority })
		writeJSON(writer, http.StatusOK, actions)
		return
	}
	if len(tail) == 1 && request.Method == http.MethodPut {
		var action requiredActionProviderRepresentation
		if decodeFakeRequest(writer, request, &action) {
			f.requiredActions[tail[0]] = action
			f.mutated(writer, http.StatusNoContent)
		}
		return
	}
	http.Error(writer, "required action path", http.StatusNotFound)
}

func (f *fakeKeycloak) authenticationFlowIDByAlias(alias string) string {
	for id, flow := range f.authenticationFlows {
		if flow.Alias == alias {
			return id
		}
	}
	return ""
}

func (f *fakeKeycloak) flattenAuthenticationExecutions(flowID string, level int) []authenticationExecutionInfoRepresentation {
	current := append([]authenticationExecutionInfoRepresentation(nil), f.authenticationExecutions[flowID]...)
	sort.Slice(current, func(i, j int) bool { return current[i].Priority < current[j].Priority })
	result := make([]authenticationExecutionInfoRepresentation, 0, len(current))
	for index, execution := range current {
		execution.Level = level
		execution.Index = index
		result = append(result, execution)
		if execution.AuthenticationFlow {
			result = append(result, f.flattenAuthenticationExecutions(execution.FlowID, level+1)...)
		}
	}
	return result
}

func (f *fakeKeycloak) updateAuthenticationExecution(update authenticationExecutionInfoRepresentation) bool {
	for flowID, executions := range f.authenticationExecutions {
		for index := range executions {
			if executions[index].ID == update.ID {
				executions[index].Requirement = update.Requirement
				executions[index].Priority = update.Priority
				f.authenticationExecutions[flowID] = executions
				return true
			}
		}
	}
	return false
}

func (f *fakeKeycloak) setAuthenticationExecutionConfig(executionID, configID string) bool {
	for flowID, executions := range f.authenticationExecutions {
		for index := range executions {
			if executions[index].ID == executionID {
				executions[index].AuthenticationConfig = configID
				f.authenticationExecutions[flowID] = executions
				return true
			}
		}
	}
	return false
}

func (f *fakeKeycloak) deleteAuthenticationExecution(executionID string) bool {
	for flowID, executions := range f.authenticationExecutions {
		for index, execution := range executions {
			if execution.ID != executionID {
				continue
			}
			if execution.AuthenticationConfig != "" {
				delete(f.authenticatorConfigs, execution.AuthenticationConfig)
			}
			if execution.AuthenticationFlow {
				f.deleteAuthenticationFlow(execution.FlowID)
			}
			f.authenticationExecutions[flowID] = append(executions[:index], executions[index+1:]...)
			return true
		}
	}
	return false
}

func (f *fakeKeycloak) deleteAuthenticationFlow(flowID string) {
	for _, execution := range append([]authenticationExecutionInfoRepresentation(nil), f.authenticationExecutions[flowID]...) {
		f.deleteAuthenticationExecution(execution.ID)
	}
	delete(f.authenticationExecutions, flowID)
	delete(f.authenticationFlows, flowID)
}

func (f *fakeKeycloak) handleRealmRoles(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) == 1 {
		switch request.Method {
		case http.MethodGet:
			roles := make([]roleRepresentation, 0, len(f.realmRoles))
			for _, role := range f.realmRoles {
				roles = append(roles, role)
			}
			writeJSON(writer, http.StatusOK, roles)
		case http.MethodPost:
			var role roleRepresentation
			if decodeFakeRequest(writer, request, &role) {
				role.ID = f.id("realm-role")
				f.realmRoles[role.Name] = role
				f.mutated(writer, http.StatusCreated)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	role, found := f.realmRoles[tail[1]]
	if !found {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, role)
	case http.MethodPut:
		var replacement roleRepresentation
		if decodeFakeRequest(writer, request, &replacement) {
			replacement.ID = role.ID
			f.realmRoles[replacement.Name] = replacement
			f.mutated(writer, http.StatusNoContent)
		}
	case http.MethodDelete:
		delete(f.realmRoles, tail[1])
		f.mutated(writer, http.StatusNoContent)
	default:
		http.Error(writer, "unhandled role mutation", http.StatusNotFound)
	}
}

func (f *fakeKeycloak) handleClients(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) == 1 {
		switch request.Method {
		case http.MethodGet:
			clients := make([]clientRepresentation, 0, len(f.clients))
			for _, client := range f.clients {
				if wanted := request.URL.Query().Get("clientId"); wanted == "" || client.ClientID == wanted {
					clients = append(clients, client)
				}
			}
			writeJSON(writer, http.StatusOK, clients)
		case http.MethodPost:
			var client clientRepresentation
			if !decodeFakeRequest(writer, request, &client) {
				return
			}
			client.ID = f.id("client")
			if f.normalizeClientCreates && (client.ClientID == "noebs-keycloak-reconciler" || client.ClientID == "noebs-api") {
				client.RootURL = "keycloak-create-default"
			}
			f.clients[client.ID] = client
			f.clientRoles[client.ID] = map[string]roleRepresentation{}
			f.clientRoleComposites[client.ID] = map[string][]roleRepresentation{}
			f.clientMappers[client.ID] = map[string]protocolMapperRepresentation{}
			f.defaultScopes[client.ID] = map[string]bool{}
			if f.normalizeClientCreates && client.ClientID == "noebs-keycloak-reconciler" {
				f.defaultScopes[client.ID][f.clientScopeID("basic")] = true
			}
			f.optionalScopes[client.ID] = map[string]bool{}
			f.clientClientScopeMappings[client.ID] = map[string][]roleRepresentation{}
			if client.ServiceAccountsEnabled {
				user := userRepresentation{ID: f.id("user"), Username: "service-account-" + client.ClientID}
				f.serviceAccounts[client.ID] = user
				f.userClientMappings[user.ID] = map[string][]roleRepresentation{}
			}
			f.mutated(writer, http.StatusCreated)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	clientID := tail[1]
	client, found := f.clients[clientID]
	if !found {
		http.Error(writer, "client not found", http.StatusNotFound)
		return
	}
	if len(tail) == 2 && request.Method == http.MethodPut {
		var replacement clientRepresentation
		if decodeFakeRequest(writer, request, &replacement) {
			replacement.ID = client.ID
			attributes := cloneStringMap(client.Attributes)
			for key, value := range replacement.Attributes {
				if value == "" {
					delete(attributes, key)
					continue
				}
				attributes[key] = value
			}
			replacement.Attributes = attributes
			f.clients[clientID] = replacement
			if f.normalizeClientCreates && client.ClientID == "noebs-keycloak-reconciler" {
				f.defaultScopes[clientID][f.clientScopeID("basic")] = true
			}
			f.mutated(writer, http.StatusNoContent)
		}
		return
	}
	if len(tail) == 2 && request.Method == http.MethodDelete {
		delete(f.clients, clientID)
		delete(f.clientRoles, clientID)
		delete(f.clientRoleComposites, clientID)
		delete(f.clientMappers, clientID)
		delete(f.defaultScopes, clientID)
		delete(f.optionalScopes, clientID)
		delete(f.clientClientScopeMappings, clientID)
		if serviceAccount, exists := f.serviceAccounts[clientID]; exists {
			delete(f.userClientMappings, serviceAccount.ID)
			delete(f.userRealmMappings, serviceAccount.ID)
			delete(f.serviceAccounts, clientID)
		}
		f.mutated(writer, http.StatusNoContent)
		return
	}
	if len(tail) == 3 && tail[2] == "service-account-user" && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, f.serviceAccounts[clientID])
		return
	}
	if len(tail) == 3 && tail[2] == "client-secret" && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, credentialRepresentation{Type: "secret", Value: client.Secret})
		return
	}
	if len(tail) >= 3 && tail[2] == "roles" {
		f.handleClientRoles(writer, request, clientID, tail[3:])
		return
	}
	if len(tail) >= 4 && tail[2] == "protocol-mappers" && tail[3] == "models" {
		f.handleMappers(writer, request, f.clientMappers[clientID], tail[4:])
		return
	}
	if len(tail) >= 3 && (tail[2] == "default-client-scopes" || tail[2] == "optional-client-scopes") {
		assignments := f.optionalScopes[clientID]
		if tail[2] == "default-client-scopes" {
			assignments = f.defaultScopes[clientID]
		}
		if len(tail) == 3 && request.Method == http.MethodGet {
			var scopes []clientScopeRepresentation
			for scopeID := range assignments {
				scopes = append(scopes, f.clientScopes[scopeID])
			}
			writeJSON(writer, http.StatusOK, scopes)
			return
		}
		if len(tail) == 4 && request.Method == http.MethodPut {
			assignments[tail[3]] = true
			f.mutated(writer, http.StatusNoContent)
			return
		}
		if len(tail) == 4 && request.Method == http.MethodDelete {
			delete(assignments, tail[3])
			f.mutated(writer, http.StatusNoContent)
			return
		}
	}
	if len(tail) == 3 && tail[2] == "scope-mappings" && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, f.clientRoleMappings(clientID))
		return
	}
	if len(tail) == 4 && tail[2] == "scope-mappings" && tail[3] == "realm" {
		switch request.Method {
		case http.MethodGet:
			writeJSON(writer, http.StatusOK, f.clientRealmMappings[clientID])
		case http.MethodPost:
			var roles []roleRepresentation
			if decodeFakeRequest(writer, request, &roles) {
				f.clientRealmMappings[clientID] = append(f.clientRealmMappings[clientID], roles...)
				f.mutated(writer, http.StatusNoContent)
			}
		case http.MethodDelete:
			f.clientRealmMappings[clientID] = nil
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) == 5 && tail[2] == "scope-mappings" && tail[3] == "clients" {
		targetID := tail[4]
		switch request.Method {
		case http.MethodGet:
			writeJSON(writer, http.StatusOK, f.clientClientScopeMappings[clientID][targetID])
		case http.MethodPost:
			var roles []roleRepresentation
			if decodeFakeRequest(writer, request, &roles) {
				f.clientClientScopeMappings[clientID][targetID] = append(f.clientClientScopeMappings[clientID][targetID], roles...)
				f.mutated(writer, http.StatusNoContent)
			}
		case http.MethodDelete:
			delete(f.clientClientScopeMappings[clientID], targetID)
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	http.Error(writer, "unhandled client path", http.StatusNotFound)
}

func (f *fakeKeycloak) handleClientRoles(writer http.ResponseWriter, request *http.Request, clientID string, tail []string) {
	roles := f.clientRoles[clientID]
	if len(tail) == 0 {
		switch request.Method {
		case http.MethodGet:
			result := make([]roleRepresentation, 0, len(roles))
			for _, role := range roles {
				result = append(result, role)
			}
			writeJSON(writer, http.StatusOK, result)
		case http.MethodPost:
			var role roleRepresentation
			if decodeFakeRequest(writer, request, &role) {
				role.ID = f.id("client-role")
				role.ClientRole = true
				role.ContainerID = clientID
				roles[role.Name] = role
				f.mutated(writer, http.StatusCreated)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	role, found := roles[tail[0]]
	if !found {
		http.Error(writer, "role not found", http.StatusNotFound)
		return
	}
	if len(tail) == 2 && tail[1] == "composites" {
		switch request.Method {
		case http.MethodGet:
			writeJSON(writer, http.StatusOK, f.clientRoleComposites[clientID][role.Name])
		case http.MethodDelete:
			f.clientRoleComposites[clientID][role.Name] = nil
			role.Composite = false
			roles[role.Name] = role
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, role)
	case http.MethodPut:
		var replacement roleRepresentation
		if decodeFakeRequest(writer, request, &replacement) {
			replacement.ID = role.ID
			replacement.ClientRole = true
			replacement.ContainerID = clientID
			delete(roles, role.Name)
			roles[replacement.Name] = replacement
			f.mutated(writer, http.StatusNoContent)
		}
	case http.MethodDelete:
		delete(roles, role.Name)
		delete(f.clientRoleComposites[clientID], role.Name)
		f.mutated(writer, http.StatusNoContent)
	default:
		http.Error(writer, "unhandled client role", http.StatusNotFound)
	}
}

func (f *fakeKeycloak) clientRoleMappings(clientID string) roleMappingsRepresentation {
	mappings := roleMappingsRepresentation{
		RealmMappings:  f.clientRealmMappings[clientID],
		ClientMappings: map[string]clientRoleMappingRepresentation{},
	}
	for targetID, roles := range f.clientClientScopeMappings[clientID] {
		target := f.clients[targetID]
		mappings.ClientMappings[target.ClientID] = clientRoleMappingRepresentation{ID: targetID, Client: target.ClientID, Mappings: roles}
	}
	return mappings
}

func (f *fakeKeycloak) handleClientScopes(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) == 1 && request.Method == http.MethodGet {
		var scopes []clientScopeRepresentation
		for _, scope := range f.clientScopes {
			scopes = append(scopes, scope)
		}
		writeJSON(writer, http.StatusOK, scopes)
		return
	}
	if len(tail) >= 4 && tail[2] == "protocol-mappers" && tail[3] == "models" {
		f.handleMappers(writer, request, f.scopeMappers[tail[1]], tail[4:])
		return
	}
	http.Error(writer, "unhandled client scope", http.StatusNotFound)
}

func (f *fakeKeycloak) handleMappers(writer http.ResponseWriter, request *http.Request, mappers map[string]protocolMapperRepresentation, tail []string) {
	if len(tail) == 0 {
		switch request.Method {
		case http.MethodGet:
			result := make([]protocolMapperRepresentation, 0, len(mappers))
			for _, mapper := range mappers {
				result = append(result, mapper)
			}
			writeJSON(writer, http.StatusOK, result)
		case http.MethodPost:
			var mapper protocolMapperRepresentation
			if decodeFakeRequest(writer, request, &mapper) {
				mapper.ID = f.id("mapper")
				mappers[mapper.ID] = mapper
				f.mutated(writer, http.StatusCreated)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if request.Method == http.MethodPut {
		var mapper protocolMapperRepresentation
		if decodeFakeRequest(writer, request, &mapper) {
			mapper.ID = tail[0]
			mappers[tail[0]] = mapper
			f.mutated(writer, http.StatusNoContent)
		}
		return
	}
	if request.Method == http.MethodDelete {
		delete(mappers, tail[0])
		f.mutated(writer, http.StatusNoContent)
		return
	}
	http.Error(writer, "unhandled mapper", http.StatusNotFound)
}

func (f *fakeKeycloak) handleIdentityProviders(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) < 2 || len(tail) > 5 || tail[1] != "instances" {
		http.Error(writer, "unhandled identity provider", http.StatusNotFound)
		return
	}
	if len(tail) >= 4 && tail[3] == "mappers" {
		alias := tail[2]
		mappers, exists := f.identityProviderMappers[alias]
		if !exists {
			http.Error(writer, "identity provider not found", http.StatusNotFound)
			return
		}
		if len(tail) == 4 && request.Method == http.MethodGet {
			result := make([]identityProviderMapperRepresentation, 0, len(mappers))
			for _, mapper := range mappers {
				result = append(result, mapper)
			}
			writeJSON(writer, http.StatusOK, result)
			return
		}
		if len(tail) == 5 && request.Method == http.MethodDelete {
			delete(mappers, tail[4])
			f.mutated(writer, http.StatusNoContent)
			return
		}
		http.Error(writer, "method", http.StatusMethodNotAllowed)
		return
	}
	if len(tail) == 3 {
		alias := tail[2]
		if _, exists := f.identityProviders[alias]; !exists {
			http.Error(writer, "identity provider not found", http.StatusNotFound)
			return
		}
		switch request.Method {
		case http.MethodPut:
			var provider identityProviderRepresentation
			if decodeFakeRequest(writer, request, &provider) {
				delete(f.identityProviders, alias)
				f.identityProviders[provider.Alias] = provider
				f.mutated(writer, http.StatusNoContent)
			}
		case http.MethodDelete:
			delete(f.identityProviders, alias)
			delete(f.identityProviderMappers, alias)
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	switch request.Method {
	case http.MethodGet:
		result := make([]identityProviderRepresentation, 0, len(f.identityProviders))
		for _, provider := range f.identityProviders {
			provider.Config = cloneStringMap(provider.Config)
			if _, hasSecret := provider.Config["clientSecret"]; hasSecret {
				provider.Config["clientSecret"] = "**********"
			}
			result = append(result, provider)
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodPost:
		var provider identityProviderRepresentation
		if decodeFakeRequest(writer, request, &provider) {
			f.identityProviders[provider.Alias] = provider
			f.identityProviderMappers[provider.Alias] = map[string]identityProviderMapperRepresentation{}
			f.mutated(writer, http.StatusCreated)
		}
	default:
		http.Error(writer, "method", http.StatusMethodNotAllowed)
	}
}

func (f *fakeKeycloak) handleOrganizations(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) == 1 {
		switch request.Method {
		case http.MethodGet:
			result := make([]organizationRepresentation, 0, len(f.organizations))
			for _, organization := range f.organizations {
				result = append(result, organization)
			}
			writeJSON(writer, http.StatusOK, result)
		case http.MethodPost:
			var organization organizationRepresentation
			if decodeFakeRequest(writer, request, &organization) {
				organization.ID = f.id("organization")
				f.organizations[organization.ID] = organization
				f.groups[organization.ID] = map[string]groupRepresentation{}
				f.mutated(writer, http.StatusCreated)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	organizationID := tail[1]
	organization, found := f.organizations[organizationID]
	if !found {
		http.Error(writer, "organization not found", http.StatusNotFound)
		return
	}
	if len(tail) == 2 {
		switch request.Method {
		case http.MethodPut:
			if decodeFakeRequest(writer, request, &organization) {
				organization.ID = organizationID
				f.organizations[organizationID] = organization
				f.mutated(writer, http.StatusNoContent)
			}
		case http.MethodDelete:
			delete(f.organizations, organizationID)
			delete(f.groups, organizationID)
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) >= 3 && tail[2] == "groups" {
		f.handleOrganizationGroups(writer, request, organizationID, tail[3:])
		return
	}
	http.Error(writer, "unhandled organization", http.StatusNotFound)
}

func (f *fakeKeycloak) handleOrganizationGroups(writer http.ResponseWriter, request *http.Request, organizationID string, tail []string) {
	groups := f.groups[organizationID]
	if len(tail) == 0 {
		switch request.Method {
		case http.MethodGet:
			result := make([]groupRepresentation, 0, len(groups))
			for id, group := range groups {
				if f.groupParents[id] != "" {
					continue
				}
				result = append(result, group)
			}
			writeJSON(writer, http.StatusOK, result)
		case http.MethodPost:
			var group groupRepresentation
			if decodeFakeRequest(writer, request, &group) {
				group.ID = f.id("group")
				groups[group.ID] = group
				f.groupMembers[group.ID] = map[string]bool{}
				f.mutated(writer, http.StatusCreated)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	groupID := tail[0]
	group, found := groups[groupID]
	if !found {
		http.Error(writer, "group not found", http.StatusNotFound)
		return
	}
	if len(tail) == 1 {
		switch request.Method {
		case http.MethodPut:
			if decodeFakeRequest(writer, request, &group) {
				group.ID = groupID
				groups[groupID] = group
				f.mutated(writer, http.StatusNoContent)
			}
		case http.MethodDelete:
			f.deleteOrganizationGroup(organizationID, groupID)
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) == 2 && tail[1] == "children" {
		switch request.Method {
		case http.MethodGet:
			var children []groupRepresentation
			for id, candidate := range groups {
				if f.groupParents[id] == groupID {
					children = append(children, candidate)
				}
			}
			writeJSON(writer, http.StatusOK, children)
		case http.MethodPost:
			var child groupRepresentation
			if decodeFakeRequest(writer, request, &child) {
				child.ID = f.id("group")
				groups[child.ID] = child
				f.groupParents[child.ID] = groupID
				f.groupMembers[child.ID] = map[string]bool{}
				f.mutated(writer, http.StatusCreated)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) == 2 && tail[1] == "role-mappings" && request.Method == http.MethodGet {
		keyPrefix := organizationID + "/" + groupID + "/"
		mappings := roleMappingsRepresentation{
			RealmMappings:  f.groupRealmMappings[keyPrefix],
			ClientMappings: map[string]clientRoleMappingRepresentation{},
		}
		for clientID, client := range f.clients {
			roles := f.groupClientMappings[keyPrefix+clientID]
			if len(roles) != 0 {
				mappings.ClientMappings[client.ClientID] = clientRoleMappingRepresentation{ID: clientID, Client: client.ClientID, Mappings: roles}
			}
		}
		writeJSON(writer, http.StatusOK, mappings)
		return
	}
	if len(tail) >= 3 && tail[1] == "role-mappings" {
		keyPrefix := organizationID + "/" + groupID + "/"
		if len(tail) == 4 && tail[2] == "clients" {
			key := keyPrefix + tail[3]
			switch request.Method {
			case http.MethodGet:
				writeJSON(writer, http.StatusOK, f.groupClientMappings[key])
			case http.MethodPost:
				var roles []roleRepresentation
				if decodeFakeRequest(writer, request, &roles) {
					f.groupClientMappings[key] = append(f.groupClientMappings[key], roles...)
					f.mutated(writer, http.StatusNoContent)
				}
			case http.MethodDelete:
				delete(f.groupClientMappings, key)
				f.mutated(writer, http.StatusNoContent)
			default:
				http.Error(writer, "method", http.StatusMethodNotAllowed)
			}
			return
		}
		if len(tail) == 3 && tail[2] == "realm" {
			switch request.Method {
			case http.MethodGet:
				writeJSON(writer, http.StatusOK, f.groupRealmMappings[keyPrefix])
			case http.MethodDelete:
				delete(f.groupRealmMappings, keyPrefix)
				f.mutated(writer, http.StatusNoContent)
			default:
				http.Error(writer, "method", http.StatusMethodNotAllowed)
			}
			return
		}
	}
	http.Error(writer, "unhandled organization group", http.StatusNotFound)
}

func (f *fakeKeycloak) deleteOrganizationGroup(organizationID, groupID string) {
	for childID, parentID := range f.groupParents {
		if parentID == groupID {
			f.deleteOrganizationGroup(organizationID, childID)
		}
	}
	delete(f.groups[organizationID], groupID)
	delete(f.groupParents, groupID)
	delete(f.groupMembers, groupID)
	prefix := organizationID + "/" + groupID + "/"
	for key := range f.groupClientMappings {
		if strings.HasPrefix(key, prefix) {
			delete(f.groupClientMappings, key)
		}
	}
	delete(f.groupRealmMappings, prefix)
}

func (f *fakeKeycloak) handleUsers(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) < 3 || tail[2] != "role-mappings" {
		http.Error(writer, "unhandled user", http.StatusNotFound)
		return
	}
	userID := tail[1]
	if len(tail) == 3 && request.Method == http.MethodGet {
		mappings := roleMappingsRepresentation{
			RealmMappings:  f.userRealmMappings[userID],
			ClientMappings: map[string]clientRoleMappingRepresentation{},
		}
		for targetID, roles := range f.userClientMappings[userID] {
			target := f.clients[targetID]
			mappings.ClientMappings[target.ClientID] = clientRoleMappingRepresentation{ID: targetID, Client: target.ClientID, Mappings: roles}
		}
		writeJSON(writer, http.StatusOK, mappings)
		return
	}
	if len(tail) == 5 && tail[3] == "clients" {
		switch request.Method {
		case http.MethodPost:
			var roles []roleRepresentation
			if decodeFakeRequest(writer, request, &roles) {
				f.userClientMappings[userID][tail[4]] = append(f.userClientMappings[userID][tail[4]], roles...)
				f.mutated(writer, http.StatusNoContent)
			}
		case http.MethodDelete:
			delete(f.userClientMappings[userID], tail[4])
			f.mutated(writer, http.StatusNoContent)
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	if len(tail) == 4 && tail[3] == "realm" && request.Method == http.MethodDelete {
		f.userRealmMappings[userID] = nil
		f.mutated(writer, http.StatusNoContent)
		return
	}
	http.Error(writer, "unhandled user role mapping", http.StatusNotFound)
}

func (f *fakeKeycloak) id(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s-%d", prefix, f.nextID)
}

func (f *fakeKeycloak) mutated(writer http.ResponseWriter, status int) {
	f.writes++
	writer.WriteHeader(status)
}

func (f *fakeKeycloak) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *fakeKeycloak) addUnmanagedClient(clientID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("client")
	f.clients[id] = clientRepresentation{ID: id, ClientID: clientID, Name: clientID, Enabled: true, Protocol: "openid-connect"}
	f.clientRoles[id] = map[string]roleRepresentation{}
	f.clientRoleComposites[id] = map[string][]roleRepresentation{}
	f.clientMappers[id] = map[string]protocolMapperRepresentation{}
	f.defaultScopes[id] = map[string]bool{}
	f.optionalScopes[id] = map[string]bool{}
	f.clientClientScopeMappings[id] = map[string][]roleRepresentation{}
}

func (f *fakeKeycloak) removeClient(clientID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID == clientID {
			delete(f.clients, id)
			delete(f.clientRoles, id)
			delete(f.clientRoleComposites, id)
			delete(f.clientMappers, id)
			delete(f.defaultScopes, id)
			delete(f.optionalScopes, id)
			delete(f.clientClientScopeMappings, id)
			return
		}
	}
}

func (f *fakeKeycloak) addUnmanagedIdentityProvider(alias string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.identityProviders[alias] = identityProviderRepresentation{
		Alias: alias, DisplayName: alias, ProviderID: "oidc", Enabled: true, Config: map[string]string{},
	}
}

func (f *fakeKeycloak) hasIdentityProvider(alias string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, exists := f.identityProviders[alias]
	return exists
}

func (f *fakeKeycloak) addIdentityProviderMapper(alias, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.id("idp-mapper")
	f.identityProviderMappers[alias][id] = identityProviderMapperRepresentation{
		ID: id, Name: name, IdentityProviderAlias: alias, IdentityProviderMapper: "oidc-hardcoded-group-idp-mapper", Config: map[string]string{},
	}
}

func (f *fakeKeycloak) identityProviderMapperNames(alias string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.identityProviderMappers[alias]))
	for _, mapper := range f.identityProviderMappers[alias] {
		names = append(names, mapper.Name)
	}
	sort.Strings(names)
	return names
}

func (f *fakeKeycloak) addRealmRole(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.realmRoles[name] = roleRepresentation{ID: f.id("realm-role"), Name: name}
}

func (f *fakeKeycloak) hasRealmRole(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, exists := f.realmRoles[name]
	return exists
}

func (f *fakeKeycloak) assignClientScope(clientID, assignment, scopeName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var clientUUID, scopeID string
	for id, client := range f.clients {
		if client.ClientID == clientID {
			clientUUID = id
		}
	}
	for id, scope := range f.clientScopes {
		if scope.Name == scopeName {
			scopeID = id
		}
	}
	if assignment == "default" {
		f.defaultScopes[clientUUID][scopeID] = true
	} else {
		f.optionalScopes[clientUUID][scopeID] = true
	}
}

func (f *fakeKeycloak) clientScopeNames(clientID, assignment string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var clientUUID string
	for id, client := range f.clients {
		if client.ClientID == clientID {
			clientUUID = id
		}
	}
	assigned := f.optionalScopes[clientUUID]
	if assignment == "default" {
		assigned = f.defaultScopes[clientUUID]
	}
	names := make([]string, 0, len(assigned))
	for scopeID := range assigned {
		names = append(names, f.clientScopes[scopeID].Name)
	}
	sort.Strings(names)
	return names
}

func (f *fakeKeycloak) addClientRealmScopeRole(clientID, roleName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var clientUUID string
	for id, client := range f.clients {
		if client.ClientID == clientID {
			clientUUID = id
		}
	}
	f.clientRealmMappings[clientUUID] = append(f.clientRealmMappings[clientUUID], f.realmRoles[roleName])
}

func (f *fakeKeycloak) addClientScopeRole(clientID, targetClientID, roleName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var clientUUID, targetUUID string
	for id, client := range f.clients {
		switch client.ClientID {
		case clientID:
			clientUUID = id
		case targetClientID:
			targetUUID = id
		}
	}
	f.clientClientScopeMappings[clientUUID][targetUUID] = append(f.clientClientScopeMappings[clientUUID][targetUUID], f.clientRoles[targetUUID][roleName])
}

func (f *fakeKeycloak) clientScopeRoleMappings(clientID string) roleMappingsRepresentation {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID == clientID {
			return f.clientRoleMappings(id)
		}
	}
	return roleMappingsRepresentation{}
}

func (f *fakeKeycloak) addOrganizationGroupClientRole(organizationAlias, groupName, clientID, roleName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var organizationID, groupID, clientUUID string
	for id, organization := range f.organizations {
		if organization.Alias == organizationAlias {
			organizationID = id
		}
	}
	for id, group := range f.groups[organizationID] {
		if group.Name == groupName {
			groupID = id
		}
	}
	for id, client := range f.clients {
		if client.ClientID == clientID {
			clientUUID = id
		}
	}
	key := organizationID + "/" + groupID + "/" + clientUUID
	f.groupClientMappings[key] = append(f.groupClientMappings[key], f.clientRoles[clientUUID][roleName])
}

func (f *fakeKeycloak) organizationGroupRoleMappingClients(organizationAlias, groupName string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var organizationID, groupID string
	for id, organization := range f.organizations {
		if organization.Alias == organizationAlias {
			organizationID = id
		}
	}
	for id, group := range f.groups[organizationID] {
		if group.Name == groupName {
			groupID = id
		}
	}
	prefix := organizationID + "/" + groupID + "/"
	var clientIDs []string
	for key, roles := range f.groupClientMappings {
		if len(roles) == 0 || !strings.HasPrefix(key, prefix) {
			continue
		}
		clientIDs = append(clientIDs, f.clients[strings.TrimPrefix(key, prefix)].ClientID)
	}
	sort.Strings(clientIDs)
	return clientIDs
}

func (f *fakeKeycloak) addClientRoleComposite(clientID, roleName, compositeName string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID != clientID {
			continue
		}
		role := f.clientRoles[id][roleName]
		role.Composite = true
		f.clientRoles[id][roleName] = role
		f.clientRoleComposites[id][roleName] = append(f.clientRoleComposites[id][roleName], f.clientRoles[id][compositeName])
		return
	}
}

func (f *fakeKeycloak) clientRoleCompositeNames(clientID, roleName string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for id, client := range f.clients {
		if client.ClientID == clientID {
			for _, role := range f.clientRoleComposites[id][roleName] {
				names = append(names, role.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func (f *fakeKeycloak) addClientMapper(clientID, name, provider string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID != clientID {
			continue
		}
		mapperID := f.id("mapper")
		f.clientMappers[id][mapperID] = protocolMapperRepresentation{
			ID: mapperID, Name: name, Protocol: "openid-connect", ProtocolMapper: provider, Config: map[string]string{},
		}
		return
	}
}

func (f *fakeKeycloak) injectClientAttribute(clientID, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID == clientID {
			client.Attributes[key] = value
			f.clients[id] = client
			return
		}
	}
}

func (f *fakeKeycloak) injectWalletAuthorizerDrift() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID != walletAuthorizerClientID {
			continue
		}
		client.StandardFlowEnabled = false
		client.ImplicitFlowEnabled = true
		client.DirectAccessGrantsEnabled = true
		client.ServiceAccountsEnabled = true
		client.AuthorizationServicesEnabled = true
		client.FullScopeAllowed = true
		client.RedirectURIs = []string{"https://hostile.invalid/callback"}
		client.WebOrigins = []string{"https://hostile.invalid"}
		client.Attributes = cloneStringMap(client.Attributes)
		client.Attributes["pkce.code.challenge.method"] = "plain"
		client.Attributes["default.acr.values"] = googleACR
		client.Attributes["minimum.acr.value"] = googleACR
		client.Attributes["hostile.attribute"] = "true"
		f.clients[id] = client
		return
	}
}

func (f *fakeKeycloak) injectRealmDrift() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.realm.SSLRequired = "none"
	f.realm.DefaultSignatureAlgorithm = "HS256"
	f.realm.ResetPasswordAllowed = true
	f.realm.BruteForceProtected = false
	f.realm.FailureFactor = 30
	f.realm.AccessCodeLifespan = 3_600
	f.realm.BrowserSecurityHeaders = cloneStringMap(f.realm.BrowserSecurityHeaders)
	f.realm.BrowserSecurityHeaders["xFrameOptions"] = "ALLOWALL"
	f.realm.Attributes = cloneStringPointerMap(f.realm.Attributes)
	hostile := "true"
	longPAR := "3600"
	f.realm.Attributes["hostile.attribute"] = &hostile
	f.realm.Attributes["parRequestUriLifespan"] = &longPAR
}

func (f *fakeKeycloak) injectAuthenticationDrift(state DesiredState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.realm.BrowserFlow = "browser"
	f.realm.FirstBrokerLoginFlow = "first broker login"
	f.realm.OTPPolicyAlgorithm = "HmacSHA1"
	f.realm.OTPPolicyCodeReusable = true
	f.realm.Attributes = cloneStringPointerMap(f.realm.Attributes)
	hostileACRMap := `{"urn:noebs:acr:google":1,"urn:noebs:acr:google-totp":1}`
	f.realm.Attributes["acr.loa.map"] = &hostileACRMap

	browserID := f.authenticationFlowIDByAlias(state.Authentication.BrowserFlow)
	loa1ID := f.authenticationFlowIDByAlias(googleLoA1FlowAlias)
	for index := range f.authenticationExecutions[loa1ID] {
		execution := &f.authenticationExecutions[loa1ID][index]
		if execution.ProviderID == "identity-provider-redirector" {
			f.authenticatorConfigs[execution.AuthenticationConfig] = authenticatorConfigRepresentation{
				ID: execution.AuthenticationConfig, Alias: "hostile-redirect", Config: map[string]string{"defaultProvider": "hostile"},
			}
		}
	}
	f.authenticationExecutions[browserID] = append(f.authenticationExecutions[browserID], authenticationExecutionInfoRepresentation{
		ID: f.id("hostile-execution"), ProviderID: "auth-username-password-form", Requirement: "ALTERNATIVE", Priority: 30,
	})
	for _, alias := range []string{googleTOTPLoA2FlowAlias, googlePostBrokerLoA2FlowAlias} {
		loa2ID := f.authenticationFlowIDByAlias(alias)
		for index := range f.authenticationExecutions[loa2ID] {
			execution := &f.authenticationExecutions[loa2ID][index]
			if execution.ProviderID != "conditional-level-of-authentication" {
				continue
			}
			execution.Requirement = "DISABLED"
			f.authenticatorConfigs[execution.AuthenticationConfig] = authenticatorConfigRepresentation{
				ID: execution.AuthenticationConfig, Alias: "hostile-loa2", Config: map[string]string{
					"loa-condition-level": "1",
					"loa-max-age":         "300",
				},
			}
		}
		f.authenticationExecutions[loa2ID] = append(f.authenticationExecutions[loa2ID], authenticationExecutionInfoRepresentation{
			ID: f.id("hostile-execution"), ProviderID: "idp-username-password-form", Requirement: "REQUIRED", Priority: 10,
		})
	}
	f.requiredActions[configureTOTPProvider] = requiredActionProviderRepresentation{
		Alias: configureTOTPProvider, Name: "Hostile OTP", ProviderID: configureTOTPProvider,
		Enabled: false, DefaultAction: false, Priority: 999, Config: map[string]string{"hostile": "true"},
	}
	password := f.requiredActions["UPDATE_PASSWORD"]
	password.Enabled = true
	password.DefaultAction = true
	f.requiredActions["UPDATE_PASSWORD"] = password
	rogueID := f.id("authentication-flow")
	f.authenticationFlows[rogueID] = authenticationFlowRepresentation{
		ID: rogueID, Alias: "hostile-password-flow", ProviderID: "basic-flow", TopLevel: true, BuiltIn: false,
	}
	f.authenticationExecutions[rogueID] = []authenticationExecutionInfoRepresentation{{
		ID: f.id("hostile-execution"), ProviderID: "auth-username-password-form", Requirement: "REQUIRED", Priority: 10,
	}}
	google := f.identityProviders["google"]
	google.FirstBrokerLoginFlowAlias = "first broker login"
	google.PostBrokerLoginFlowAlias = "hostile-password-flow"
	google.Config = cloneStringMap(google.Config)
	google.Config["forwardParameters"] = "acr_values"
	f.identityProviders["google"] = google

	for id, client := range f.clients {
		if client.ClientID == "admin-cli" {
			client.Enabled = true
			client.DirectAccessGrantsEnabled = true
			client.FullScopeAllowed = true
			f.clients[id] = client
		}
	}
}

func (f *fakeKeycloak) realmMatches(wanted realmRepresentation) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return realmMatches(*f.realm, wanted)
}

func (f *fakeKeycloak) injectBuiltinClientDrift() {
	f.mu.Lock()
	defer f.mu.Unlock()
	var adminCLI, broker, securityAdmin string
	for id, client := range f.clients {
		switch client.ClientID {
		case "admin-cli":
			adminCLI = id
			client.StandardFlowEnabled = true
			client.RedirectURIs = []string{"https://evil.example/*"}
			client.WebOrigins = []string{"*"}
			client.Attributes["hostile.attribute"] = "true"
			client.AuthenticationFlowBindingOverrides["browser"] = "hostile-flow"
			f.clients[id] = client
		case "broker":
			broker = id
		case "security-admin-console":
			securityAdmin = id
		}
	}
	delete(f.defaultScopes[adminCLI], f.clientScopeID("email"))
	mapperID := f.id("mapper")
	f.clientMappers[broker][mapperID] = protocolMapperRepresentation{
		ID: mapperID, Name: "hostile-claim", Protocol: "openid-connect", ProtocolMapper: "oidc-hardcoded-claim-mapper", Config: map[string]string{},
	}
	for id, mapper := range f.clientMappers[securityAdmin] {
		if mapper.Name == "locale" {
			mapper.Config["access.token.claim"] = "false"
			f.clientMappers[securityAdmin][id] = mapper
		}
	}
	f.clientRealmMappings[broker] = append(f.clientRealmMappings[broker], f.realmRoles["offline_access"])
}

func (f *fakeKeycloak) injectClientSecret(clientID, secret string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID == clientID {
			client.Secret = secret
			f.clients[id] = client
			return
		}
	}
}

func (f *fakeKeycloak) clientSecret(clientID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, client := range f.clients {
		if client.ClientID == clientID {
			return client.Secret
		}
	}
	return ""
}

func (f *fakeKeycloak) injectIdentityProviderSecret(alias, secret string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	provider := f.identityProviders[alias]
	provider.Config["clientSecret"] = secret
	f.identityProviders[alias] = provider
}

func (f *fakeKeycloak) identityProviderSecret(alias string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.identityProviders[alias].Config["clientSecret"]
}

func (f *fakeKeycloak) addUnmanagedOrganizationChildGroup(organizationAlias, parentName, name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var organizationID, parentID string
	for id, organization := range f.organizations {
		if organization.Alias == organizationAlias {
			organizationID = id
			break
		}
	}
	for id, group := range f.groups[organizationID] {
		if group.Name == parentName {
			parentID = id
			break
		}
	}
	groupID := f.id("group")
	f.groups[organizationID][groupID] = groupRepresentation{ID: groupID, Name: name}
	f.groupParents[groupID] = parentID
	f.groupMembers[groupID] = map[string]bool{}
	return groupID
}

func (f *fakeKeycloak) addOrganizationGroupMember(groupID, subject string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupMembers[groupID][subject] = true
}

func (f *fakeKeycloak) hasOrganizationGroupID(groupID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, groups := range f.groups {
		if _, found := groups[groupID]; found {
			return true
		}
	}
	return false
}

func (f *fakeKeycloak) hasOrganizationGroupMember(groupID, subject string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.groupMembers[groupID][subject]
}

func (f *fakeKeycloak) addOrganizationScopeMapper(name, provider string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for scopeID, scope := range f.clientScopes {
		if scope.Name != "organization" {
			continue
		}
		mapperID := f.id("mapper")
		f.scopeMappers[scopeID][mapperID] = protocolMapperRepresentation{
			ID: mapperID, Name: name, Protocol: "openid-connect", ProtocolMapper: provider, Config: map[string]string{},
		}
		return
	}
}

func (f *fakeKeycloak) injectClientMapperConfig(clientID, mapperName, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID != clientID {
			continue
		}
		for mapperID, mapper := range f.clientMappers[id] {
			if mapper.Name == mapperName {
				mapper.Config[key] = value
				f.clientMappers[id][mapperID] = mapper
				return
			}
		}
	}
}

func (f *fakeKeycloak) injectOrganizationScopeMapperConfig(mapperName, key, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for scopeID, scope := range f.clientScopes {
		if scope.Name != "organization" {
			continue
		}
		for mapperID, mapper := range f.scopeMappers[scopeID] {
			if mapper.Name == mapperName {
				mapper.Config[key] = value
				f.scopeMappers[scopeID][mapperID] = mapper
				return
			}
		}
	}
}

func (f *fakeKeycloak) clientMapperNames(clientID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for id, client := range f.clients {
		if client.ClientID == clientID {
			for _, mapper := range f.clientMappers[id] {
				names = append(names, mapper.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func (f *fakeKeycloak) organizationScopeMapperNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var names []string
	for scopeID, scope := range f.clientScopes {
		if scope.Name == "organization" {
			for _, mapper := range f.scopeMappers[scopeID] {
				names = append(names, mapper.Name)
			}
		}
	}
	sort.Strings(names)
	return names
}

func (f *fakeKeycloak) clientMapper(clientID, mapperName string) (protocolMapperRepresentation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, client := range f.clients {
		if client.ClientID == clientID {
			for _, mapper := range f.clientMappers[id] {
				if mapper.Name == mapperName {
					return mapper, true
				}
			}
		}
	}
	return protocolMapperRepresentation{}, false
}

func (f *fakeKeycloak) organizationScopeMapper(mapperName string) (protocolMapperRepresentation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for scopeID, scope := range f.clientScopes {
		if scope.Name == "organization" {
			for _, mapper := range f.scopeMappers[scopeID] {
				if mapper.Name == mapperName {
					return mapper, true
				}
			}
		}
	}
	return protocolMapperRepresentation{}, false
}

func (f *fakeKeycloak) addUnmanagedOrganization(alias, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	organizationID := f.id("organization")
	f.organizations[organizationID] = organizationRepresentation{ID: organizationID, Alias: alias, Name: name, Enabled: true}
	f.groups[organizationID] = map[string]groupRepresentation{}
}

func (f *fakeKeycloak) addUnmanagedOrganizationGroup(organizationAlias, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for organizationID, organization := range f.organizations {
		if organization.Alias == organizationAlias {
			groupID := f.id("group")
			f.groups[organizationID][groupID] = groupRepresentation{ID: groupID, Name: name}
			return
		}
	}
}

func (f *fakeKeycloak) hasOrganization(alias string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, organization := range f.organizations {
		if organization.Alias == alias {
			return true
		}
	}
	return false
}

func (f *fakeKeycloak) hasOrganizationGroup(organizationAlias, groupName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for organizationID, organization := range f.organizations {
		if organization.Alias != organizationAlias {
			continue
		}
		for _, group := range f.groups[organizationID] {
			if group.Name == groupName {
				return true
			}
		}
	}
	return false
}

func (f *fakeKeycloak) clientByClientID(clientID string) (clientRepresentation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, client := range f.clients {
		if client.ClientID == clientID {
			return client, true
		}
	}
	return clientRepresentation{}, false
}

func (f *fakeKeycloak) serviceAccountHasRole(clientID, targetClientID, roleName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	var serviceClient, target clientRepresentation
	for _, client := range f.clients {
		switch client.ClientID {
		case clientID:
			serviceClient = client
		case targetClientID:
			target = client
		}
	}
	user := f.serviceAccounts[serviceClient.ID]
	for _, role := range f.userClientMappings[user.ID][target.ID] {
		if role.Name == roleName {
			return true
		}
	}
	return false
}

func (f *fakeKeycloak) clientScopeHasRole(clientID, targetClientID, roleName string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	var client, target clientRepresentation
	for _, candidate := range f.clients {
		switch candidate.ClientID {
		case clientID:
			client = candidate
		case targetClientID:
			target = candidate
		}
	}
	for _, role := range f.clientClientScopeMappings[client.ID][target.ID] {
		if role.Name == roleName {
			return true
		}
	}
	return false
}

func (f *fakeKeycloak) organizationGroupRoles(organizationAlias, groupName string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var organizationID, groupID, apiClientID string
	for id, organization := range f.organizations {
		if organization.Alias == organizationAlias {
			organizationID = id
		}
	}
	for id, group := range f.groups[organizationID] {
		if group.Name == groupName {
			groupID = id
		}
	}
	for id, client := range f.clients {
		if client.ClientID == "noebs-api" {
			apiClientID = id
		}
	}
	var names []string
	for _, role := range f.groupClientMappings[organizationID+"/"+groupID+"/"+apiClientID] {
		names = append(names, role.Name)
	}
	sort.Strings(names)
	return names
}

func decodeFakeRequest(writer http.ResponseWriter, request *http.Request, target any) bool {
	if err := json.NewDecoder(request.Body).Decode(target); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func roleNames(roles []roleRepresentation) []string {
	names := make([]string, 0, len(roles))
	for _, role := range roles {
		names = append(names, role.Name)
	}
	sort.Strings(names)
	return names
}
