package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

const (
	DriverPostgres = "pgx"
)

var (
	ErrMissingDatabaseDriver     = errors.New("missing database driver")
	ErrMissingDatabaseURL        = errors.New("missing database URL")
	ErrUnsupportedDatabaseDriver = errors.New("unsupported database driver")
	ErrInvalidDatabaseTLS        = errors.New("invalid database TLS configuration")
)

// DB wraps sqlx.DB with metadata.
type DB struct {
	*sqlx.DB
	Driver string
}

// OpenFromConfig opens a service Postgres database.
func OpenFromConfig(dbURL, driverOverride string) (*DB, error) {
	return OpenFromConfigWithCACertificate(dbURL, driverOverride, "")
}

// OpenFromConfigWithCACertificate opens a service Postgres database and,
// when a release CA is supplied, pins the server chain and hostname instead
// of relying on process-global trust roots.
func OpenFromConfigWithCACertificate(dbURL, driverOverride, caCertificate string) (*DB, error) {
	sqlx.NameMapper = toSnake

	driver := strings.ToLower(strings.TrimSpace(driverOverride))
	switch driver {
	case "":
		return nil, ErrMissingDatabaseDriver
	case "postgres", "pgx":
		if dbURL == "" {
			return nil, fmt.Errorf("%w: db_url required for %s driver", ErrMissingDatabaseURL, driver)
		}
		driver = DriverPostgres
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedDatabaseDriver, driver)
	}

	var db *sqlx.DB
	caCertificate = strings.TrimSpace(caCertificate)
	if caCertificate == "" {
		var err error
		db, err = sqlx.Open(driver, dbURL)
		if err != nil {
			return nil, err
		}
	} else {
		config, err := postgresTLSConfig(dbURL, caCertificate)
		if err != nil {
			return nil, err
		}
		db = sqlx.NewDb(stdlib.OpenDB(*config), driver)
	}
	return pingDatabase(db, driver)
}

func postgresTLSConfig(dbURL, caCertificate string) (*pgx.ConnConfig, error) {
	parsedURL, err := url.Parse(dbURL)
	if err != nil || parsedURL.Query().Get("sslmode") != "verify-full" {
		return nil, fmt.Errorf("%w: db_url must set sslmode=verify-full", ErrInvalidDatabaseTLS)
	}
	block, rest := pem.Decode([]byte(caCertificate))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return nil, fmt.Errorf("%w: CA certificate", ErrInvalidDatabaseTLS)
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !ca.IsCA {
		return nil, fmt.Errorf("%w: CA certificate", ErrInvalidDatabaseTLS)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	config, err := pgx.ParseConfig(dbURL)
	if err != nil {
		return nil, fmt.Errorf("%w: parse db_url: %v", ErrInvalidDatabaseTLS, err)
	}
	config.TLSConfig = &tls.Config{
		MinVersion: tls.VersionTLS13,
		RootCAs:    roots,
		ServerName: config.Host,
	}
	return config, nil
}

func ValidateDatabaseTLSConfig(dbURL, caCertificate string) error {
	_, err := postgresTLSConfig(dbURL, strings.TrimSpace(caCertificate))
	return err
}

func pingDatabase(db *sqlx.DB, driver string) (*DB, error) {
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
