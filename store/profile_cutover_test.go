package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

func TestKeycloakProfileCutoverDestroysRecordedCredentialSchema(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	goose.SetTableName(migrationScopeTableNames[MigrationScopeIdentityAuth])
	goose.SetBaseFS(postgresMigrations)
	path := migrationScopePaths[MigrationScopeIdentityAuth]
	if err := goose.UpToContext(ctx, db.DB.DB, path, 103); err != nil {
		t.Fatalf("migrate legacy identity schema: %v", err)
	}
	now := time.Now().UTC()
	var userID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO users(
		tenant_id, password, fullname, mobile, public_key, otp, created_at, updated_at
	) VALUES($1, $2, $3, $4, $5, $6, $7, $7) RETURNING id`,
		"tenant", "password-hash", "Legacy User", "0990000000", "legacy-public-key", "otp-secret", now,
	).Scan(&userID); err != nil {
		t.Fatalf("insert legacy credential row: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO auth_accounts(
		tenant_id, user_id, provider, provider_user_id, created_at, updated_at
	) VALUES($1, $2, $3, $4, $5, $5)`, "tenant", userID, "local", "legacy", now); err != nil {
		t.Fatalf("insert legacy auth account: %v", err)
	}
	if err := goose.UpToContext(ctx, db.DB.DB, path, 104); err != nil {
		t.Fatalf("apply Keycloak profile cutover: %v", err)
	}

	for _, table := range []string{
		"auth_accounts", "api_keys", "login_metrics", "auth_rate_limits",
		"otp_challenges", "used_refresh_tokens", "password_recovery_credentials",
	} {
		var exists bool
		if err := db.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("legacy credential table %q survived cutover", table)
		}
	}
	rows, err := db.QueryContext(ctx, `SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'users'
		ORDER BY ordinal_position`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	want := []string{
		"tenant_id", "issuer", "subject", "id", "fullname", "username", "gender", "birthday",
		"email", "device_token", "language", "created_at", "updated_at",
	}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("post-cutover users columns = %v, want %v", columns, want)
	}
	var count int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("legacy users retained after destructive cutover: %d", count)
	}
	if err := goose.DownToContext(ctx, db.DB.DB, path, 103); err == nil ||
		!strings.Contains(err.Error(), "identity auth migration 104 is irreversible") {
		t.Fatalf("cutover down migration error = %v, want irreversible", err)
	}
}
