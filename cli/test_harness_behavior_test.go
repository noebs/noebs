package main

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestCLIHarnessInitializesAPIGatewayBoundary(t *testing.T) {
	ensureInit()
	role, err := currentServiceRole()
	if err != nil {
		t.Fatal(err)
	}
	if role != serviceRoleAPIGateway {
		t.Fatalf("CLI test startup role = %q, want %q", role, serviceRoleAPIGateway)
	}
	if backofficeAuthHandler == nil {
		t.Fatal("CLI test startup did not initialize the API-gateway BFF boundary")
	}
}

func TestCLIHarnessRunsDatabaseIndependentTestsWithoutPostgres(t *testing.T) {
	if testPostgres != nil {
		t.Skip("PostgreSQL test environment is available")
	}
	if _, err := os.Stat(testConfigPath); err != nil {
		t.Fatalf("database-independent test config: %v", err)
	}
	cfg := cliTestConfig(serviceRoleAPIGateway, "")
	if cfg.ServiceRole != string(serviceRoleAPIGateway) || cfg.DatabaseURL != "" {
		t.Fatalf("database-independent config has role %q and db_url %q", cfg.ServiceRole, cfg.DatabaseURL)
	}
	if database == nil || storeSvc == nil {
		t.Fatal("database-unavailable boundary was not installed")
	}
}

func TestCLIHarnessDatabaseBoundaryFailsClosedWithoutPostgres(t *testing.T) {
	if testPostgres != nil {
		t.Skip("PostgreSQL test environment is available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("database operation succeeded without a PostgreSQL test environment")
	}
}
