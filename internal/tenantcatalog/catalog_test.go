package tenantcatalog

import (
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestRepositoryCatalog(t *testing.T) {
	file, err := os.Open("../../deploy/kubernetes/keycloak-authority/tenant-catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Load(file)
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close repository catalog: %v", closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	want := []Tenant{
		{ID: "tenant-cutover", Name: "Tenant Cutover"},
		{ID: "tenant-sandbox", Name: "Tenant Sandbox"},
	}
	if got := catalog.All(); !slices.Equal(got, want) {
		t.Fatalf("catalog = %#v, want %#v", got, want)
	}
	for _, tenant := range want {
		if got, err := catalog.Require(string(tenant.ID)); err != nil || got != tenant {
			t.Fatalf("Require(%q) = %#v, %v", tenant.ID, got, err)
		}
	}
}

func TestCatalogRejectsInvalidDocuments(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "unknown field", yaml: "api_version: noebs.sd/tenants/v1\ntenants: []\nunknown: true\n"},
		{name: "noncanonical ID", yaml: "api_version: noebs.sd/tenants/v1\ntenants:\n  - id: Tenant-A\n    name: Tenant A\n"},
		{name: "unordered", yaml: "api_version: noebs.sd/tenants/v1\ntenants:\n  - id: tenant-b\n    name: Tenant B\n  - id: tenant-a\n    name: Tenant A\n"},
		{name: "duplicate", yaml: "api_version: noebs.sd/tenants/v1\ntenants:\n  - id: tenant-a\n    name: Tenant A\n  - id: tenant-a\n    name: Other\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := Load(strings.NewReader(test.yaml)); !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("Load() error = %v, want ErrInvalidCatalog", err)
			}
		})
	}
}

func TestCatalogRequireUsesTypedErrors(t *testing.T) {
	catalog, err := Load(strings.NewReader("api_version: noebs.sd/tenants/v1\ntenants:\n  - id: tenant-a\n    name: Tenant A\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Require(" tenant-a "); !errors.Is(err, ErrInvalidTenantID) {
		t.Fatalf("noncanonical error = %v, want ErrInvalidTenantID", err)
	}
	if _, err := catalog.Require("tenant-b"); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("unknown error = %v, want ErrUnknownTenant", err)
	}
}

func TestNewValidatesProgrammaticCatalogs(t *testing.T) {
	tenants := []Tenant{{ID: "tenant-a", Name: "Tenant A"}}
	catalog, err := New(tenants)
	if err != nil {
		t.Fatal(err)
	}
	tenants[0].Name = "mutated"
	if got := catalog.All()[0].Name; got != "Tenant A" {
		t.Fatalf("catalog name = %q, want immutable input copy", got)
	}
	if _, err := New([]Tenant{{ID: "tenant_1", Name: "Tenant One"}}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("New() error = %v, want ErrInvalidCatalog", err)
	}
	if _, err := New([]Tenant{{ID: "default", Name: "Default"}}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("New() reserved ID error = %v, want ErrInvalidCatalog", err)
	}
}
