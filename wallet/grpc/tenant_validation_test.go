package walletgrpc

import (
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkflowRequestsRejectInvalidOrMismatchedTenantBeforeTemporal(t *testing.T) {
	server := NewServer(&wallet.Service{
		Store:  &walletstore.Store{},
		Config: ebs_fields.NoebsConfig{},
	})
	ctx := walletGatewayIdentityContext(42, "tenant")
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
				FromOwnerId:    "42",
				ToOwnerType:    "user",
				ToOwnerId:      "2",
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
		wantCode codes.Code
		wantErr  error
	}{
		{"default", codes.InvalidArgument, walletstore.ErrInvalidTenantID},
		{"other-tenant", codes.PermissionDenied, nil},
	}
	for _, tc := range cases {
		for _, tenantCase := range tenantCases {
			t.Run(tc.name+"/"+tenantCase.tenantID, func(t *testing.T) {
				err := tc.run(tenantCase.tenantID)
				if status.Code(err) != tenantCase.wantCode {
					t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), tenantCase.wantCode)
				}
				if tenantCase.wantErr != nil && status.Convert(err).Message() != tenantCase.wantErr.Error() {
					t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), tenantCase.wantErr.Error())
				}
			})
		}
	}
}

func TestWorkflowRequestsRejectBlankRequiredTextBeforeTemporal(t *testing.T) {
	baseServer := func(cfg ebs_fields.NoebsConfig) *Server {
		return NewServer(&wallet.Service{
			Store:  &walletstore.Store{},
			Config: cfg,
		})
	}
	ctx := walletGatewayIdentityContext(42, "tenant")

	cases := []struct {
		name    string
		run     func() error
		wantErr error
	}{
		{
			name: "deposit-client-reference",
			run: func() error {
				_, err := baseServer(ebs_fields.NoebsConfig{}).RequestDeposit(ctx, &walletv1.DepositRequest{
					TenantId:        "tenant",
					ClientReference: " \t ",
					ProviderCode:    "noop",
					WalletId:        uuid.NewString(),
					OwnerType:       "user",
					OwnerId:         "42",
					Amount:          100,
					Currency:        "USD",
				})
				return err
			},
			wantErr: walletstore.ErrMissingClientReference,
		},
		{
			name: "deposit-provider",
			run: func() error {
				_, err := baseServer(ebs_fields.NoebsConfig{}).RequestDeposit(ctx, &walletv1.DepositRequest{
					TenantId:        "tenant",
					ClientReference: "deposit-ref",
					ProviderCode:    " \t ",
					WalletId:        uuid.NewString(),
					OwnerType:       "user",
					OwnerId:         "42",
					Amount:          100,
					Currency:        "USD",
				})
				return err
			},
			wantErr: walletstore.ErrMissingProviderCode,
		},
		{
			name: "withdrawal-currency",
			run: func() error {
				_, err := baseServer(ebs_fields.NoebsConfig{}).RequestWithdrawal(ctx, &walletv1.WithdrawalRequest{
					TenantId:          "tenant",
					ClientReference:   "withdrawal-ref",
					ProviderCode:      "noop",
					WalletId:          uuid.NewString(),
					Amount:            100,
					Currency:          " \t ",
					OwnerType:         "user",
					OwnerId:           "42",
					HoldExpirySeconds: 60,
				})
				return err
			},
			wantErr: walletstore.ErrMissingCurrency,
		},
		{
			name: "p2p-idempotency-and-reference",
			run: func() error {
				_, err := baseServer(ebs_fields.NoebsConfig{}).RequestP2PTransfer(ctx, &walletv1.P2PTransferRequest{
					TenantId:       "tenant",
					IdempotencyKey: " \t ",
					ReferenceId:    " \t ",
					Currency:       "USD",
					FromWalletId:   uuid.NewString(),
					ToWalletId:     uuid.NewString(),
					Amount:         100,
					FromOwnerType:  "user",
					FromOwnerId:    "42",
					ToOwnerType:    "user",
					ToOwnerId:      "2",
				})
				return err
			},
			wantErr: walletstore.ErrMissingIdempotencyKey,
		},
		{
			name: "p2p-to-owner",
			run: func() error {
				_, err := baseServer(ebs_fields.NoebsConfig{}).RequestP2PTransfer(ctx, &walletv1.P2PTransferRequest{
					TenantId:       "tenant",
					IdempotencyKey: "p2p-ref",
					Currency:       "USD",
					FromWalletId:   uuid.NewString(),
					ToWalletId:     uuid.NewString(),
					Amount:         100,
					FromOwnerType:  "user",
					FromOwnerId:    "42",
					ToOwnerType:    " \t ",
					ToOwnerId:      "2",
				})
				return err
			},
			wantErr: walletstore.ErrMissingOwnerType,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
			}
			if status.Convert(err).Message() != tc.wantErr.Error() {
				t.Fatalf("status message = %q, want %q", status.Convert(err).Message(), tc.wantErr.Error())
			}
		})
	}
}
