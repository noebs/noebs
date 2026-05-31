package walletgrpc

import (
	"context"
	"fmt"
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

	dbName := fmt.Sprintf("noebs_wallet_grpc_%d", time.Now().UnixNano())
	dbURL, err := container.CreateDatabase(ctx, dbName)
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
	if err := basestore.MigrateScope(context.Background(), db, tenantID, basestore.MigrationScopeWalletLedger); err != nil {
		t.Fatalf("migrate db: %v", err)
	}

	cfg := ebs_fields.NoebsConfig{
		WalletEnabled:         true,
		WalletDefaultCurrency: "USD",
	}
	service := wallet.NewService(db, cfg)
	server := NewServer(service)

	if err := basestore.New(db).EnsureTenant(context.Background(), tenantID); err != nil {
		t.Fatalf("ensure tenant: %v", err)
	}

	wallet42, err := service.EnsureUserWallet(context.Background(), tenantID, 42, "USD")
	if err != nil {
		t.Fatalf("ensure wallet 42: %v", err)
	}
	wallet7, err := service.EnsureUserWallet(context.Background(), tenantID, 7, "USD")
	if err != nil {
		t.Fatalf("ensure wallet 7: %v", err)
	}
	return server, tenantID, wallet42, wallet7
}

func walletGatewayIdentityContext(userID int64, tenantID string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		"x-noebs-tenant-id", tenantID,
		"x-noebs-user-id", fmt.Sprintf("%d", userID),
		"x-noebs-mobile", "0990000000",
	))
}

func TestGetWalletPublicEnforcesGatewayIdentityOwnership(t *testing.T) {
	server, tenantID, wallet42, wallet7 := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(42, tenantID)

	resp, err := server.GetWalletPublic(ctx, &walletv1.GetWalletRequest{
		WalletId: wallet42.ID.String(),
	})
	if err != nil {
		t.Fatalf("GetWalletPublic(own wallet) error = %v", err)
	}
	if resp == nil || resp.Id != wallet42.ID.String() {
		t.Fatalf("unexpected wallet response: %+v", resp)
	}

	_, err = server.GetWalletPublic(ctx, &walletv1.GetWalletRequest{
		TenantId: tenantID,
		WalletId: wallet7.ID.String(),
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.NotFound)
	}
}

func TestRequestWithdrawalRejectsGatewayIdentityMismatch(t *testing.T) {
	server, tenantID, wallet42, _ := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(42, tenantID)

	req := &walletv1.WithdrawalRequest{
		TenantId:                   tenantID,
		ClientReference:            "ref-1",
		ProviderCode:               "noop",
		WalletId:                   wallet42.ID.String(),
		Amount:                     100,
		Currency:                   "USD",
		OwnerType:                  walletstore.OwnerTypeUser,
		OwnerId:                    "7",
		DestinationId:              10,
		HoldExpirySeconds:          60,
		VerificationTimeoutSeconds: 60,
	}

	_, err := server.RequestWithdrawal(ctx, req)
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
}
