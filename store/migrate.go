package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/postgres/*.sql
var postgresMigrations embed.FS

var migrationDriver string
var migrationDefaultTenant string

// Migrate applies embedded SQL/Go migrations using goose.
func Migrate(ctx context.Context, db *DB, defaultTenantID string) error {
	if db == nil || db.DB == nil {
		return fmt.Errorf("db is nil")
	}

	migrationDriver = db.Driver
	migrationDefaultTenant = defaultTenantID
	if migrationDefaultTenant == "" {
		migrationDefaultTenant = DefaultTenantID
	}

	if db.Driver != DriverPostgres {
		return fmt.Errorf("unsupported migration driver %q (postgres only)", db.Driver)
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return err
	}
	goose.SetBaseFS(postgresMigrations)
	return goose.UpContext(ctx, db.DB.DB, "migrations/postgres")
}
