package walletgrpc

import (
	"crypto/tls"
	"testing"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestLocalTransferDiscoverySeparatesReceivingFromSenderPolicy(t *testing.T) {
	server, tenant, owned, other := newWalletServerWithUsers(t)
	ctx := walletGatewayIdentityContext(42, tenant)
	server.TemporalOptions = walletworker.Options{Host: "temporal.example", Port: "7233", Namespace: "wallet",
		TaskQueue: walletworker.TaskQueueMain, TLS: &tls.Config{MinVersion: tls.VersionTLS12},
		Credentials: client.NewAPIKeyStaticCredentials("isolated-fixture")}
	provider := func() *walletv1.WalletProvider {
		t.Helper()
		result, err := server.ListWalletProviders(ctx, &walletv1.ListWalletProvidersRequest{})
		if err != nil || len(result.GetProviders()) != 1 {
			t.Fatalf("local provider: %+v, %v", result, err)
		}
		return result.Providers[0]
	}
	funding := func() *walletv1.WalletFundingMethod {
		t.Helper()
		result, err := server.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{WalletId: owned.ID.String()})
		if err != nil || len(result.GetMethods()) != 1 {
			t.Fatalf("local funding: %+v, %v", result, err)
		}
		return result.Methods[0]
	}
	p := provider()
	if p.Id != "noebs" || !p.Available || p.TransferMode != "wallet_p2p" || p.FundingMode != "account_transfer" ||
		p.Capabilities.Send || !p.Capabilities.Receive || !p.Capabilities.Funding || p.Capabilities.Services {
		t.Fatalf("receiving needs no sender fee or limits: %+v", p)
	}
	m := funding()
	if !m.Available || m.Id != "noebs:receive" || m.AccountIdentifier != owned.ID.String() || m.AccountName != "" || m.Mode != "account_transfer" {
		t.Fatalf("owned reference must be exact and name left for identity authority: %+v", m)
	}
	_, err := server.ListWalletFundingMethods(ctx, &walletv1.ListWalletFundingMethodsRequest{WalletId: other.ID.String()})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("foreign reference disclosed: %v", err)
	}

	db := server.Service.Store.DB
	operator := resolveWalletGRPCTestOperator(t, ctx, db, "local-discovery")
	seedP2PValidationRules(t, ctx, db, tenant, "USD", operator)
	if !provider().Capabilities.Send {
		t.Fatal("configured sender policy not advertised")
	}
	for _, change := range []string{
		`UPDATE fee_configs SET is_active=false WHERE transaction_type='p2p'`,
		`UPDATE fee_configs SET is_active=true WHERE transaction_type='p2p'; UPDATE transaction_limits SET is_active=false WHERE transaction_type='p2p'`,
		`UPDATE transaction_limits SET is_active=true, kyc_tier='verified' WHERE transaction_type='p2p'`,
		`UPDATE transaction_limits SET kyc_tier='unverified', per_transaction_limit=10 WHERE transaction_type='p2p'; UPDATE fee_configs SET tier_min=11 WHERE transaction_type='p2p'`,
	} {
		if _, err := db.ExecContext(ctx, change); err != nil {
			t.Fatal(err)
		}
		if provider().Capabilities.Send || !funding().Available {
			t.Fatal("missing, disabled, wrong-tier or disjoint fee policy must disable sending only")
		}
	}
	if _, err := db.ExecContext(ctx, `UPDATE fee_configs SET tier_min=0,flat_fee=10 WHERE transaction_type='p2p'`); err != nil {
		t.Fatal(err)
	}
	if provider().Capabilities.Send {
		t.Fatal("fee consumes entire configured per-transfer limit")
	}
	if _, err := db.ExecContext(ctx, `UPDATE fee_configs SET flat_fee=0 WHERE transaction_type='p2p'`); err != nil {
		t.Fatal(err)
	}
	if !provider().Capabilities.Send {
		t.Fatal("valid policy should recover without restart")
	}
	current, err := server.Service.Store.GetWallet(ctx, tenant, owned.ID)
	if err != nil || current.Balance != owned.Balance || current.AvailableBalance != owned.AvailableBalance || current.Version != owned.Version {
		t.Fatalf("read-only discovery changed wallet: %+v, %v", current, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE wallets SET status='frozen' WHERE id=$1`, owned.ID); err != nil {
		t.Fatal(err)
	}
	if p := provider(); p.Available || p.Capabilities.Receive || p.Capabilities.Send {
		t.Fatalf("inactive account advertised: %+v", p)
	}
	if m := funding(); m.Available || m.AccountIdentifier != "" || m.UnavailableReason != "wallet_inactive" {
		t.Fatalf("inactive receiving details exposed: %+v", m)
	}
	server.TemporalOptions.TaskQueue = ""
	result, err := server.ListWalletProviders(ctx, &walletv1.ListWalletProvidersRequest{})
	if err != nil || len(result.Providers) != 0 {
		t.Fatalf("unconfigured local runtime advertised: %+v, %v", result, err)
	}
}
