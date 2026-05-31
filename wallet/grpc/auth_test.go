package walletgrpc

import (
	"context"
	"testing"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestClaimsFromContextValidatesGatewayTenant(t *testing.T) {
	server := NewServer(&wallet.Service{})
	cases := []struct {
		name string
		md   metadata.MD
	}{
		{"missing_tenant", metadata.Pairs(
			gateway.GatewayUserIDHeader, "42",
			gateway.GatewayMobileHeader, "0990000000",
		)},
		{"reserved_tenant", metadata.Pairs(
			gateway.GatewayTenantIDHeader, "default",
			gateway.GatewayUserIDHeader, "42",
			gateway.GatewayMobileHeader, "0990000000",
		)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := metadata.NewIncomingContext(context.Background(), tc.md)
			_, err := server.claimsFromContext(ctx)
			if status.Code(err) != codes.Unauthenticated {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.Unauthenticated)
			}
		})
	}
}

func TestClaimsForRPCRequiresGatewayIdentityOnPublicUserMethods(t *testing.T) {
	server := NewServer(&wallet.Service{})

	_, err := server.claimsForRPC(walletServerMethodContext(context.Background(), walletv1.WalletPublicService_RequestP2PTransfer_FullMethodName))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("public user method status = %v, want %v", status.Code(err), codes.Unauthenticated)
	}

	claims, err := server.claimsForRPC(walletServerMethodContext(walletGatewayIdentityContext(42, "tenant-a"), walletv1.WalletPublicService_RequestP2PTransfer_FullMethodName))
	if err != nil {
		t.Fatalf("public user method with identity error = %v", err)
	}
	if claims == nil || claims.UserID != 42 || claims.TenantID != "tenant-a" {
		t.Fatalf("claims = %+v, want gateway identity", claims)
	}

	claims, err = server.claimsForRPC(walletServerMethodContext(context.Background(), walletv1.WalletPublicService_RequestManualTransfer_FullMethodName))
	if err != nil {
		t.Fatalf("public admin method should be handled by admin auth, got %v", err)
	}
	if claims != nil {
		t.Fatalf("public admin method claims = %+v, want nil without user identity", claims)
	}

	claims, err = server.claimsForRPC(walletServerMethodContext(context.Background(), walletv1.WalletInternalService_RequestP2PTransfer_FullMethodName))
	if err != nil {
		t.Fatalf("internal method error = %v", err)
	}
	if claims != nil {
		t.Fatalf("internal method claims = %+v, want nil without gateway identity", claims)
	}
}

