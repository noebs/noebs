package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/testdb"
)

var (
	validationPostgresOnce sync.Once
	validationPostgres     *testdb.PostgresContainer
	validationPostgresErr  error
	validationDatabaseSeq  atomic.Uint64
)

func TestMain(m *testing.M) {
	code := m.Run()
	if validationPostgres != nil {
		_ = validationPostgres.Terminate(context.Background())
	}
	os.Exit(code)
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := newValidationDB(t)
	return New(db, WithDataKey("test-data-key"))
}

func newTestStoreWithoutDataKey(t *testing.T) *Store {
	t.Helper()
	db := newValidationDB(t)
	return New(db)
}

func newValidationDB(t *testing.T) *DB {
	t.Helper()
	ctx := context.Background()
	validationPostgresOnce.Do(func() {
		validationPostgres, validationPostgresErr = testdb.StartPostgresContainer(ctx)
	})
	if validationPostgresErr != nil {
		if testdb.IsContainerRuntimeUnavailable(validationPostgresErr) {
			t.Skipf("container runtime unavailable: %v", validationPostgresErr)
		}
		t.Fatalf("start postgres: %v", validationPostgresErr)
	}
	databaseName := fmt.Sprintf("store_validation_%d", validationDatabaseSeq.Add(1))
	dbURL, err := validationPostgres.CreateDatabase(ctx, databaseName)
	if err != nil {
		t.Fatalf("create postgres database: %v", err)
	}
	db, err := OpenFromConfig(dbURL, DriverPostgres)
	if err != nil {
		t.Fatalf("open postgres database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_ = validationPostgres.DropDatabase(context.Background(), databaseName)
	})
	return db
}

func TestStore_EnsureTenant_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.EnsureTenant(context.Background(), "")
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_EnsureTenant_RejectsReservedTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.EnsureTenant(context.Background(), "default")
	if !errors.Is(err, ErrInvalidTenantID) {
		t.Fatalf("expected ErrInvalidTenantID, got %v", err)
	}
}

func TestValidateTenantID(t *testing.T) {
	got, err := ValidateTenantID(" tenant_1 ")
	if err != nil {
		t.Fatalf("ValidateTenantID() error = %v", err)
	}
	if got != "tenant_1" {
		t.Fatalf("tenantID = %q, want tenant_1", got)
	}
	for _, tenantID := range []string{"", "   "} {
		if _, err := ValidateTenantID(tenantID); !errors.Is(err, ErrMissingTenantID) {
			t.Fatalf("ValidateTenantID(%q) = %v, want ErrMissingTenantID", tenantID, err)
		}
	}
	for _, tenantID := range []string{"default", "Default", " default "} {
		if _, err := ValidateTenantID(tenantID); !errors.Is(err, ErrInvalidTenantID) {
			t.Fatalf("ValidateTenantID(%q) = %v, want ErrInvalidTenantID", tenantID, err)
		}
	}
}

func TestStore_CreateToken_MissingTenantID(t *testing.T) {
	s := &Store{}
	err := s.CreateToken(context.Background(), "", &ebs_fields.Token{UUID: "u1"})
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_CreateToken_RequiresExplicitFields(t *testing.T) {
	s := &Store{}
	err := s.CreateToken(context.Background(), "t1", &ebs_fields.Token{})
	if !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("expected ErrMissingUUID, got %v", err)
	}
	err = s.CreateToken(context.Background(), "t1", &ebs_fields.Token{UUID: "u1"})
	if !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("expected ErrInvalidUserID, got %v", err)
	}
	err = s.CreateToken(context.Background(), "t1", &ebs_fields.Token{UUID: "u1", UserID: 1, Amount: -1})
	if !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestStore_CreateTransaction_MissingUUID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateTransaction(context.Background(), "t1", ebs_fields.EBSResponse{})
	if !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("expected ErrMissingUUID, got %v", err)
	}
}

