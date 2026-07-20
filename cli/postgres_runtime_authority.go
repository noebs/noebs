package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/adonese/noebs/store"
)

var (
	errPostgresDatabaseIdentity = errors.New("postgres database identity mismatch")
	errPostgresSessionAuthority = errors.New("postgres session authority mismatch")
)

func validatePostgresDatabaseIdentity(rawURL string, spec postgresRoleSpec) error {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fmt.Errorf("%w: parse database URL: %v", errPostgresDatabaseIdentity, err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("%w: database URL scheme must be postgres", errPostgresDatabaseIdentity)
	}
	if parsed.Host == "" || parsed.User == nil {
		return fmt.Errorf("%w: database URL must include host and user", errPostgresDatabaseIdentity)
	}
	if username := parsed.User.Username(); username != spec.username {
		return fmt.Errorf("%w: database URL user is %q, want %q", errPostgresDatabaseIdentity, username, spec.username)
	}
	if parsed.Path != "/"+spec.database {
		return fmt.Errorf("%w: database URL database is %q, want %q", errPostgresDatabaseIdentity, strings.TrimPrefix(parsed.Path, "/"), spec.database)
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("%w: database URL must not contain a fragment", errPostgresDatabaseIdentity)
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return fmt.Errorf("%w: parse database URL query: %v", errPostgresDatabaseIdentity, err)
	}
	for key, values := range query {
		if len(values) != 1 {
			return fmt.Errorf("%w: database URL query parameter %q must appear once", errPostgresDatabaseIdentity, key)
		}
		switch strings.ToLower(key) {
		case "database", "dbname", "host", "options", "password", "port", "role", "service", "servicefile", "user":
			return fmt.Errorf("%w: database URL query must not override %q", errPostgresDatabaseIdentity, key)
		}
	}
	return nil
}

func requirePostgresSessionAuthority(db *store.DB, spec postgresRoleSpec) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sessionUser, currentUser, databaseName string
	if err := db.QueryRowxContext(ctx, `SELECT session_user, current_user, current_database()`).Scan(
		&sessionUser,
		&currentUser,
		&databaseName,
	); err != nil {
		return fmt.Errorf("%w: inspect session: %v", errPostgresSessionAuthority, err)
	}
	if sessionUser != spec.username || currentUser != spec.username || databaseName != spec.database {
		return fmt.Errorf(
			"%w: session_user=%q current_user=%q database=%q, want role=%q database=%q",
			errPostgresSessionAuthority,
			sessionUser,
			currentUser,
			databaseName,
			spec.username,
			spec.database,
		)
	}
	return nil
}

func openPostgresDatabaseWithAuthority(dbURL, driver, caCertificate string, spec postgresRoleSpec) (*store.DB, error) {
	db, err := store.OpenFromConfigWithCACertificate(dbURL, driver, caCertificate)
	if err != nil {
		return nil, err
	}
	if err := requirePostgresSessionAuthority(db, spec); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
