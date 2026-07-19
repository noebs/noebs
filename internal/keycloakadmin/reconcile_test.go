package keycloakadmin

import (
	"context"
	"encoding/json"
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
	server := httptest.NewServer(fake)
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
	if fake.writeCount() != firstWrites {
		t.Fatalf("second Reconcile() issued %d writes", fake.writeCount()-firstWrites)
	}

	backoffice, ok := fake.clientByClientID("noebs-backoffice")
	if !ok || backoffice.PublicClient || backoffice.Secret != "backoffice-secret" {
		t.Fatalf("backoffice client = %#v", backoffice)
	}
	if backoffice.Attributes["post.logout.redirect.uris"] != "https://api.noebs.sd/backoffice/oauth/logout/callback" {
		t.Fatalf("backoffice post logout URI = %q", backoffice.Attributes["post.logout.redirect.uris"])
	}
	if len(backoffice.WebOrigins) != 0 {
		t.Fatalf("backoffice web origins = %v", backoffice.WebOrigins)
	}
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

func TestDeleteBootstrapClient(t *testing.T) {
	deleted := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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

	nextID int
	writes int
	realm  *realmRepresentation

	realmRoles                map[string]roleRepresentation
	clients                   map[string]clientRepresentation
	clientRoles               map[string]map[string]roleRepresentation
	clientMappers             map[string]map[string]protocolMapperRepresentation
	clientScopes              map[string]clientScopeRepresentation
	scopeMappers              map[string]map[string]protocolMapperRepresentation
	optionalScopes            map[string]map[string]bool
	clientRealmMappings       map[string][]roleRepresentation
	clientClientScopeMappings map[string]map[string][]roleRepresentation
	serviceAccounts           map[string]userRepresentation
	userClientMappings        map[string]map[string][]roleRepresentation
	userRealmMappings         map[string][]roleRepresentation
	identityProviders         map[string]identityProviderRepresentation
	organizations             map[string]organizationRepresentation
	groups                    map[string]map[string]groupRepresentation
	groupClientMappings       map[string][]roleRepresentation
	groupRealmMappings        map[string][]roleRepresentation
}

func newFakeKeycloak() *fakeKeycloak {
	return &fakeKeycloak{
		realmRoles:                map[string]roleRepresentation{},
		clients:                   map[string]clientRepresentation{},
		clientRoles:               map[string]map[string]roleRepresentation{},
		clientMappers:             map[string]map[string]protocolMapperRepresentation{},
		clientScopes:              map[string]clientScopeRepresentation{},
		scopeMappers:              map[string]map[string]protocolMapperRepresentation{},
		optionalScopes:            map[string]map[string]bool{},
		clientRealmMappings:       map[string][]roleRepresentation{},
		clientClientScopeMappings: map[string]map[string][]roleRepresentation{},
		serviceAccounts:           map[string]userRepresentation{},
		userClientMappings:        map[string]map[string][]roleRepresentation{},
		userRealmMappings:         map[string][]roleRepresentation{},
		identityProviders:         map[string]identityProviderRepresentation{},
		organizations:             map[string]organizationRepresentation{},
		groups:                    map[string]map[string]groupRepresentation{},
		groupClientMappings:       map[string][]roleRepresentation{},
		groupRealmMappings:        map[string][]roleRepresentation{},
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
	default:
		http.Error(writer, "unhandled "+request.Method+" "+request.URL.RequestURI(), http.StatusNotFound)
	}
}

func (f *fakeKeycloak) initializeRealmBuiltins() {
	managementID := f.id("client")
	f.clients[managementID] = clientRepresentation{ID: managementID, ClientID: "realm-management", Name: "realm-management"}
	f.clientRoles[managementID] = map[string]roleRepresentation{
		"realm-admin": {ID: f.id("role"), Name: "realm-admin", ClientRole: true, ContainerID: managementID},
	}
	scopeID := f.id("scope")
	f.clientScopes[scopeID] = clientScopeRepresentation{ID: scopeID, Name: "organization", Protocol: "openid-connect"}
	mapperID := f.id("mapper")
	f.scopeMappers[scopeID] = map[string]protocolMapperRepresentation{
		mapperID: {
			ID: mapperID, Name: "organization membership", Protocol: "openid-connect",
			ProtocolMapper: organizationMapperID, Config: map[string]string{},
		},
	}
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
	if request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, role)
		return
	}
	http.Error(writer, "unhandled role mutation", http.StatusNotFound)
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
			f.clients[client.ID] = client
			f.clientRoles[client.ID] = map[string]roleRepresentation{}
			f.clientMappers[client.ID] = map[string]protocolMapperRepresentation{}
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
			f.clients[clientID] = replacement
			f.mutated(writer, http.StatusNoContent)
		}
		return
	}
	if len(tail) == 3 && tail[2] == "service-account-user" && request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, f.serviceAccounts[clientID])
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
	if len(tail) >= 3 && tail[2] == "optional-client-scopes" {
		if len(tail) == 3 && request.Method == http.MethodGet {
			var scopes []clientScopeRepresentation
			for scopeID := range f.optionalScopes[clientID] {
				scopes = append(scopes, f.clientScopes[scopeID])
			}
			writeJSON(writer, http.StatusOK, scopes)
			return
		}
		if len(tail) == 4 && request.Method == http.MethodPut {
			f.optionalScopes[clientID][tail[3]] = true
			f.mutated(writer, http.StatusNoContent)
			return
		}
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
	if request.Method == http.MethodGet {
		writeJSON(writer, http.StatusOK, role)
		return
	}
	http.Error(writer, "unhandled client role", http.StatusNotFound)
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
	http.Error(writer, "unhandled mapper", http.StatusNotFound)
}

