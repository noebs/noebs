package store

import (
	"context"
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
	s := newTestStore(t)
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