func TestWalletInternalRPCsRequireAdminMetadataInHandlers(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	tests := []struct {
		name   string
		method string
		call   func(context.Context) error
	}{
		{
			name:   "get wallet",
			method: walletv1.WalletInternalService_GetWallet_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.GetWallet(ctx, nil)
				return err
			},
		},
		{
			name:   "ensure wallet",
			method: walletv1.WalletInternalService_EnsureWallet_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.EnsureWallet(ctx, nil)
				return err
			},
		},
		{
			name:   "validate p2p",
			method: walletv1.WalletInternalService_ValidateP2P_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.ValidateP2P(ctx, nil)
				return err
			},
		},
		{
			name:   "request p2p",
			method: walletv1.WalletInternalService_RequestP2PTransfer_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.RequestP2PTransfer(ctx, nil)
				return err
			},
		},
		{
			name:   "request deposit",
			method: walletv1.WalletInternalService_RequestDeposit_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.RequestDeposit(ctx, nil)
				return err
			},
		},
		{
			name:   "request withdrawal",
			method: walletv1.WalletInternalService_RequestWithdrawal_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.RequestWithdrawal(ctx, nil)
				return err
			},
		},
		{
			name:   "request manual transfer",
			method: walletv1.WalletInternalService_RequestManualTransfer_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.RequestManualTransfer(ctx, nil)
				return err
			},
		},
		{
			name:   "signal withdrawal approval",
			method: walletv1.WalletInternalService_SignalWithdrawalApproval_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.SignalWithdrawalApproval(ctx, nil)
				return err
			},
		},
		{
			name:   "signal withdrawal verification",
			method: walletv1.WalletInternalService_SignalWithdrawalVerification_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.SignalWithdrawalVerification(ctx, nil)
				return err
			},
		},
		{
			name:   "signal manual transfer decision",
			method: walletv1.WalletInternalService_SignalManualTransferDecision_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.SignalManualTransferDecision(ctx, nil)
				return err
			},
		},
		{
			name:   "create funding source",
			method: walletv1.WalletInternalService_CreateFundingSource_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.CreateFundingSource(ctx, nil)
				return err
			},
		},
		{
			name:   "list funding sources",
			method: walletv1.WalletInternalService_ListFundingSources_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.ListFundingSources(ctx, nil)
				return err
			},
		},
		{
			name:   "create withdrawal destination",
			method: walletv1.WalletInternalService_CreateWithdrawalDestination_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.CreateWithdrawalDestination(ctx, nil)
				return err
			},
		},
		{
			name:   "list withdrawal destinations",
			method: walletv1.WalletInternalService_ListWithdrawalDestinations_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.ListWithdrawalDestinations(ctx, nil)
				return err
			},
		},
		{
			name:   "deactivate withdrawal destination",
			method: walletv1.WalletInternalService_DeactivateWithdrawalDestination_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.DeactivateWithdrawalDestination(ctx, nil)
				return err
			},
		},
		{
			name:   "request ownership verification",
			method: walletv1.WalletInternalService_RequestOwnershipVerification_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.RequestOwnershipVerification(ctx, nil)
				return err
			},
		},
		{
			name:   "complete ownership verification",
			method: walletv1.WalletInternalService_CompleteOwnershipVerification_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.CompleteOwnershipVerification(ctx, nil)
				return err
			},
		},
		{
			name:   "set wallet pin",
			method: walletv1.WalletInternalService_SetWalletPIN_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.SetWalletPIN(ctx, nil)
				return err
			},
		},
		{
			name:   "reset wallet pin",
			method: walletv1.WalletInternalService_ResetWalletPIN_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.ResetWalletPIN(ctx, nil)
				return err
			},
		},
		{
			name:   "enroll user 2fa",
			method: walletv1.WalletInternalService_EnrollUser2FA_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.EnrollUser2FA(ctx, nil)
				return err
			},
		},
		{
			name:   "confirm user 2fa",
			method: walletv1.WalletInternalService_ConfirmUser2FA_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.ConfirmUser2FA(ctx, nil)
				return err
			},
		},
		{
			name:   "disable user 2fa",
			method: walletv1.WalletInternalService_DisableUser2FA_FullMethodName,
			call: func(ctx context.Context) error {
				_, err := server.DisableUser2FA(ctx, nil)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := walletServerMethodContext(context.Background(), tt.method)
			if err := tt.call(ctx); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.PermissionDenied)
			}
		})
	}
}

func TestWalletInternalRPCsValidateAfterAdminAuth(t *testing.T) {
	server := NewServer(&wallet.Service{Store: &walletstore.Store{}})
	ctx := walletServerMethodContext(metadata.NewIncomingContext(context.Background(), adminMetadata()), walletv1.WalletInternalService_RequestDeposit_FullMethodName)

	_, err := server.RequestDeposit(ctx, nil)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("status.Code(err) = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestBindTenantToClaimsValidatesRequestAndGatewayTenant(t *testing.T) {
	claims := &gateway.TokenClaims{TenantID: "tenant-a", UserID: 42}
	tenantID, err := bindTenantToClaims("", claims)
	if err != nil {
		t.Fatalf("bindTenantToClaims() error = %v", err)
	}
	if tenantID != claims.TenantID {
		t.Fatalf("tenantID = %q, want %q", tenantID, claims.TenantID)
	}

	if _, err := bindTenantToClaims("default", claims); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("reserved request tenant code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
	if _, err := bindTenantToClaims("tenant-b", claims); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("mismatched tenant code = %v, want %v", status.Code(err), codes.PermissionDenied)
	}
	if _, err := bindTenantToClaims("tenant-a", &gateway.TokenClaims{TenantID: "default", UserID: 42}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("reserved gateway tenant code = %v, want %v", status.Code(err), codes.Unauthenticated)
	}
}

type walletTestServerTransportStream struct {
	method string
}

func (s walletTestServerTransportStream) Method() string {
	return s.method
}

func (s walletTestServerTransportStream) SetHeader(metadata.MD) error {
	return nil
}

func (s walletTestServerTransportStream) SendHeader(metadata.MD) error {
	return nil
}

func (s walletTestServerTransportStream) SetTrailer(metadata.MD) error {
	return nil
}

func walletServerMethodContext(ctx context.Context, method string) context.Context {
	return grpc.NewContextWithServerTransportStream(ctx, walletTestServerTransportStream{method: method})
}
