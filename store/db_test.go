package store

import (
	"errors"
	"testing"
)

func TestOpenFromConfigRequiresExplicitDriver(t *testing.T) {
	_, err := OpenFromConfig("postgres://noebs:noebs@postgres:5432/noebs?sslmode=disable", "")
	if !errors.Is(err, ErrMissingDatabaseDriver) {
		t.Fatalf("OpenFromConfig() error = %v, want %v", err, ErrMissingDatabaseDriver)
	}
}

func TestOpenFromConfigRequiresPostgresURL(t *testing.T) {
	_, err := OpenFromConfig("", DriverPostgres)
	if !errors.Is(err, ErrMissingDatabaseURL) {
		t.Fatalf("OpenFromConfig() error = %v, want %v", err, ErrMissingDatabaseURL)
	}
}

func TestOpenFromConfigRejectsSQLiteDriver(t *testing.T) {
	_, err := OpenFromConfig(":memory:", "sqlite3")
	if !errors.Is(err, ErrUnsupportedDatabaseDriver) {
		t.Fatalf("OpenFromConfig() error = %v, want %v", err, ErrUnsupportedDatabaseDriver)
	}
}

func TestOpenFromConfigRejectsUnsupportedDriver(t *testing.T) {
	_, err := OpenFromConfig("postgres://noebs:noebs@postgres:5432/noebs?sslmode=disable", "mysql")
	if !errors.Is(err, ErrUnsupportedDatabaseDriver) {
		t.Fatalf("OpenFromConfig() error = %v, want %v", err, ErrUnsupportedDatabaseDriver)
	}
}
