package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
)

const (
	DriverPostgres = "pgx"
	DriverSQLite   = "sqlite3"
)

// DB wraps sqlx.DB with metadata.
type DB struct {
	*sqlx.DB
	Driver string
}

// OpenFromConfig opens a database based on the provided url/path.
func OpenFromConfig(dbURL, sqlitePath, driverOverride string) (*DB, error) {
	sqlx.NameMapper = toSnake

	driver := strings.TrimSpace(driverOverride)
	dsn := ""

	switch strings.ToLower(driver) {
	case "", "default":
		if dbURL != "" {
			driver = DriverPostgres
			dsn = dbURL
		} else {
			driver = DriverSQLite
			if sqlitePath == "" {
				sqlitePath = "test.db"
			}
			dsn = sqlitePath
		}
	case "postgres", "pgx":
		if dbURL == "" {
			return nil, fmt.Errorf("db_url required for %s driver", driver)
		}
		driver = DriverPostgres
		dsn = dbURL
	case "sqlite", "sqlite3":
		driver = DriverSQLite
		if sqlitePath == "" {
			sqlitePath = "test.db"
		}
		dsn = sqlitePath
	default:
		if dbURL != "" {
			dsn = dbURL
		} else {
			if sqlitePath == "" {
				sqlitePath = "test.db"
			}
			dsn = sqlitePath
		}
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