func TestStore_CreatePushDataRequiresExplicitFields(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	if err := s.CreatePushData(ctx, "t1", nil); !errors.Is(err, ErrMissingPushData) {
		t.Fatalf("CreatePushData(nil) error = %v, want %v", err, ErrMissingPushData)
	}
	if err := s.CreatePushData(ctx, "t1", &ebs_fields.PushDataRecord{UUID: " "}); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("CreatePushData(missing uuid) error = %v, want %v", err, ErrMissingUUID)
	}
	if err := s.CreatePushData(ctx, "t1", &ebs_fields.PushDataRecord{UUID: "push-uuid"}); !errors.Is(err, ErrMissingPushTarget) {
		t.Fatalf("CreatePushData(missing target) error = %v, want %v", err, ErrMissingPushTarget)
	}
}

type rowsAffectedResult int64

func (r rowsAffectedResult) LastInsertId() (int64, error) {
	return 0, nil
}

func (r rowsAffectedResult) RowsAffected() (int64, error) {
	return int64(r), nil
}

type execFunc func(context.Context, string, ...any) (sql.Result, error)

func (fn execFunc) ExecContext(ctx context.Context, stmt string, args ...any) (sql.Result, error) {
	return fn(ctx, stmt, args...)
}

func TestExecContextRequireRowsAffected(t *testing.T) {
	err := execContextRequireRowsAffected(context.Background(), execFunc(func(context.Context, string, ...any) (sql.Result, error) {
		return rowsAffectedResult(1), nil
	}), "UPDATE tokens SET is_paid = TRUE")
	if err != nil {
		t.Fatalf("execContextRequireRowsAffected() error = %v", err)
	}

	err = execContextRequireRowsAffected(context.Background(), execFunc(func(context.Context, string, ...any) (sql.Result, error) {
		return rowsAffectedResult(0), nil
	}), "UPDATE tokens SET is_paid = TRUE")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("zero rows error = %v, want %v", err, sql.ErrNoRows)
	}
}

func TestErrNotFoundOnlyMatchesNoRows(t *testing.T) {
	if !ErrNotFound(sql.ErrNoRows) {
		t.Fatalf("ErrNotFound(sql.ErrNoRows) = false, want true")
	}
	if ErrNotFound(nil) {
		t.Fatalf("ErrNotFound(nil) = true, want false")
	}
	if ErrNotFound(errors.New("database file not found")) {
		t.Fatalf("ErrNotFound(non-store error) = true, want false")
	}
}

func TestStore_GenericBeneficiaryOperationsAreTerminal(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	operations := []func() error{
		func() error { _, err := s.ListBeneficiaries(ctx, "tenant", 42); return err },
		func() error {
			return s.UpsertBeneficiary(ctx, "tenant", 42, ebs_fields.Beneficiary{Data: "6011000073184629", BillType: "mobile"})
		},
		func() error { return s.DeleteBeneficiary(ctx, "tenant", 42, "6011000073184629") },
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, ErrBeneficiaryRetired) {
			t.Fatalf("operation %d error = %v, want %v", index, err, ErrBeneficiaryRetired)
		}
	}
}

func TestStore_TransactionParticipantQueriesRequireExplicitInputs(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	if _, err := s.GetTransactionsByParticipantUserID(ctx, "t1", 0); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("missing user id error = %v, want %v", err, ErrInvalidUserID)
	}
	if _, err := s.GetTransactionByUUIDForParticipantUserID(ctx, "t1", 0, "transaction-uuid"); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("missing user id error = %v, want %v", err, ErrInvalidUserID)
	}
	if _, err := s.GetTransactionByUUIDForParticipantUserID(ctx, "t1", 1, " "); !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("missing uuid error = %v, want %v", err, ErrMissingUUID)
	}
}

