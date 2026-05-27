package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

const (
	DriverPostgres = "pgx"
)

var (
	ErrMissingDatabaseDriver     = errors.New("missing database driver")
	ErrMissingDatabaseURL        = errors.New("missing database URL")
	ErrUnsupportedDatabaseDriver = errors.New("unsupported database driver")
)

// DB wraps sqlx.DB with metadata.
type DB struct {
	*sqlx.DB
	Driver string
}

// OpenFromConfig opens a service Postgres database.
func OpenFromConfig(dbURL, driverOverride string) (*DB, error) {
	sqlx.NameMapper = toSnake

	driver := strings.ToLower(strings.TrimSpace(driverOverride))
	dsn := ""

	switch driver {
	case "":
		return nil, ErrMissingDatabaseDriver
	case "postgres", "pgx":
		if dbURL == "" {
			return nil, fmt.Errorf("%w: db_url required for %s driver", ErrMissingDatabaseURL, driver)
		}
		driver = DriverPostgres
		dsn = dbURL
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDatabaseDriver, driver)
	}

	db, err := sqlx.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, err
	}
	return &DB{DB: db, Driver: driver}, nil
}

func toSnake(s string) string {
	var out strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := rune(s[i-1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) {
					out.WriteByte('_')
				}
			}
			out.WriteRune(unicode.ToLower(r))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (db *DB) Close() error {
	if db == nil || db.DB == nil {
		return nil
	}
	return db.DB.Close()
}

func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error) {
	if db == nil || db.DB == nil {
		return nil, fmt.Errorf("nil db")
	}
	return db.DB.BeginTxx(ctx, opts)
}
