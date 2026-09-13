package accountenrollment

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) (*PostgresStore, error) {
	if db == nil {
		return nil, ErrInvalidConfig
	}
	return &PostgresStore{db: db}, nil
}

func (s *PostgresStore) WithIdentity(ctx context.Context, identity Identity, work func(ProgressAccess) error) error {
	if identity.Issuer == "" || identity.Subject == "" || identity.TenantID == "" || work == nil {
		return ErrInvalidIdentity
	}
	connection, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer connection.Close()
	// Session lock serializes retries across replicas. Individual saves commit
	// before the next external mutation, preserving completed targets on failure.
	key := identity.Issuer + "\x1f" + identity.Subject + "\x1f" + identity.TenantID
	if _, err := connection.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 49287))`, key); err != nil {
		return err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if _, err := connection.ExecContext(cleanup, `SELECT pg_advisory_unlock(hashtextextended($1, 49287))`, key); err != nil {
			_ = connection.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	return work(postgresProgress{connection: connection, identity: identity})
}

type postgresProgress struct {
	connection *sql.Conn
	identity   Identity
}

func (p postgresProgress) Load(ctx context.Context) (*Progress, error) {
	var payload []byte
	err := p.connection.QueryRowContext(ctx, `SELECT progress FROM account_enrollments WHERE issuer=$1 AND subject=$2 AND tenant_id=$3`, p.identity.Issuer, p.identity.Subject, p.identity.TenantID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result Progress
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode account enrollment: %w", err)
	}
	return &result, nil
}
func (p postgresProgress) Save(ctx context.Context, progress Progress) error {
	payload, err := json.Marshal(progress)
	if err != nil {
		return err
	}
	_, err = p.connection.ExecContext(ctx, `INSERT INTO account_enrollments (issuer,subject,tenant_id,progress) VALUES ($1,$2,$3,$4::jsonb) ON CONFLICT (issuer,subject,tenant_id) DO UPDATE SET progress=EXCLUDED.progress, updated_at=clock_timestamp()`, p.identity.Issuer, p.identity.Subject, p.identity.TenantID, payload)
	return err
}
