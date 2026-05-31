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
	"time"

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

func TestStore_CreateUser_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateUser(context.Background(), "", &ebs_fields.User{Mobile: "0990000000"})
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_UserWritesDoNotPersistMainExpDate(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	user := &ebs_fields.User{
		Mobile:   "0990000000",
		Username: "0990000000",
		Email:    "user@example.test",
		ExpDate:  "2601",
	}
	if err := s.CreateUser(ctx, "tenant", user); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	assertUserMainExpDateEmpty(t, s, user.ID)

	user.Fullname = "Updated User"
	user.ExpDate = "3001"
	if err := s.UpdateUser(ctx, "tenant", user); err != nil {
		t.Fatalf("UpdateUser(): %v", err)
	}
	assertUserMainExpDateEmpty(t, s, user.ID)
}

func assertUserMainExpDateEmpty(t *testing.T, s *Store, userID int64) {
	t.Helper()
	var mainExpDate sql.NullString
	stmt := s.DB.Rebind("SELECT main_expdate FROM users WHERE tenant_id = ? AND id = ?")
	if err := s.DB.QueryRowContext(context.Background(), stmt, "tenant", userID).Scan(&mainExpDate); err != nil {
		t.Fatalf("read main_expdate: %v", err)
	}
	if mainExpDate.Valid && mainExpDate.String != "" {
		t.Fatalf("main_expdate = %q, want empty", mainExpDate.String)
	}
}