func TestStore_CacheBillerRequiresExplicitFields(t *testing.T) {
	s := &Store{}
	if err := s.UpsertCacheBiller(context.Background(), "t1", " ", "0010010001"); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("UpsertCacheBiller(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := s.UpsertCacheBiller(context.Background(), "t1", "0912141660", " "); !errors.Is(err, ErrMissingBillerID) {
		t.Fatalf("UpsertCacheBiller(missing biller id) error = %v, want %v", err, ErrMissingBillerID)
	}
	if _, err := s.GetCacheBiller(context.Background(), "t1", " "); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("GetCacheBiller(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
}

func TestStore_CreateToken_RequiresDataKeyForDestinationPAN(t *testing.T) {
	s := &Store{}
	err := s.CreateToken(context.Background(), "t1", &ebs_fields.Token{UUID: "u1", UserID: 1, Amount: 1, ToCard: "9222081700000000"})
	if !errors.Is(err, ErrMissingDataKey) {
		t.Fatalf("expected ErrMissingDataKey, got %v", err)
	}
}

func TestStore_GetNotifications_MissingMobile(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetNotifications(context.Background(), "t1", "")
	if !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("expected ErrMissingMobile, got %v", err)
	}
}

func TestStore_MarkNotificationsRead_MissingMobile(t *testing.T) {
	s := newTestStore(t)
	err := s.MarkNotificationsRead(context.Background(), "t1", "")
	if !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("expected ErrMissingMobile, got %v", err)
	}
}

func newIdentityAuthTestStore(t *testing.T, ctx context.Context) *Store {
	t.Helper()
	db := newValidationDB(t)
	if err := MigrateScope(ctx, db, "tenant", MigrationScopeIdentityAuth); err != nil {
		t.Fatalf("migrate identity-auth scope: %v", err)
	}
	return New(db)
}

func TestStore_CoreTenantValidationFailsBeforeDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{}
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"UpsertCacheBiller", func(tenantID string) error {
			return s.UpsertCacheBiller(ctx, tenantID, "0990000000", "biller")
		}},
		{"GetCacheBiller", func(tenantID string) error {
			_, err := s.GetCacheBiller(ctx, tenantID, "0990000000")
			return err
		}},
		{"CreateToken", func(tenantID string) error {
			return s.CreateToken(ctx, tenantID, &ebs_fields.Token{UUID: "token-uuid"})
		}},
		{"GetTokenByUUID", func(tenantID string) error {
			_, err := s.GetTokenByUUID(ctx, tenantID, "token-uuid")
			return err
		}},
		{"MarkTokenPaid", func(tenantID string) error {
			return s.MarkTokenPaid(ctx, tenantID, "token-uuid", "rail-uuid", 1)
		}},
		{"CreateTransaction", func(tenantID string) error {
			return s.CreateTransaction(ctx, tenantID, ebs_fields.EBSResponse{UUID: "transaction-uuid"})
		}},
		{"GetTransactionsByParticipantUserID", func(tenantID string) error {
			_, err := s.GetTransactionsByParticipantUserID(ctx, tenantID, 1)
			return err
		}},
		{"GetTransactionByUUID", func(tenantID string) error {
			_, err := s.GetTransactionByUUID(ctx, tenantID, "transaction-uuid")
			return err
		}},
		{"GetTransactionByUUIDForParticipantUserID", func(tenantID string) error {
			_, err := s.GetTransactionByUUIDForParticipantUserID(ctx, tenantID, 1, "transaction-uuid")
			return err
		}},
		{"CreatePushData", func(tenantID string) error {
			return s.CreatePushData(ctx, tenantID, &ebs_fields.PushDataRecord{UUID: "push-uuid"})
		}},
		{"GetNotifications", func(tenantID string) error {
			_, err := s.GetNotifications(ctx, tenantID, "0990000000")
			return err
		}},
		{"MarkNotificationsRead", func(tenantID string) error {
			return s.MarkNotificationsRead(ctx, tenantID, "0990000000")
		}},
		{"GetMeterName", func(tenantID string) error {
			_, err := s.GetMeterName(ctx, tenantID, "nec")
			return err
		}},
		{"GetAllTokensByUserID", func(tenantID string) error {
			_, err := s.GetAllTokensByUserID(ctx, tenantID, 1)
			return err
		}},
		{"GetAllTokensByUserIDAndCartID", func(tenantID string) error {
			_, err := s.GetAllTokensByUserIDAndCartID(ctx, tenantID, 1, "cart")
			return err
		}},
		{"UpdateTokenCard", func(tenantID string) error {
			return s.UpdateTokenCard(ctx, tenantID, "token-uuid", "9222081700000000")
		}},
		{"UpdatePaymentRequest", func(tenantID string) error {
			return s.UpdatePaymentRequest(ctx, tenantID, "push-uuid", ebs_fields.QrData{})
		}},
	}
	tenantCases := []struct {
		tenantID string
		wantErr  error
	}{
		{"", ErrMissingTenantID},
		{"default", ErrInvalidTenantID},
	}
	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.tenantID, func(t *testing.T) {
				err := tc.run(tenantCase.tenantID)
				if !errors.Is(err, tenantCase.wantErr) {
					t.Fatalf("expected %v, got %v", tenantCase.wantErr, err)
				}
			})
		}
	}
}

