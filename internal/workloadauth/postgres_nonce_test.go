package workloadauth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type recordingExecer struct {
	query string
	args  []any
	rows  int64
	err   error
}

func (e *recordingExecer) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	e.query = query
	e.args = args
	return recordingResult(e.rows), e.err
}

type recordingResult int64

func (recordingResult) LastInsertId() (int64, error)   { return 0, errors.New("unsupported") }
func (r recordingResult) RowsAffected() (int64, error) { return int64(r), nil }

func TestCleanupExpiredUsesExactRetentionBoundary(t *testing.T) {
	before := time.Date(2026, time.July, 19, 12, 30, 0, 0, time.FixedZone("test", 2*60*60))
	db := &recordingExecer{rows: 7}

	deleted, err := CleanupExpired(context.Background(), db, before)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 7 {
		t.Fatalf("deleted = %d, want 7", deleted)
	}
	if strings.TrimSpace(db.query) != "DELETE FROM workload_request_nonces WHERE expires_at < $1" {
		t.Fatalf("query = %q", db.query)
	}
	if len(db.args) != 1 || db.args[0] != before.UTC() {
		t.Fatalf("args = %#v, want %s", db.args, before.UTC())
	}
}

func TestCleanupExpiredRejectsInvalidInputs(t *testing.T) {
	now := time.Now().UTC()
	for _, test := range []struct {
		name   string
		ctx    context.Context
		db     postgresExecer
		before time.Time
	}{
		{name: "context", db: &recordingExecer{}, before: now},
		{name: "database", ctx: context.Background(), before: now},
		{name: "boundary", ctx: context.Background(), db: &recordingExecer{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CleanupExpired(test.ctx, test.db, test.before); !errors.Is(err, ErrInvalidNonceClaim) {
				t.Fatalf("CleanupExpired() error = %v, want %v", err, ErrInvalidNonceClaim)
			}
		})
	}
}

func TestPostgresNonceUseTranslatesOnlyUniqueViolationToReplay(t *testing.T) {
	expiresAt := time.Date(2026, time.July, 20, 12, 30, 0, 0, time.FixedZone("test", 2*60*60))
	db := &recordingExecer{err: &pgconn.PgError{Code: "23505"}}
	store, err := NewPostgresNonceStore(db)
	if err != nil {
		t.Fatal(err)
	}

	used, err := store.Use(context.Background(), "key-1", "card-vault", "nonce-1", expiresAt)
	if err != nil || used {
		t.Fatalf("duplicate use = %v, %v, want false, nil", used, err)
	}
	if strings.Contains(db.query, "ON CONFLICT") {
		t.Fatalf("nonce insert retained read-requiring conflict clause: %s", db.query)
	}
	if len(db.args) != 4 || db.args[0] != "key-1" || db.args[1] != "card-vault" || db.args[2] != "nonce-1" || db.args[3] != expiresAt.UTC() {
		t.Fatalf("nonce insert args = %#v", db.args)
	}
}

func TestPostgresNonceUsePropagatesNonUniqueDatabaseErrors(t *testing.T) {
	databaseError := &pgconn.PgError{Code: "42501", Message: "permission denied"}
	store, err := NewPostgresNonceStore(&recordingExecer{err: databaseError})
	if err != nil {
		t.Fatal(err)
	}

	used, err := store.Use(context.Background(), "key-1", "card-vault", "nonce-1", time.Now().Add(time.Minute))
	if used || err != databaseError {
		t.Fatalf("database failure = %v, %v, want false, original error", used, err)
	}
}
