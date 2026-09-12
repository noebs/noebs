package walletgrpc

import (
	"context"
	"strconv"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestP2PPreviewAndAdmissionRecovery(t *testing.T) {
	ctx := context.Background()
	s, tenant, db := newWalletPSPTestServer(t, ebs_fields.NoebsConfig{})
	provisionWalletGRPCTestTenant(t, ctx, db, tenant, "P2P receipt test")
	from, err := ensureUserWalletForTest(t, ctx, s.Service, tenant, 1, "USD")
	if err != nil {
		t.Fatal(err)
	}
	to, err := ensureUserWalletForTest(t, ctx, s.Service, tenant, 2, "USD")
	if err != nil {
		t.Fatal(err)
	}
	setWalletBalances(t, ctx, db, tenant, from.ID, 1000, 1000)
	op := resolveWalletGRPCTestOperator(t, ctx, db, "p2p-preview")
	seedP2PValidationRules(t, ctx, db, tenant, "USD", op)
	caller := walletGatewayIdentityContext(1, tenant)
	req := &walletv1.PreviewP2PTransferRequest{TenantId: tenant, FromWalletId: from.ID.String(), ToWalletId: to.ID.String(), Amount: 125, Currency: "USD"}
	preview, err := s.PreviewP2PTransfer(caller, req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ToOwnerId != "2" || preview.ToOwnerType != "user" || preview.AmountMinor != "125" || preview.FeeAmountMinor != "0" || preview.TotalDebitMinor != "125" || preview.CurrencyUnitVersion != strconv.FormatInt(from.CurrencyUnitID, 10) {
		t.Fatal(preview)
	}
	for _, tt := range []struct {
		name   string
		mutate func(*walletv1.PreviewP2PTransferRequest)
		user   int64
		code   codes.Code
	}{
		{"same wallet", func(r *walletv1.PreviewP2PTransferRequest) { r.ToWalletId = r.FromWalletId }, 1, codes.InvalidArgument},
		{"foreign source", func(r *walletv1.PreviewP2PTransferRequest) {}, 2, codes.NotFound},
		{"wrong tenant", func(r *walletv1.PreviewP2PTransferRequest) { r.TenantId = "another" }, 1, codes.PermissionDenied},
		{"missing amount", func(r *walletv1.PreviewP2PTransferRequest) { r.Amount = 0 }, 1, codes.InvalidArgument},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := proto.Clone(req).(*walletv1.PreviewP2PTransferRequest)
			tt.mutate(r)
			_, e := s.PreviewP2PTransfer(walletGatewayIdentityContext(tt.user, tenant), r)
			if status.Code(e) != tt.code {
				t.Fatalf("got %v want %v", e, tt.code)
			}
		})
	}
	zero, unit := int64(0), from.CurrencyUnitID
	command := &walletv1.RequestP2PTransferRequest{TenantId: tenant, IdempotencyKey: "fee-changed", ReferenceId: "fee-changed", FromWalletId: from.ID.String(), ToWalletId: to.ID.String(), Currency: "USD", Amount: 125, ToOwnerType: "user", ToOwnerId: "2", ExpectedFeeAmount: &zero, ExpectedCurrencyUnitVersion: &unit}
	if _, err = db.ExecContext(ctx, `UPDATE fee_configs SET flat_fee=5 WHERE tenant_id=$1 AND transaction_type='p2p'`, tenant); err != nil {
		t.Fatal(err)
	}
	_, err = s.RequestP2PTransfer(caller, command)
	if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != walletstore.ErrP2PFeeChanged.Error() {
		t.Fatalf("stale fee: %v", err)
	}
	receipt, err := s.GetP2PTransferStatus(caller, &walletv1.GetP2PTransferStatusRequest{TenantId: tenant, IdempotencyKey: command.IdempotencyKey})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != "failed" || receipt.ErrorCode != "p2p_fee_changed" || receipt.FeeAmountMinor != "0" || receipt.AmountMinor != "125" || receipt.CurrencyUnitVersion != strconv.FormatInt(unit, 10) || receipt.TransactionId != "" {
		t.Fatal(receipt)
	}
	for _, scope := range []struct {
		user   int64
		tenant string
	}{{2, tenant}, {1, "another"}} {
		_, e := s.GetP2PTransferStatus(walletGatewayIdentityContext(scope.user, scope.tenant), &walletv1.GetP2PTransferStatusRequest{TenantId: scope.tenant, IdempotencyKey: command.IdempotencyKey})
		if status.Code(e) != codes.NotFound {
			t.Fatalf("scope leak: %v", e)
		}
	}

	t.Run("recipient becomes inactive after review", func(t *testing.T) {
		if _, e := db.ExecContext(ctx, `UPDATE fee_configs SET flat_fee=0 WHERE tenant_id=$1 AND transaction_type='p2p'`, tenant); e != nil {
			t.Fatal(e)
		}
		if _, e := s.PreviewP2PTransfer(caller, req); e != nil {
			t.Fatal(e)
		}
		if _, e := db.ExecContext(ctx, `UPDATE wallets SET status='frozen' WHERE tenant_id=$1 AND id=$2`, tenant, to.ID); e != nil {
			t.Fatal(e)
		}
		inactive := proto.Clone(command).(*walletv1.RequestP2PTransferRequest)
		inactive.IdempotencyKey = "recipient-inactive"
		inactive.ReferenceId = inactive.IdempotencyKey
		_, e := s.RequestP2PTransfer(caller, inactive)
		if status.Code(e) != codes.FailedPrecondition {
			t.Fatalf("inactive admission: %v", e)
		}
		r, e := s.GetP2PTransferStatus(caller, &walletv1.GetP2PTransferStatusRequest{TenantId: tenant, IdempotencyKey: inactive.IdempotencyKey})
		if e != nil || r.Status != "failed" || r.TransactionId != "" {
			t.Fatalf("inactive receipt=%+v err=%v", r, e)
		}
		_, e = s.PreviewP2PTransfer(caller, req)
		if status.Code(e) != codes.NotFound {
			t.Fatalf("inactive preview: %v", e)
		}
	})
	var count int
	if err = db.GetContext(ctx, &count, `SELECT count(*) FROM ledger_transactions WHERE tenant_id=$1`, tenant); err != nil || count != 0 {
		t.Fatalf("admission changed ledger count=%d err=%v", count, err)
	}
}