func TestStoreTargetedUpdatesReportMissingRows(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	tenantID := "tenant-targeted-updates"
	for _, scope := range []string{MigrationScopeIdentityAuth, MigrationScopeCardVault, MigrationScopeNotificationChat} {
		if err := MigrateScope(ctx, db, tenantID, scope); err != nil {
			t.Fatalf("migrate %s: %v", scope, err)
		}
	}
	s := New(db, WithDataKey("test-data-key"))
	if err := s.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	fullname := "Missing User"
	tests := []struct {
		name string
		run  func() error
	}{
		{"UpdateProfileProjection", func() error {
			return s.UpdateProfileProjection(ctx, tenantID, 999, ProfileProjectionUpdate{Fullname: &fullname})
		}},
		{"SetProfileDeviceToken", func() error {
			return s.SetProfileDeviceToken(ctx, tenantID, 999, "device-token")
		}},
		{"MarkTokenPaid", func() error {
			return s.MarkTokenPaid(ctx, tenantID, "missing-token", "rail-uuid", 1)
		}},
		{"UpdateTokenCard", func() error {
			return s.UpdateTokenCard(ctx, tenantID, "missing-token", "")
		}},
		{"UpdatePaymentRequest", func() error {
			return s.UpdatePaymentRequest(ctx, tenantID, "missing-push", ebs_fields.QrData{UUID: "payment-1"})
		}},
		{"updateTokenCard", func() error {
			return s.updateTokenCard(ctx, tenantID, "missing-token", "hash:missing-token-card", "enc:missing-token-card")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("%s error = %v, want %v", tt.name, err, sql.ErrNoRows)
			}
		})
	}
}

func TestLegacyCardStoreOperationsAreTerminal(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	operations := []func() error{
		func() error { _, err := s.ListCardsByUserID(ctx, "tenant", 1); return err },
		func() error { _, err := s.ListCardsByMobile(ctx, "tenant", "0912141660"); return err },
		func() error { return s.AddCards(ctx, "tenant", 1, nil) },
		func() error { return s.UpdateCard(ctx, "tenant", 1, ebs_fields.Card{}) },
		func() error { return s.DeleteCard(ctx, "tenant", 1, "9222081700000000") },
		func() error { return s.SetMainCard(ctx, "tenant", 1, "9222081700000000") },
		func() error { _, err := s.GetPanByMobile(ctx, "tenant", "0912141660"); return err },
		func() error { _, err := s.CardExists(ctx, "tenant", "9222081700000000"); return err },
		func() error { _, err := s.GetDeviceIDsByPan(ctx, "tenant", "9222081700000000"); return err },
	}
	for index, operation := range operations {
		if err := operation(); !errors.Is(err, ErrLegacyCardOperation) {
			t.Fatalf("legacy operation %d error = %v, want %v", index, err, ErrLegacyCardOperation)
		}
	}
}
