package walletgrpc

import (
	"context"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkflowRequestsValidateTenantBeforeTemporal(t *testing.T) {
	server := NewServer(&wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	})
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(string) error
	}{
		{"deposit", func(tenantID string) error {
			_, err := server.RequestDeposit(ctx, &walletv1.DepositRequest{
				TenantId:        tenantID,
				ClientReference: "deposit-ref",
				ProviderCode:    "noop",
				WalletId:        uuid.NewString(),
				OwnerType:       "user",
				OwnerId:         "42",
				Amount:          100,
				Currency:        "USD",
			})
			return err
		}},
		{"p2p", func(tenantID string) error {
			_, err := server.RequestP2PTransfer(ctx, &walletv1.P2PTransferRequest{
				TenantId:       tenantID,
				IdempotencyKey: "p2p-ref",
				Currency:       "USD",
				FromWalletId:   uuid.NewString(),
				ToWalletId:     uuid.NewString(),
				Amount:         100,
				FromOwnerType:  "user",
				FromOwnerId:    "1",
				ToOwnerType:    "user",
				ToOwnerId:      "2",
			})
			return err
		}},
		{"manual_transfer", func(tenantID string) error {
			_, err := server.RequestManualTransfer(ctx, &walletv1.ManualTransferRequest{
				TenantId:               tenantID,
				IdempotencyKey:         "manual-ref",
				TransferType:           "manual_debit",
				WalletId:               uuid.NewString(),
				Amount:                 100,
				Currency:               "USD",
				Reason:                 "test",
				RequestedBy:            10,
				ApprovalTimeoutSeconds: 60,
			})
			return err
		}},
		{"withdrawal", func(tenantID string) error {
			_, err := server.RequestWithdrawal(ctx, &walletv1.WithdrawalRequest{
				TenantId:          tenantID,
				ClientReference:   "withdrawal-ref",
				ProviderCode:      "noop",
				WalletId:          uuid.NewString(),
				Amount:            100,
				Currency:          "USD",
				OwnerType:         "user",
				OwnerId:           "42",
				HoldExpirySeconds: 60,
			})
			return err
		}},
	}
	tenantCases := []struct {
		tenantID string
		wantErr  error
	}{
		{"", walletstore.ErrMissingTenantID},
		{"default", walletstore.ErrInvalidTenantID},
	}
	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.tenantID, func(t *testing.T) {
				err := tc.run(tenantCase.tenantID)
				if status.Code(err) != codes.InvalidArgument {
					t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
				}
				if status.Convert(err).Message() != tenantCase.wantErr.Error() {
					t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), tenantCase.wantErr.Error())
				}
			})
		}
	}
}