func (f *fakeKeycloak) handleIdentityProviders(writer http.ResponseWriter, request *http.Request, tail []string) {
	if len(tail) != 2 || tail[1] != "instances" {
		http.Error(writer, "unhandled identity provider", http.StatusNotFound)
		return
	}
	switch request.Method {
	case http.MethodGet:
		result := make([]identityProviderRepresentation, 0, len(f.identityProviders))
		for _, provider := range f.identityProviders {
			result = append(result, provider)
		}
		writeJSON(writer, http.StatusOK, result)
	case http.MethodPost:
		var provider identityProviderRepresentation
		if decodeFakeRequest(writer, request, &provider) {
			f.identityProviders[provider.Alias] = provider
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
	if _, found := f.organizations[organizationID]; !found {
		http.Error(writer, "organization not found", http.StatusNotFound)
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
			for _, group := range groups {
				result = append(result, group)
			}
			writeJSON(writer, http.StatusOK, result)
		case http.MethodPost:
			var group groupRepresentation
			if decodeFakeRequest(writer, request, &group) {
				group.ID = f.id("group")
				groups[group.ID] = group
				f.mutated(writer, http.StatusCreated)
			}
		default:
			http.Error(writer, "method", http.StatusMethodNotAllowed)
		}
		return
	}
	groupID := tail[0]
	if _, found := groups[groupID]; !found {
		http.Error(writer, "group not found", http.StatusNotFound)
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
			default:
				http.Error(writer, "method", http.StatusMethodNotAllowed)
			}
			return
		}
		if len(tail) == 3 && tail[2] == "realm" && request.Method == http.MethodGet {
			writeJSON(writer, http.StatusOK, f.groupRealmMappings[keyPrefix])
			return
		}
	}
	http.Error(writer, "unhandled organization group", http.StatusNotFound)
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
	if len(tail) == 5 && tail[3] == "clients" && request.Method == http.MethodPost {
		var roles []roleRepresentation
		if decodeFakeRequest(writer, request, &roles) {
			f.userClientMappings[userID][tail[4]] = append(f.userClientMappings[userID][tail[4]], roles...)
			f.mutated(writer, http.StatusNoContent)
		}
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
