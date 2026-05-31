package consumer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestBeneficiaryServiceUsesGatewayUserIDOnly(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeConsumerBeneficiary})
	service := &Service{Store: storeSvc}
	ctx := context.Background()
	userID := int64(42)

	if err := service.UpsertBeneficiaryForUserID(ctx, tenantID, userID, ebs_fields.Beneficiary{
		Data:     "0912141660",
		BillType: "0010010001",
		Name:     "Primary",
	}); err != nil {
		t.Fatalf("upsert beneficiary: %v", err)
	}

	list, err := service.ListBeneficiariesForUserID(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("list beneficiaries: %v", err)
	}
	if len(list) != 1 || list[0].UserID != userID || list[0].Data != "0912141660" || list[0].BillType != "0010010001" {
		t.Fatalf("beneficiaries = %+v", list)
	}

	if err := service.UpsertBeneficiaryForUserID(ctx, tenantID, userID, ebs_fields.Beneficiary{
		Data:     "0912141660",
		BillType: "0010010002",
		Name:     "Updated",
	}); err != nil {
		t.Fatalf("repeat upsert beneficiary: %v", err)
	}
	list, err = service.ListBeneficiariesForUserID(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("list beneficiaries after repeat upsert: %v", err)
	}
	if len(list) != 1 || list[0].BillType != "0010010002" || list[0].Name != "Updated" {
		t.Fatalf("beneficiaries after repeat upsert = %+v", list)
	}

	if _, err := db.ExecContext(ctx, "SELECT 1 FROM users LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("consumer-beneficiary scope should not create user tables, err=%v", err)
	}

	if err := service.DeleteBeneficiaryForUserID(ctx, tenantID, userID, "0912141660"); err != nil {
		t.Fatalf("delete beneficiary: %v", err)
	}
	list, err = service.ListBeneficiariesForUserID(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("list beneficiaries after delete: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("beneficiaries after delete = %+v", list)
	}
	if err := service.DeleteBeneficiaryForUserID(ctx, tenantID, userID, "0912141660"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("delete missing beneficiary error = %v, want %v", err, sql.ErrNoRows)
	}
}

func TestBeneficiaryServiceRejectsMissingUserID(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	ctx := context.Background()
	tenantID := "tenant"

	if _, err := service.ListBeneficiariesForUserID(ctx, tenantID, 0); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("list missing user_id error = %v", err)
	}
	if err := service.UpsertBeneficiaryForUserID(ctx, tenantID, 0, ebs_fields.Beneficiary{Data: "0912141660", BillType: "0010010001"}); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("upsert missing user_id error = %v", err)
	}
	if err := service.DeleteBeneficiaryForUserID(ctx, tenantID, 0, "0912141660"); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("delete missing user_id error = %v", err)
	}
}
