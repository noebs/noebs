package workloadauth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

type cleanupExecer struct {
	query string
	args  []any
	rows  int64
	err   error
}

func (e *cleanupExecer) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	e.query = query
	e.args = args
	return cleanupResult(e.rows), e.err
}

type cleanupResult int64

func (cleanupResult) LastInsertId() (int64, error)   { return 0, errors.New("unsupported") }
func (r cleanupResult) RowsAffected() (int64, error) { return int64(r), nil }

func TestCleanupExpiredUsesExactRetentionBoundary(t *testing.T) {
	before := time.Date(2026, time.July, 19, 12, 30, 0, 0, time.FixedZone("test", 2*60*60))
	db := &cleanupExecer{rows: 7}

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
		{name: "context", db: &cleanupExecer{}, before: now},
		{name: "database", ctx: context.Background(), before: now},
		{name: "boundary", ctx: context.Background(), db: &cleanupExecer{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CleanupExpired(test.ctx, test.db, test.before); !errors.Is(err, ErrInvalidNonceClaim) {
				t.Fatalf("CleanupExpired() error = %v, want %v", err, ErrInvalidNonceClaim)
			}
		})
	}
}
