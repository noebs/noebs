package store

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

const (
	testProfileIssuer  = "https://identity.example/realms/noebs"
	testProfileSubject = "72ca10cc-a0db-4b06-a907-dc759c403bed"
)

func TestProfileProjectionUsesPrincipalCompositeAuthority(t *testing.T) {
	ctx := context.Background()
	store := newIdentityAuthTestStore(t, ctx)
	if err := store.EnsureTenant(ctx, "tenant"); err != nil {
		t.Fatal(err)
	}
	created, err := store.CreateProfileProjection(ctx, CreateProfileProjectionParams{
		PrincipalIdentity: PrincipalIdentity{TenantID: "tenant", Issuer: testProfileIssuer, Subject: testProfileSubject},
		Fullname:          "Profile Owner",
		Username:          "profile-owner",
		Email:             "owner@example.test",
		Mobile:            "0990000000",
		Language:          "en",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.UserID <= 0 {
		t.Fatalf("user id = %d, want positive domain projection", created.UserID)
	}
	resolved, err := store.ResolveProfileProjection(ctx, created.PrincipalIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(resolved, created) {
		t.Fatalf("resolved = %+v, want %+v", resolved, created)
	}
	byID, err := store.FindProfileProjectionByUserID(ctx, "tenant", created.UserID)
	if err != nil {
		t.Fatal(err)
	}
	if byID.PrincipalIdentity != created.PrincipalIdentity {
		t.Fatalf("numeric projection authority = %+v, want %+v", byID.PrincipalIdentity, created.PrincipalIdentity)
	}

	if _, err := store.CreateProfileProjection(ctx, CreateProfileProjectionParams{
		PrincipalIdentity: created.PrincipalIdentity,
		Fullname:          "Replay",
		Mobile:            "0990000001",
	}); !errors.Is(err, ErrProfileAlreadyExists) {
		t.Fatalf("duplicate principal error = %v, want ErrProfileAlreadyExists", err)
	}
}

func TestResolveProfileProjectionNeverCreates(t *testing.T) {
	ctx := context.Background()
	store := newIdentityAuthTestStore(t, ctx)
	if err := store.EnsureTenant(ctx, "tenant"); err != nil {
		t.Fatal(err)
	}
	identity := PrincipalIdentity{TenantID: "tenant", Issuer: testProfileIssuer, Subject: testProfileSubject}
	if _, err := store.ResolveProfileProjection(ctx, identity); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown principal error = %v, want sql.ErrNoRows", err)
	}
	var count int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("resolve created %d profile rows", count)
	}
}

func TestProfileProjectionRejectsInvalidAuthorityBeforeDatabaseAccess(t *testing.T) {
	ctx := context.Background()
	store := &Store{}
	valid := CreateProfileProjectionParams{
		PrincipalIdentity: PrincipalIdentity{TenantID: "tenant", Issuer: testProfileIssuer, Subject: testProfileSubject},
		Fullname:          "Profile Owner",
		Mobile:            "0990000000",
	}
	tests := []struct {
		name    string
		mutate  func(*CreateProfileProjectionParams)
		wantErr error
	}{
		{"tenant missing", func(p *CreateProfileProjectionParams) { p.TenantID = "" }, ErrMissingTenantID},
		{"tenant not exact", func(p *CreateProfileProjectionParams) { p.TenantID = " tenant " }, ErrInvalidTenantID},
		{"issuer missing", func(p *CreateProfileProjectionParams) { p.Issuer = "" }, ErrMissingIssuer},
		{"issuer not HTTPS", func(p *CreateProfileProjectionParams) { p.Issuer = "http://identity.example/realms/noebs" }, ErrInvalidIssuer},
		{"subject missing", func(p *CreateProfileProjectionParams) { p.Subject = "" }, ErrMissingSubject},
		{"subject not exact", func(p *CreateProfileProjectionParams) { p.Subject = " subject " }, ErrInvalidSubject},
		{"fullname missing", func(p *CreateProfileProjectionParams) { p.Fullname = "" }, ErrMissingProfileName},
		{"mobile invalid", func(p *CreateProfileProjectionParams) { p.Mobile = "990000000" }, ErrInvalidMobile},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := valid
			test.mutate(&params)
			if _, err := store.CreateProfileProjection(ctx, params); !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestIdentitySchemaContainsNoLocalCredentialAuthority(t *testing.T) {
	ctx := context.Background()
	store := newIdentityAuthTestStore(t, ctx)
	rows, err := store.DB.QueryContext(ctx, `SELECT column_name
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
		"email", "mobile", "device_token", "language", "created_at", "updated_at",
	}
	if !reflect.DeepEqual(columns, want) {
		t.Fatalf("users columns = %v, want %v", columns, want)
	}
	for _, table := range []string{"auth_accounts", "api_keys", "login_metrics", "auth_rate_limits", "otp_challenges", "used_refresh_tokens", "password_recovery_credentials"} {
		var exists bool
		if err := store.DB.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			t.Fatalf("credential table %q still exists", table)
		}
	}
}
