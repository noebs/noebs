package keycloakadmin

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/adonese/noebs/internal/tenantcatalog"
)

func TestRepositoryDesiredStateContract(t *testing.T) {
	file, err := os.Open("../../deploy/kubernetes/keycloak-authority/keycloak-desired-state.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	state, err := LoadDesiredState(file, repositoryTenantCatalog(t))
	if err != nil {
		t.Fatalf("LoadDesiredState() error = %v", err)
	}

	clients := make(map[string]InteractiveClient, len(state.InteractiveClients))
	for _, client := range state.InteractiveClients {
		clients[client.ClientID] = client
	}
	if clients["noebs-mobile"].AccessType != "public" {
		t.Fatalf("noebs-mobile access type = %q", clients["noebs-mobile"].AccessType)
	}
	if clients["noebs-backoffice"].AccessType != "confidential" || clients["noebs-backoffice"].Credential == "" {
		t.Fatalf("noebs-backoffice contract = %#v", clients["noebs-backoffice"])
	}
	if !exactStringSet(stringSliceSet(state.ReconcilerClient.RealmManagementRoles), "realm-admin") {
		t.Fatalf("reconciler realm roles = %v", state.ReconcilerClient.RealmManagementRoles)
	}
	if len(state.RealmRoles) != 0 {
		t.Fatalf("realm roles = %v, want none", state.RealmRoles)
	}
	if len(state.Organizations) != 2 {
		t.Fatalf("organizations = %d, want 2", len(state.Organizations))
	}
}

func TestDesiredStateRejectsWildcardRedirect(t *testing.T) {
	state := repositoryDesiredState(t)
	state.InteractiveClients[0].RedirectURIs = []string{"https://api.noebs.sd/*"}
	if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
		t.Fatalf("Validate() error = %v, want ErrInvalidDesiredState", err)
	}
}

func TestDesiredStateRejectsPublicBackofficeClient(t *testing.T) {
	state := repositoryDesiredState(t)
	for index := range state.InteractiveClients {
		if state.InteractiveClients[index].ClientID == "noebs-backoffice" {
			state.InteractiveClients[index].AccessType = "public"
			state.InteractiveClients[index].Credential = ""
		}
	}
	if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
		t.Fatalf("Validate() error = %v, want ErrInvalidDesiredState", err)
	}
}

func TestDesiredStateRejectsOrganizationMapperNameDrift(t *testing.T) {
	state := repositoryDesiredState(t)
	state.OrganizationClaim.MapperName = "organization"
	if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
		t.Fatalf("Validate() error = %v, want ErrInvalidDesiredState", err)
	}
}

func TestDesiredStateRejectsRealmRoles(t *testing.T) {
	state := repositoryDesiredState(t)
	state.RealmRoles = []Role{{Name: "platform-admin", Description: "legacy global bypass"}}
	if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
		t.Fatalf("Validate() error = %v, want ErrInvalidDesiredState", err)
	}
}

func TestDesiredStateOrganizationsExactlyMatchTenantCatalog(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DesiredState)
	}{
		{name: "missing", mutate: func(state *DesiredState) {
			state.Organizations = state.Organizations[:1]
		}},
		{name: "extra", mutate: func(state *DesiredState) {
			state.Organizations = append(state.Organizations, state.Organizations[0])
			state.Organizations[2].Alias = "tenant-extra"
		}},
		{name: "alias", mutate: func(state *DesiredState) {
			state.Organizations[0].Alias = "tenant-renamed"
		}},
		{name: "name", mutate: func(state *DesiredState) {
			state.Organizations[0].Name = "Renamed Tenant"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := repositoryDesiredState(t)
			test.mutate(&state)
			if err := state.Validate(); !errors.Is(err, ErrInvalidDesiredState) {
				t.Fatalf("Validate() error = %v, want ErrInvalidDesiredState", err)
			}
		})
	}
}

func TestDesiredStateRejectsUnknownField(t *testing.T) {
	_, err := LoadDesiredState(strings.NewReader("api_version: noebs.sd/keycloak/v1\nunknown: true\n"), repositoryTenantCatalog(t))
	if !errors.Is(err, ErrInvalidDesiredState) {
		t.Fatalf("LoadDesiredState() error = %v, want ErrInvalidDesiredState", err)
	}
}

func TestConfigRejectsMissingAdministrativeClientSecret(t *testing.T) {
	config := validTestConfig("https://keycloak.test")
	config.ClientSecret = ""
	if err := config.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestConfigRejectsPermanentMasterRealmClient(t *testing.T) {
	config := validTestConfig("https://keycloak.test")
	config.ClientID = "noebs-keycloak-reconciler"
	if err := config.Validate(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Validate() error = %v, want ErrInvalidConfig", err)
	}
}

func TestSteadyRealmLocalConfig(t *testing.T) {
	config := validTestConfig("https://keycloak.test")
	config.AdminRealm = "noebs"
	config.ClientID = "noebs-keycloak-reconciler"
	config.ClientSecret = config.ClientCredentials["noebs-keycloak-reconciler"].ClientSecret
	if err := config.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func repositoryDesiredState(t *testing.T) DesiredState {
	t.Helper()
	file, err := os.Open("../../deploy/kubernetes/keycloak-authority/keycloak-desired-state.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	state, err := LoadDesiredState(file, repositoryTenantCatalog(t))
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func repositoryTenantCatalog(t *testing.T) tenantcatalog.Catalog {
	t.Helper()
	catalog, err := tenantcatalog.LoadFile("../../deploy/kubernetes/keycloak-authority/tenant-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func validTestConfig(baseURL string) Config {
	return Config{
		BaseURL:      baseURL,
		AdminRealm:   "master",
		ClientID:     BootstrapClientID,
		ClientSecret: "temporary-bootstrap-secret",
		ClientCredentials: map[string]ClientCredential{
			"noebs-keycloak-reconciler": {ClientSecret: "steady-reconciler-secret"},
			"noebs-backoffice":          {ClientSecret: "backoffice-secret"},
		},
		IdentityProviders: map[string]IdentityProviderCredential{
			"google": {ClientID: "google-client", ClientSecret: "google-secret"},
		},
	}
}
