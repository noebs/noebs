package walletgrpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRequestDepositValidatesWalletBeforePersistingPSPTransaction(t *testing.T) {
	server, tenantID, _ := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	req := &walletv1.RequestDepositRequest{
		TenantId:       tenantID,
		IdempotencyKey: "deposit-missing-wallet",
		ProviderCode:   "noop",
		WalletId:       uuid.NewString(),
		Amount:         100,
		Currency:       "USD",
	}

	_, err := server.RequestDeposit(walletGatewayIdentityContext(42, tenantID), req)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.NotFound)
	}
	if status.Convert(err).Message() != walletstore.ErrWalletNotFound.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrWalletNotFound.Error())
	}
	if _, err := server.Service.Store.GetDepositIntentByIdempotency(context.Background(), tenantID, req.ProviderCode, req.IdempotencyKey); !errors.Is(err, walletstore.ErrDepositIntentNotFound) {
		t.Fatalf("stored deposit intent error = %v, want %v", err, walletstore.ErrDepositIntentNotFound)
	}
}

func TestRequestWithdrawalValidatesWalletBeforePersistingPSPTransaction(t *testing.T) {
	server, tenantID, _ := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{WalletHoldExpirySeconds: 60})
	allowReturn := true
	approvalRequired := false
	req := &walletv1.RequestWithdrawalRequest{
		TenantId:            tenantID,
		IdempotencyKey:      "withdrawal-missing-wallet",
		ClientReference:     "withdrawal-missing-wallet",
		ProviderCode:        "noop",
		WalletId:            uuid.NewString(),
		OwnerType:           walletstore.OwnerTypeUser,
		OwnerId:             "42",
		Amount:              100,
		Currency:            "USD",
		AllowReturnToSource: &allowReturn,
		HoldExpirySeconds:   60,
		ApprovalRequired:    &approvalRequired,
	}

	_, err := server.RequestWithdrawal(walletGatewayIdentityContext(42, tenantID), req)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.NotFound)
	}
	if status.Convert(err).Message() != walletstore.ErrWalletNotFound.Error() {
		t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrWalletNotFound.Error())
	}
	if _, err := server.Service.Store.GetPSPTransactionByReference(context.Background(), tenantID, req.ClientReference); !errors.Is(err, walletstore.ErrPSPTransactionNotFound) {
		t.Fatalf("stored PSP transaction error = %v, want %v", err, walletstore.ErrPSPTransactionNotFound)
	}
}

func newWalletPSPTestServer(t *testing.T, cfg ebs_fields.NoebsConfig) (*Server, string, *basestore.DB) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	container, err := testdb.StartPostgresContainer(ctx)
	if err != nil {
		if testdb.IsContainerRuntimeUnavailable(err) {
			t.Skipf("container runtime unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	const dbName = "wallet_ledger"
	dbURL, err := container.CreateDatabaseForRole(ctx, dbName, "wallet_ledger_migrate")
	if err != nil {
		t.Fatalf("create database: %v", err)
	}

	db, err := basestore.OpenFromConfig(dbURL, basestore.DriverPostgres)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer dropCancel()
		_ = container.DropDatabase(dropCtx, dbName)
	})

	tenantID := "tenant"
	if err := basestore.MigrateScope(ctx, db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate wallet-ledger db: %v", err)
	}
	return NewServer(wallet.NewService(db, cfg)), tenantID, db
}

func seedWalletValidationRules(t *testing.T, ctx context.Context, db *basestore.DB, tenantID, providerCode, currency string, operatorID int64, supportsDeposit, supportsWithdrawal bool) {
	t.Helper()

	pspStmt := db.Rebind(`INSERT INTO psp_configs(
		tenant_id, provider_code, provider_name, api_base_url, idempotency_header_name, enabled_currencies,
		is_active, supports_deposit, supports_withdrawal, method_type, display_name,
		supported_regions, min_amount, max_amount, deposit_input_schema, presentation_schema,
		deposit_response_mapping
	) VALUES(?, ?, ?, 'https://psp.example', 'Idempotency-Key', ARRAY[?], TRUE, ?, ?, 'redirect', ?, ARRAY['US'], 1, 1000000000, '{}', '{}',
		'{"client_reference":["client_reference"],"transaction_id":["transaction_id"],"status":["status"],"amount":["amount"],"currency":["currency"]}')
	ON CONFLICT (tenant_id, provider_code) DO NOTHING`)
	if _, err := db.ExecContext(ctx, pspStmt, tenantID, providerCode, "Test PSP", currency, supportsDeposit, supportsWithdrawal, "Test PSP"); err != nil {
		t.Fatalf("seed psp config: %v", err)
	}

	for _, txType := range []string{"deposit", "withdrawal"} {
		feeStmt := db.Rebind(`INSERT INTO fee_configs(
			tenant_id, transaction_type, currency, tier_min, percentage_fee, flat_fee, min_fee, is_active,
			created_by_operator_id
		) VALUES(?, ?, ?, 0, 0, 0, 0, TRUE, ?)
		ON CONFLICT (tenant_id, transaction_type, currency, tier_min) DO NOTHING`)
		if _, err := db.ExecContext(ctx, feeStmt, tenantID, txType, currency, operatorID); err != nil {
			t.Fatalf("seed %s fee config: %v", txType, err)
		}

		limitStmt := db.Rebind(`INSERT INTO transaction_limits(
			tenant_id, kyc_tier, transaction_type, currency, daily_limit, monthly_limit, per_transaction_limit, is_active
		) VALUES(?, ?, ?, ?, 1000000000, 1000000000, 1000000000, TRUE)
		ON CONFLICT (tenant_id, kyc_tier, transaction_type, currency) DO NOTHING`)
		if _, err := db.ExecContext(ctx, limitStmt, tenantID, walletstore.KYCTierUnverified, txType, currency); err != nil {
			t.Fatalf("seed %s transaction limit: %v", txType, err)
		}
	}
}

func setWalletBalances(t *testing.T, ctx context.Context, db *basestore.DB, tenantID string, walletID uuid.UUID, balance, available int64) {
	t.Helper()

	stmt := db.Rebind(`UPDATE wallets SET balance = ?, available_balance = ?, updated_at = NOW() WHERE tenant_id = ? AND id = ?`)
	result, err := db.ExecContext(ctx, stmt, balance, available, tenantID, walletID)
	if err != nil {
		t.Fatalf("set wallet balances: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("set wallet balances rows affected: %v", err)
	}
	if affected != 1 {
		t.Fatalf("set wallet balances rows affected = %d, want 1", affected)
	}
}
