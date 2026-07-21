package walletgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/testdb"
	basestore "github.com/adonese/noebs/store"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func newWalletServerWithUsers(t *testing.T) (*Server, string, *walletstore.Wallet, *walletstore.Wallet) {
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
	if err := basestore.MigrateScope(context.Background(), db, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	cfg := ebs_fields.NoebsConfig{
		WalletEnabled:         true,
		WalletDefaultCurrency: "USD",
	}
	service := wallet.NewService(db, cfg)
	server := NewServer(service)

	provisionWalletGRPCTestTenant(t, context.Background(), db, tenantID, "Public Auth Tenant")

	wallet42, err := ensureUserWalletForTest(t, context.Background(), service, tenantID, 42, "USD")
	if err != nil {
		t.Fatalf("ensure wallet 42: %v", err)
	}
	wallet7, err := ensureUserWalletForTest(t, context.Background(), service, tenantID, 7, "USD")
	if err != nil {
		t.Fatalf("ensure wallet 7: %v", err)
	}
	return server, tenantID, wallet42, wallet7
}

func walletGatewayIdentityContext(userID int64, tenantID string) context.Context {
	return metadata.NewIncomingContext(context.Background(), userMetadata(userID, tenantID))
}

func TestGetWalletPublicEnforcesGatewayIdentityOwnership(t *testing.T) {
	server, tenantID, wallet42, wallet7 := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(42, tenantID)

	resp, err := server.GetWalletPublic(ctx, &walletv1.GetWalletPublicRequest{
		WalletId: wallet42.ID.String(),
	})
	if err != nil {
		t.Fatalf("GetWalletPublic(own wallet) error = %v", err)
	}
	if resp.GetWallet().GetId() != wallet42.ID.String() {
		t.Fatalf("unexpected wallet response: %+v", resp)
	}
	if resp.GetWallet().GetBalanceMoney().GetMinorUnits() != "0" ||
		resp.GetWallet().GetBalanceMoney().GetCurrencyUnitVersionId() != wallet42.CurrencyUnitID ||
		resp.GetWallet().GetAvailableBalanceMoney().GetCurrencyUnitVersionId() != wallet42.CurrencyUnitID {
		t.Fatalf("wallet money does not use pinned unit %d: %+v", wallet42.CurrencyUnitID, resp.GetWallet())
	}

	_, err = server.GetWalletPublic(ctx, &walletv1.GetWalletPublicRequest{
		TenantId: tenantID,
		WalletId: wallet7.ID.String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.NotFound)
	}
}

func TestEnsureWalletRejectsDisabledAndNonOperationalCurrencies(t *testing.T) {
	server, tenantID, _, _ := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(99, tenantID)

	if _, err := server.Service.Store.DB.ExecContext(ctx,
		`UPDATE currencies SET is_active = FALSE WHERE code = 'AED'`,
	); err != nil {
		t.Fatalf("disable AED: %v", err)
	}
	_, err := server.EnsureWalletPublic(ctx, &walletv1.EnsureWalletPublicRequest{
		TenantId: tenantID,
		UserId:   99,
		Currency: "AED",
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("disabled currency status = %v, want %v: %v", status.Code(err), codes.NotFound, err)
	}

	if _, err := server.Service.Store.DB.ExecContext(ctx, `
		INSERT INTO currencies(code, numeric_code, name, kind, is_active)
		VALUES('ZZZ', NULL, 'No operational scale', 'test', TRUE);
		INSERT INTO currency_unit_versions(
			currency_code, iso_minor_exponent, display_exponent, cash_exponent,
			cash_rounding_increment, valid_from, source, source_revision, source_published_on
		) VALUES('ZZZ', NULL, 2, 2, 1, '2026-01-01', 'test', 'no-scale', '2026-01-01')`); err != nil {
		t.Fatalf("insert non-operational currency: %v", err)
	}
	_, err = server.EnsureWalletPublic(ctx, &walletv1.EnsureWalletPublicRequest{
		TenantId: tenantID,
		UserId:   99,
		Currency: "ZZZ",
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("non-operational currency status = %v, want %v: %v", status.Code(err), codes.FailedPrecondition, err)
	}

	var created int
	if err := server.Service.Store.DB.GetContext(ctx, &created,
		`SELECT count(*) FROM wallets WHERE tenant_id = $1 AND user_id = 99 AND currency IN ('AED', 'ZZZ')`, tenantID,
	); err != nil {
		t.Fatalf("count invalid wallets: %v", err)
	}
	if created != 0 {
		t.Fatalf("invalid wallets created = %d, want 0", created)
	}
}

func TestRequestWithdrawalRejectsGatewayIdentityMismatch(t *testing.T) {
	server, tenantID, wallet42, _ := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(42, tenantID)
	allowReturn := true
	approvalRequired := false

	req := &walletv1.RequestWithdrawalRequest{
		TenantId:            tenantID,
		ClientReference:     "ref-1",
		IdempotencyKey:      "ref-1",
		ProviderCode:        "noop",
		WalletId:            wallet42.ID.String(),
		Amount:              100,
		Currency:            "USD",
		OwnerType:           walletstore.OwnerTypeUser,
		OwnerId:             "7",
		DestinationId:       10,
		AllowReturnToSource: &allowReturn,
		HoldExpirySeconds:   60,
		ApprovalRequired:    &approvalRequired,
	}

	_, err := server.RequestWithdrawal(ctx, req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}

func TestPublicPSPRequestsBindGatewayIdentityBeforeTenantAndOwnerValidation(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	ctx := walletGatewayIdentityContext(42, "tenant-a")

	cases := []struct {
		name string
		run  func() error
	}{
		{
			name: "deposit missing tenant",
			run: func() error {
				_, err := server.RequestDeposit(
					walletServerMethodContext(ctx, walletv1.WalletPublicService_RequestDeposit_FullMethodName),
					&walletv1.RequestDepositRequest{
						IdempotencyKey: "deposit-ref-tenant",
						ProviderCode:   "noop",
						WalletId:       "not-a-uuid",
						Amount:         100,
						Currency:       "USD",
					},
				)
				return err
			},
		},
		{
			name: "deposit missing owner",
			run: func() error {
				_, err := server.RequestDeposit(
					walletServerMethodContext(ctx, walletv1.WalletPublicService_RequestDeposit_FullMethodName),
					&walletv1.RequestDepositRequest{
						TenantId:       "tenant-a",
						IdempotencyKey: "deposit-ref-owner",
						ProviderCode:   "noop",
						WalletId:       "not-a-uuid",
						Amount:         100,
						Currency:       "USD",
					},
				)
				return err
			},
		},
		{
			name: "withdrawal missing tenant",
			run: func() error {
				_, err := server.RequestWithdrawal(
					walletServerMethodContext(ctx, walletv1.WalletPublicService_RequestWithdrawal_FullMethodName),
					&walletv1.RequestWithdrawalRequest{
						ClientReference:   "withdrawal-ref-tenant",
						ProviderCode:      "noop",
						WalletId:          "not-a-uuid",
						OwnerType:         walletstore.OwnerTypeUser,
						OwnerId:           "42",
						Amount:            100,
						Currency:          "USD",
						HoldExpirySeconds: 60,
					},
				)
				return err
			},
		},
		{
			name: "withdrawal missing owner",
			run: func() error {
				_, err := server.RequestWithdrawal(
					walletServerMethodContext(ctx, walletv1.WalletPublicService_RequestWithdrawal_FullMethodName),
					&walletv1.RequestWithdrawalRequest{
						TenantId:          "tenant-a",
						ClientReference:   "withdrawal-ref-owner",
						ProviderCode:      "noop",
						WalletId:          "not-a-uuid",
						Amount:            100,
						Currency:          "USD",
						HoldExpirySeconds: 60,
					},
				)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
			}
			if status.Convert(err).Message() != walletstore.ErrMissingWalletID.Error() {
				t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), walletstore.ErrMissingWalletID.Error())
			}
		})
	}
}
