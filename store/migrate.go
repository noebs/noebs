package store

import (
	"context"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/sqlite/*.sql
var sqliteMigrations embed.FS

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

	switch db.Driver {
	case DriverPostgres:
		if err := goose.SetDialect("postgres"); err != nil {
			return err
		}
		goose.SetBaseFS(postgresMigrations)
		return goose.UpContext(ctx, db.DB.DB, "migrations/postgres")
	default:
		if err := goose.SetDialect("sqlite3"); err != nil {
			return err
		}
		goose.SetBaseFS(sqliteMigrations)
		return goose.UpContext(ctx, db.DB.DB, "migrations/sqlite")
	}
}
