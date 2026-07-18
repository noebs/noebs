package workloadauth

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrInvalidNonceClaim = errors.New("invalid workload nonce claim")

type postgresExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// PostgresNonceStore is a durable replay boundary shared by every receiver
// replica. Request handling only inserts; retention deletion belongs to the
// separately credentialed cleanup command.
type PostgresNonceStore struct {
	db postgresExecer
}

func NewPostgresNonceStore(db postgresExecer) (*PostgresNonceStore, error) {
	if db == nil {
		return nil, ErrInvalidConfiguration
	}
	return &PostgresNonceStore{db: db}, nil
}

func (s *PostgresNonceStore) Use(ctx context.Context, keyID, audience, nonce string, expiresAt time.Time) (bool, error) {
	if s == nil || s.db == nil || ctx == nil || keyID == "" || audience == "" || nonce == "" || expiresAt.IsZero() {
		return false, ErrInvalidNonceClaim
	}
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO workload_request_nonces(key_id, audience, nonce, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (key_id, audience, nonce) DO NOTHING
	`, keyID, audience, nonce, expiresAt.UTC())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows != 0 && rows != 1 {
		return false, ErrInvalidNonceClaim
	}
	return rows == 1, nil
}