func TestStore_IdentityTenantValidationFailsBeforeDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{}
	user := &ebs_fields.User{Mobile: "0990000000"}
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"CreateAPIKey", func(tenantID string) error {
			return s.CreateAPIKey(ctx, tenantID, "user@example.test", "api-key")
		}},
		{"ValidateAPIKey", func(tenantID string) error {
			_, err := s.ValidateAPIKey(ctx, tenantID, "user@example.test", "api-key")
			return err
		}},
		{"ValidateAPIKeyValue", func(tenantID string) error {
			_, err := s.ValidateAPIKeyValue(ctx, tenantID, "api-key")
			return err
		}},
		{"CreateUser", func(tenantID string) error {
			return s.CreateUser(ctx, tenantID, &ebs_fields.User{Mobile: "0990000000"})
		}},
		{"GetUserByMobile", func(tenantID string) error {
			_, err := s.GetUserByMobile(ctx, tenantID, "0990000000")
			return err
		}},
		{"GetUserByEmailOrMobile", func(tenantID string) error {
			_, err := s.GetUserByEmailOrMobile(ctx, tenantID, "0990000000")
			return err
		}},
		{"GetUserByCard", func(tenantID string) error {
			_, err := s.GetUserByCard(ctx, tenantID, "9222081700000000")
			return err
		}},
		{"FindUserByUsername", func(tenantID string) error {
			_, err := s.FindUserByUsername(ctx, tenantID, "user")
			return err
		}},
		{"GetUserByUsernameEmailOrMobile", func(tenantID string) error {
			_, err := s.GetUserByUsernameEmailOrMobile(ctx, tenantID, "user")
			return err
		}},
		{"UpdateUser", func(tenantID string) error {
			return s.UpdateUser(ctx, tenantID, user)
		}},
		{"UpdateUserColumns", func(tenantID string) error {
			return s.UpdateUserColumns(ctx, tenantID, 1, map[string]any{"fullname": "User"})
		}},
		{"UpsertDeviceToken", func(tenantID string) error {
			return s.UpsertDeviceToken(ctx, tenantID, "0990000000", "device-token")
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

func TestStore_UpdateUserColumnsRejectsUnsafeColumns(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	for _, column := range []string{"main_expdate", "main_card_enc", "fullname = 'x'"} {
		t.Run(column, func(t *testing.T) {
			err := s.UpdateUserColumns(ctx, "tenant", 1, map[string]any{column: "value"})
			if !errors.Is(err, ErrInvalidUserColumn) {
				t.Fatalf("UpdateUserColumns() error = %v, want %v", err, ErrInvalidUserColumn)
			}
		})
	}
}

func TestStore_CoreTenantValidationFailsBeforeDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{}
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"ListCardsByUserID", func(tenantID string) error {
			_, err := s.ListCardsByUserID(ctx, tenantID, 1)
			return err
		}},
		{"ListCardsByMobile", func(tenantID string) error {
			_, err := s.ListCardsByMobile(ctx, tenantID, "0990000000")
			return err
		}},
		{"AddCards", func(tenantID string) error {
			return s.AddCards(ctx, tenantID, 1, []ebs_fields.Card{{Mobile: "0990000000"}})
		}},
		{"UpdateCard", func(tenantID string) error {
			return s.UpdateCard(ctx, tenantID, 1, ebs_fields.Card{})
		}},
		{"DeleteCard", func(tenantID string) error {
			return s.DeleteCard(ctx, tenantID, 1, "9222081700000000")
		}},
		{"SetMainCard", func(tenantID string) error {
			return s.SetMainCard(ctx, tenantID, 1, "9222081700000000")
		}},
		{"ListBeneficiaries", func(tenantID string) error {
			_, err := s.ListBeneficiaries(ctx, tenantID, 1)
			return err
		}},
		{"UpsertBeneficiary", func(tenantID string) error {
			return s.UpsertBeneficiary(ctx, tenantID, 1, ebs_fields.Beneficiary{Data: "meter", BillType: "electricity"})
		}},
		{"DeleteBeneficiary", func(tenantID string) error {
			return s.DeleteBeneficiary(ctx, tenantID, 1, "meter")
		}},
		{"UpsertCacheCard", func(tenantID string) error {
			return s.UpsertCacheCard(ctx, tenantID, ebs_fields.CacheCards{Pan: "9222081700000000"})
		}},
		{"GetCacheCard", func(tenantID string) error {
			_, err := s.GetCacheCard(ctx, tenantID, "9222081700000000")
			return err
		}},
		{"CardExists", func(tenantID string) error {
			_, err := s.CardExists(ctx, tenantID, "9222081700000000")
			return err
		}},
		{"UpsertCacheBiller", func(tenantID string) error {
			return s.UpsertCacheBiller(ctx, tenantID, "0990000000", "biller")
		}},
		{"GetCacheBiller", func(tenantID string) error {
			_, err := s.GetCacheBiller(ctx, tenantID, "0990000000")
			return err
		}},
		{"RecordLoginAttempt", func(tenantID string) error {
			_, err := s.RecordLoginAttempt(ctx, tenantID, "0990000000", true)
			return err
		}},
		{"IncrementSuspicious", func(tenantID string) error {
			return s.IncrementSuspicious(ctx, tenantID, "0990000000")
		}},
		{"CreateToken", func(tenantID string) error {
			return s.CreateToken(ctx, tenantID, &ebs_fields.Token{UUID: "token-uuid"})
		}},
		{"GetTokenByUUID", func(tenantID string) error {
			_, err := s.GetTokenByUUID(ctx, tenantID, "token-uuid")
			return err
		}},
		{"MarkTokenPaid", func(tenantID string) error {
			return s.MarkTokenPaid(ctx, tenantID, "token-uuid")
		}},
		{"CreateTransaction", func(tenantID string) error {
			return s.CreateTransaction(ctx, tenantID, ebs_fields.EBSResponse{UUID: "transaction-uuid"})
		}},
		{"GetTransactionsByMaskedPan", func(tenantID string) error {
			_, err := s.GetTransactionsByMaskedPan(ctx, tenantID, "922208******0000")
			return err
		}},
		{"GetTransactionByUUID", func(tenantID string) error {
			_, err := s.GetTransactionByUUID(ctx, tenantID, "transaction-uuid")
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
		{"UpdateKYC", func(tenantID string) error {
			return s.UpdateKYC(ctx, tenantID, &ebs_fields.KYC{}, nil)
		}},
		{"GetUserWithKYC", func(tenantID string) error {
			_, _, _, err := s.GetUserWithKYC(ctx, tenantID, "0990000000")
			return err
		}},
		{"LinkAuthAccount", func(tenantID string) error {
			return s.LinkAuthAccount(ctx, tenantID, &ebs_fields.AuthAccount{})
		}},
		{"CreateUserWithAuthAccount", func(tenantID string) error {
			return s.CreateUserWithAuthAccount(ctx, tenantID, &ebs_fields.User{}, &ebs_fields.AuthAccount{})
		}},
		{"FindAuthAccount", func(tenantID string) error {
			_, err := s.FindAuthAccount(ctx, tenantID, "provider", "provider-user")
			return err
		}},
		{"FindUserByEmail", func(tenantID string) error {
			_, err := s.FindUserByEmail(ctx, tenantID, "user@example.test")
			return err
		}},
		{"FindUserByID", func(tenantID string) error {
			_, err := s.FindUserByID(ctx, tenantID, 1)
			return err
		}},
		{"GetDeviceIDsByPan", func(tenantID string) error {
			_, err := s.GetDeviceIDsByPan(ctx, tenantID, "9222081700000000")
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

func TestStore_CreateUser_MissingUser(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateUser(context.Background(), "t1", nil)
	if !errors.Is(err, ErrMissingUser) {
		t.Fatalf("expected ErrMissingUser, got %v", err)
	}
}

func TestStore_CreateToken_MissingTenantID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateToken(context.Background(), "", &ebs_fields.Token{UUID: "u1"})
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestStore_CreateToken_MissingUUID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateToken(context.Background(), "t1", &ebs_fields.Token{})
	if !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("expected ErrMissingUUID, got %v", err)
	}
}

func TestStore_CreateTransaction_MissingUUID(t *testing.T) {
	s := newTestStore(t)
	err := s.CreateTransaction(context.Background(), "t1", ebs_fields.EBSResponse{})
	if !errors.Is(err, ErrMissingUUID) {
		t.Fatalf("expected ErrMissingUUID, got %v", err)
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

func TestStoreTargetedUpdatesReportMissingRows(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	tenantID := "tenant-targeted-updates"
	for _, scope := range []string{MigrationScopeIdentityAuth, MigrationScopeCardVault, MigrationScopeNotificationChat} {
		if err := MigrateScope(ctx, db, tenantID, scope); err != nil {
			t.Fatalf("migrate %s: %v", scope, err)
		}
	}
	s := New(db)
	if err := s.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{"UpdateUserColumns", func() error {
			return s.UpdateUserColumns(ctx, tenantID, 999, map[string]any{"fullname": "Missing User"})
		}},
		{"UpdateUser", func() error {
			return s.UpdateUser(ctx, tenantID, &ebs_fields.User{Model: ebs_fields.Model{ID: 999}, Mobile: "0990000000"})
		}},
		{"UpdateCard", func() error {
			return s.UpdateCard(ctx, tenantID, 999, ebs_fields.Card{CardIdx: "9222081700000000"})
		}},
		{"DeleteCard", func() error {
			return s.DeleteCard(ctx, tenantID, 999, "9222081700000000")
		}},
		{"SetMainCard", func() error {
			return s.SetMainCard(ctx, tenantID, 999, "9222081700000000")
		}},
		{"MarkTokenPaid", func() error {
			return s.MarkTokenPaid(ctx, tenantID, "missing-token")
		}},
		{"UpdateTokenCard", func() error {
			return s.UpdateTokenCard(ctx, tenantID, "missing-token", "")
		}},
		{"UpsertDeviceToken", func() error {
			return s.UpsertDeviceToken(ctx, tenantID, "0990000000", "device-token")
		}},
		{"UpdatePaymentRequest", func() error {
			return s.UpdatePaymentRequest(ctx, tenantID, "missing-push", ebs_fields.QrData{UUID: "payment-1"})
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

func TestStore_UpdateUserRequiresExplicitTarget(t *testing.T) {
	s := &Store{}
	if err := s.UpdateUser(context.Background(), "tenant", nil); !errors.Is(err, ErrMissingUser) {
		t.Fatalf("UpdateUser(nil) error = %v, want %v", err, ErrMissingUser)
	}
	if err := s.UpdateUser(context.Background(), "tenant", &ebs_fields.User{}); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("UpdateUser(invalid id) error = %v, want %v", err, ErrInvalidUserID)
	}
}

func TestStore_CardTargetedWritesRequirePAN(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	if err := s.UpdateCard(ctx, "tenant", 1, ebs_fields.Card{CardIdx: " "}); !errors.Is(err, ErrMissingPAN) {
		t.Fatalf("UpdateCard(missing card index) error = %v, want %v", err, ErrMissingPAN)
	}
	if err := s.DeleteCard(ctx, "tenant", 1, " "); !errors.Is(err, ErrMissingPAN) {
		t.Fatalf("DeleteCard(missing card index) error = %v, want %v", err, ErrMissingPAN)
	}
}

func TestStore_SetMainCardMissingTargetRollsBackReset(t *testing.T) {
	ctx := context.Background()
	db := newValidationDB(t)
	tenantID := "tenant-main-card-rollback"
	for _, scope := range []string{MigrationScopeIdentityAuth, MigrationScopeCardVault} {
		if err := MigrateScope(ctx, db, tenantID, scope); err != nil {
			t.Fatalf("migrate %s: %v", scope, err)
		}
	}
	s := New(db, WithDataKey("test-data-key"))
	if err := s.EnsureTenant(ctx, tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}
	user := &ebs_fields.User{Mobile: "0990000000", Username: "0990000000"}
	if err := s.CreateUser(ctx, tenantID, user); err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}
	valid := true
	if err := s.AddCards(ctx, tenantID, user.ID, []ebs_fields.Card{{
		Mobile:  "0990000000",
		Pan:     "9222081700000000",
		Expiry:  "3001",
		IsMain:  true,
		IsValid: &valid,
	}}); err != nil {
		t.Fatalf("AddCards(): %v", err)
	}

	if err := s.SetMainCard(ctx, tenantID, user.ID, "9222081700009999"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("SetMainCard(missing target) error = %v, want %v", err, sql.ErrNoRows)
	}

	cards, err := s.ListCardsByUserID(ctx, tenantID, user.ID)
	if err != nil {
		t.Fatalf("ListCardsByUserID(): %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("cards length = %d, want 1", len(cards))
	}
	if !cards[0].IsMain {
		t.Fatal("existing main card was reset after missing-target SetMainCard")
	}
}

func TestStore_UpsertDeviceTokenRequiresExplicitFields(t *testing.T) {
	s := &Store{}
	if err := s.UpsertDeviceToken(context.Background(), "tenant", " ", "device-token"); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("UpsertDeviceToken(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := s.UpsertDeviceToken(context.Background(), "tenant", "0990000000", " "); !errors.Is(err, ErrMissingToken) {
		t.Fatalf("UpsertDeviceToken(missing token) error = %v, want %v", err, ErrMissingToken)
	}
}

func TestStore_AddCards_RequiresDataKey(t *testing.T) {
	s := newTestStoreWithoutDataKey(t)
	err := s.AddCards(context.Background(), "t1", 1, []ebs_fields.Card{{Pan: "9222081700000000", IPIN: "1234", Mobile: "0912141660"}})
	if !errors.Is(err, ErrMissingDataKey) {
		t.Fatalf("expected ErrMissingDataKey, got %v", err)
	}
}

func TestStore_AddCards_RequiresMobile(t *testing.T) {
	s := newTestStore(t)
	err := s.AddCards(context.Background(), "t1", 1, []ebs_fields.Card{{Pan: "9222081700000000"}})
	if !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("expected ErrMissingMobile, got %v", err)
	}
}

func TestStore_UpsertBeneficiary_RequiresExplicitFields(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertBeneficiary(context.Background(), "t1", 0, ebs_fields.Beneficiary{Data: "0912141660", BillType: "0010010001"}); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("expected ErrInvalidUserID, got %v", err)
	}
	if err := s.UpsertBeneficiary(context.Background(), "t1", 1, ebs_fields.Beneficiary{BillType: "0010010001"}); !errors.Is(err, ErrMissingData) {
		t.Fatalf("expected ErrMissingData, got %v", err)
	}
	if err := s.UpsertBeneficiary(context.Background(), "t1", 1, ebs_fields.Beneficiary{Data: "0912141660"}); !errors.Is(err, ErrMissingBillType) {
		t.Fatalf("expected ErrMissingBillType, got %v", err)
	}
}

func TestStore_DeleteBeneficiary_RequiresExplicitFields(t *testing.T) {
	s := newTestStore(t)
	if err := s.DeleteBeneficiary(context.Background(), "t1", 0, "0912141660"); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("expected ErrInvalidUserID, got %v", err)
	}
	if err := s.DeleteBeneficiary(context.Background(), "t1", 1, " "); !errors.Is(err, ErrMissingData) {
		t.Fatalf("expected ErrMissingData, got %v", err)
	}
}

func TestStore_GetTransactionsByMaskedPan_RequiresPAN(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetTransactionsByMaskedPan(context.Background(), "t1", " "); !errors.Is(err, ErrMissingPAN) {
		t.Fatalf("expected ErrMissingPAN, got %v", err)
	}
}

func TestStore_CardExists_RequiresPAN(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.CardExists(context.Background(), "t1", " "); !errors.Is(err, ErrMissingPAN) {
		t.Fatalf("expected ErrMissingPAN, got %v", err)
	}
}

func TestStore_SetMainCard_RequiresPAN(t *testing.T) {
	s := &Store{}
	if err := s.SetMainCard(context.Background(), "t1", 42, " "); !errors.Is(err, ErrMissingPAN) {
		t.Fatalf("expected ErrMissingPAN, got %v", err)
	}
}

func TestStore_UpsertCacheCard_RequiresDataKey(t *testing.T) {
	s := newTestStoreWithoutDataKey(t)
	err := s.UpsertCacheCard(context.Background(), "t1", ebs_fields.CacheCards{Pan: "9222081700000000"})
	if !errors.Is(err, ErrMissingDataKey) {
		t.Fatalf("expected ErrMissingDataKey, got %v", err)
	}
}

func TestStore_CreateToken_RequiresDataKeyForDestinationPAN(t *testing.T) {
	s := newTestStoreWithoutDataKey(t)
	err := s.CreateToken(context.Background(), "t1", &ebs_fields.Token{UUID: "u1", ToCard: "9222081700000000"})
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

func TestStore_LoginMetricsRequireMobileBeforeDB(t *testing.T) {
	s := &Store{}
	if _, err := s.RecordLoginAttempt(context.Background(), "tenant", " ", true); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("RecordLoginAttempt() error = %v, want %v", err, ErrMissingMobile)
	}
	if err := s.IncrementSuspicious(context.Background(), "tenant", " "); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("IncrementSuspicious() error = %v, want %v", err, ErrMissingMobile)
	}
}

func TestStore_RecordLoginAttemptCountsFirstAttemptAndResetsWindow(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)

	count, err := s.RecordLoginAttempt(ctx, "tenant", "0990000000", true)
	if err != nil {
		t.Fatalf("first RecordLoginAttempt(): %v", err)
	}
	if count != 1 {
		t.Fatalf("first login count = %d, want 1", count)
	}

	count, err = s.RecordLoginAttempt(ctx, "tenant", "0990000000", true)
	if err != nil {
		t.Fatalf("second RecordLoginAttempt(): %v", err)
	}
	if count != 2 {
		t.Fatalf("second login count = %d, want 2", count)
	}

	expiredWindow := time.Now().UTC().Add(-loginAttemptWindow - time.Second)
	stmt := s.DB.Rebind("UPDATE login_metrics SET window_started_at = ? WHERE tenant_id = ? AND mobile = ?")
	if _, err := s.DB.ExecContext(ctx, stmt, expiredWindow, "tenant", "0990000000"); err != nil {
		t.Fatalf("expire login window: %v", err)
	}

	count, err = s.RecordLoginAttempt(ctx, "tenant", "0990000000", true)
	if err != nil {
		t.Fatalf("expired-window RecordLoginAttempt(): %v", err)
	}
	if count != 1 {
		t.Fatalf("expired-window login count = %d, want 1", count)
	}
}

func TestStore_IncrementSuspiciousCreatesAndUpdatesMetric(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)

	if err := s.IncrementSuspicious(ctx, "tenant", "0990000000"); err != nil {
		t.Fatalf("first IncrementSuspicious(): %v", err)
	}
	if err := s.IncrementSuspicious(ctx, "tenant", "0990000000"); err != nil {
		t.Fatalf("second IncrementSuspicious(): %v", err)
	}

	var suspiciousCount int
	var loginCount int
	var updatedAt sql.NullTime
	stmt := s.DB.Rebind("SELECT suspicious_count, login_count, updated_at FROM login_metrics WHERE tenant_id = ? AND mobile = ?")
	if err := s.DB.QueryRowContext(ctx, stmt, "tenant", "0990000000").Scan(&suspiciousCount, &loginCount, &updatedAt); err != nil {
		t.Fatalf("read login metric: %v", err)
	}
	if suspiciousCount != 2 {
		t.Fatalf("suspicious count = %d, want 2", suspiciousCount)
	}
	if loginCount != 0 {
		t.Fatalf("login count = %d, want 0", loginCount)
	}
	if !updatedAt.Valid {
		t.Fatal("updated_at is NULL, want timestamp")
	}
}

func TestStore_AuthAccountValidationFailsBeforeDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{}
	tests := []struct {
		name string
		run  func() error
		want error
	}{
		{
			name: "link missing account",
			run: func() error {
				return s.LinkAuthAccount(ctx, "tenant", nil)
			},
			want: ErrMissingAccount,
		},
		{
			name: "link missing user",
			run: func() error {
				return s.LinkAuthAccount(ctx, "tenant", &ebs_fields.AuthAccount{Provider: "google", ProviderUserID: "sub"})
			},
			want: ErrInvalidUserID,
		},
		{
			name: "link missing provider",
			run: func() error {
				return s.LinkAuthAccount(ctx, "tenant", &ebs_fields.AuthAccount{UserID: 1, ProviderUserID: "sub"})
			},
			want: ErrMissingProvider,
		},
		{
			name: "find missing provider user",
			run: func() error {
				_, err := s.FindAuthAccount(ctx, "tenant", "google", "")
				return err
			},
			want: ErrMissingProviderUserID,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestStore_UpdateKYCValidationFailsBeforeDB(t *testing.T) {
	ctx := context.Background()
	s := &Store{}
	tests := []struct {
		name     string
		kyc      *ebs_fields.KYC
		passport *ebs_fields.Passport
		want     error
	}{
		{
			name: "missing kyc",
			want: ErrMissingKYC,
		},
		{
			name: "missing user mobile",
			kyc:  &ebs_fields.KYC{Mobile: "0990000000"},
			want: ErrMissingMobile,
		},
		{
			name: "missing kyc mobile",
			kyc:  &ebs_fields.KYC{UserMobile: "0990000000"},
			want: ErrMissingMobile,
		},
		{
			name: "mismatched kyc mobile",
			kyc:  &ebs_fields.KYC{UserMobile: "0990000000", Mobile: "0991111111"},
			want: ErrInvalidMobile,
		},
		{
			name:     "missing passport mobile",
			kyc:      &ebs_fields.KYC{UserMobile: "0990000000", Mobile: "0990000000"},
			passport: &ebs_fields.Passport{},
			want:     ErrMissingMobile,
		},
		{
			name:     "mismatched passport mobile",
			kyc:      &ebs_fields.KYC{UserMobile: "0990000000", Mobile: "0990000000"},
			passport: &ebs_fields.Passport{Mobile: "0991111111"},
			want:     ErrInvalidMobile,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.UpdateKYC(ctx, "tenant", tt.kyc, tt.passport)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestStore_UpdateKYCRequiresExistingUser(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	mobile := "0990000000"

	err := s.UpdateKYC(
		ctx,
		"tenant",
		&ebs_fields.KYC{UserMobile: mobile, Mobile: mobile, Selfie: "selfie"},
		&ebs_fields.Passport{Mobile: mobile, PassportNumber: "P123"},
	)
	if !ErrNotFound(err) {
		t.Fatalf("error = %v, want not found", err)
	}

	var kycRows int
	stmt := s.DB.Rebind("SELECT COUNT(*) FROM kyc WHERE tenant_id = ? AND mobile = ?")
	if err := s.DB.GetContext(ctx, &kycRows, stmt, "tenant", mobile); err != nil {
		t.Fatalf("count kyc rows: %v", err)
	}
	if kycRows != 0 {
		t.Fatalf("kyc rows = %d, want 0", kycRows)
	}
}

func TestStore_CreateUserWithAuthAccountPersistsUserAndAccount(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	user := &ebs_fields.User{
		Mobile:     "google:sub-1",
		Username:   "google:sub-1",
		Email:      "google-user@example.test",
		Password:   "hashed-password",
		IsVerified: true,
	}
	account := &ebs_fields.AuthAccount{
		Provider:       "google",
		ProviderUserID: "sub-1",
		Email:          "google-user@example.test",
		EmailVerified:  true,
	}

	if err := s.CreateUserWithAuthAccount(ctx, "tenant", user, account); err != nil {
		t.Fatalf("CreateUserWithAuthAccount(): %v", err)
	}
	if user.ID <= 0 {
		t.Fatalf("user id = %d, want persisted id", user.ID)
	}
	if account.UserID != user.ID {
		t.Fatalf("account user id = %d, want %d", account.UserID, user.ID)
	}
	storedAccount, err := s.FindAuthAccount(ctx, "tenant", "google", "sub-1")
	if err != nil {
		t.Fatalf("FindAuthAccount(): %v", err)
	}
	if storedAccount.UserID != user.ID {
		t.Fatalf("stored account user id = %d, want %d", storedAccount.UserID, user.ID)
	}
}

func TestStore_CreateUserWithAuthAccountRollsBackWhenLinkFails(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	existingUser := &ebs_fields.User{
		Mobile:   "google:existing",
		Username: "google:existing",
		Email:    "existing-google@example.test",
		Password: "hashed-password",
	}
	existingAccount := &ebs_fields.AuthAccount{Provider: "google", ProviderUserID: "existing-sub"}
	if err := s.CreateUserWithAuthAccount(ctx, "tenant", existingUser, existingAccount); err != nil {
		t.Fatalf("seed auth account: %v", err)
	}
	if _, err := s.DB.ExecContext(ctx, "CREATE UNIQUE INDEX auth_accounts_single_row_test_idx ON auth_accounts((true))"); err != nil {
		t.Fatalf("create failing auth_accounts index: %v", err)
	}

	newUser := &ebs_fields.User{
		Mobile:   "google:new-sub",
		Username: "google:new-sub",
		Email:    "new-google@example.test",
		Password: "hashed-password",
	}
	newAccount := &ebs_fields.AuthAccount{Provider: "google", ProviderUserID: "new-sub"}
	if err := s.CreateUserWithAuthAccount(ctx, "tenant", newUser, newAccount); err == nil {
		t.Fatal("CreateUserWithAuthAccount() error = nil, want auth link failure")
	}
	if _, err := s.FindUserByEmail(ctx, "tenant", "new-google@example.test"); !ErrNotFound(err) {
		t.Fatalf("FindUserByEmail() error = %v, want not found after rollback", err)
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
