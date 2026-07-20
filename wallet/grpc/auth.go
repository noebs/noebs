package walletgrpc

import (
	"context"
	"strconv"
	"strings"
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/tenantauth"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *Server) claimsFromContext(ctx context.Context) (*gateway.PrincipalIdentity, error) {
	if s == nil || s.Service == nil {
		return nil, status.Error(codes.FailedPrecondition, "missing wallet service")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, nil
	}
	return principalFromMetadata(md, time.Now().UTC())
}

func (s *Server) requireGatewayClaims(ctx context.Context) (*gateway.PrincipalIdentity, error) {
	claims, err := s.claimsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if claims == nil || !isWalletUserPrincipal(*claims) {
		return nil, status.Error(codes.Unauthenticated, "missing or invalid gateway identity")
	}
	return claims, nil
}

func (s *Server) claimsForRPC(ctx context.Context) (*gateway.PrincipalIdentity, error) {
	return s.requireGatewayClaims(ctx)
}

func (s *Server) requirePublicWalletRPC(ctx context.Context) error {
	method, ok := grpc.Method(ctx)
	if !ok {
		return nil
	}
	switch method {
	case walletv1.WalletPublicService_ListFundingSources_FullMethodName,
		walletv1.WalletPublicService_CreateWithdrawalDestination_FullMethodName,
		walletv1.WalletPublicService_ListWithdrawalDestinations_FullMethodName,
		walletv1.WalletPublicService_DeactivateWithdrawalDestination_FullMethodName:
		return nil
	default:
		return status.Error(codes.PermissionDenied, "wallet method is outside the public service")
	}
}

func singleGatewayMetadataValue(md metadata.MD, header string) (string, error) {
	values := md.Get(strings.ToLower(header))
	if len(values) == 0 {
		return "", nil
	}
	if len(values) > 1 {
		return "", status.Error(codes.Unauthenticated, "duplicate gateway identity header")
	}
	return values[0], nil
}

func hasGatewayIdentityValues(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func principalFromMetadata(md metadata.MD, now time.Time) (*gateway.PrincipalIdentity, error) {
	headers := []string{
		gateway.GatewayTenantIDHeader,
		gateway.GatewayIssuerHeader,
		gateway.GatewaySubjectHeader,
		gateway.GatewayOrganizationIDHeader,
		gateway.GatewayAuthorizedPartyHeader,
		gateway.GatewayRolesHeader,
		gateway.GatewayPermissionHeader,
		gateway.GatewayUserIDHeader,
		gateway.GatewaySourceIPHeader,
		gateway.GatewayTokenExpiresAtHeader,
	}
	values := make([]string, len(headers))
	for index, header := range headers {
		value, err := singleGatewayMetadataValue(md, header)
		if err != nil {
			return nil, err
		}
		values[index] = value
	}
	if !hasGatewayIdentityValues(values...) {
		return nil, nil
	}
	principal, err := gateway.ParseInternalPrincipalIdentity(gateway.PrincipalHeaderValues{
		TenantID:        values[0],
		Issuer:          values[1],
		Subject:         values[2],
		OrganizationID:  values[3],
		AuthorizedParty: values[4],
		Roles:           values[5],
		Permission:      values[6],
		UserID:          values[7],
		SourceIP:        values[8],
		TokenExpiresAt:  values[9],
	}, now)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "missing or invalid gateway identity")
	}
	return &principal, nil
}

func isWalletUserPrincipal(principal gateway.PrincipalIdentity) bool {
	return principal.UserID > 0 && principal.HasRole(tenantauth.RoleUser)
}

func bindTenantToClaims(tenantID string, claims *gateway.PrincipalIdentity) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if claims == nil {
		return validateGRPCTenantID(tenantID)
	}
	authenticated, err := validateGRPCTenantID(claims.TenantID)
	if err != nil {
		return "", status.Error(codes.Unauthenticated, "missing tenant in gateway identity")
	}
	tenantID, err = requestTenantID(tenantID, authenticated)
	if err != nil {
		return "", err
	}
	if tenantID != authenticated {
		return "", status.Error(codes.PermissionDenied, "tenant mismatch")
	}
	return authenticated, nil
}

func requestTenantID(requested, authenticated string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return authenticated, nil
	}
	return validateGRPCTenantID(requested)
}

func validateGRPCTenantID(tenantID string) (string, error) {
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return "", status.Error(codes.InvalidArgument, err.Error())
	}
	return tenantID, nil
}

func bindUserIDToClaims(userID int64, claims *gateway.PrincipalIdentity) (int64, error) {
	if claims == nil || !isWalletUserPrincipal(*claims) {
		return userID, nil
	}
	if claims.UserID <= 0 {
		return 0, status.Error(codes.Unauthenticated, "missing user in gateway identity")
	}
	if userID == 0 {
		return claims.UserID, nil
	}
	if userID != claims.UserID {
		return 0, status.Error(codes.PermissionDenied, "user mismatch")
	}
	return claims.UserID, nil
}

func bindOwnerToClaims(ownerType, ownerID string, claims *gateway.PrincipalIdentity) (string, string, error) {
	ownerType = strings.TrimSpace(ownerType)
	ownerID = strings.TrimSpace(ownerID)
	if claims == nil || !isWalletUserPrincipal(*claims) {
		return ownerType, ownerID, nil
	}
	if claims.UserID <= 0 {
		return "", "", status.Error(codes.Unauthenticated, "missing user in gateway identity")
	}
	authenticatedType := walletstore.OwnerTypeUser
	authenticatedID := strconv.FormatInt(claims.UserID, 10)
	if ownerType == "" {
		ownerType = authenticatedType
	}
	if ownerID == "" {
		ownerID = authenticatedID
	}
	if ownerType != authenticatedType || ownerID != authenticatedID {
		return "", "", status.Error(codes.PermissionDenied, "owner mismatch")
	}
	return ownerType, ownerID, nil
}

func walletOwnedByClaims(walletRow *walletstore.Wallet, claims *gateway.PrincipalIdentity) bool {
	if claims == nil || !isWalletUserPrincipal(*claims) {
		return true
	}
	if walletRow == nil || walletRow.TenantID != strings.TrimSpace(claims.TenantID) || walletRow.OwnerType != walletstore.OwnerTypeUser {
		return false
	}
	if walletRow.UserID.Valid {
		return walletRow.UserID.Int64 == claims.UserID
	}
	return walletRow.OwnerID == strconv.FormatInt(claims.UserID, 10)
}

func (s *Server) authorizeWalletForClaims(ctx context.Context, tenantID string, walletID uuid.UUID, claims *gateway.PrincipalIdentity) error {
	if claims == nil || !isWalletUserPrincipal(*claims) {
		return nil
	}
	walletRow, err := s.Service.Store.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return mapError(err)
	}
	if !walletOwnedByClaims(walletRow, claims) {
		return status.Error(codes.NotFound, walletstore.ErrWalletNotFound.Error())
	}
	return nil
}

func (s *Server) authorizeDestinationForClaims(ctx context.Context, tenantID string, destinationID int64, claims *gateway.PrincipalIdentity) error {
	if claims == nil || !isWalletUserPrincipal(*claims) {
		return nil
	}
	dest, err := s.Service.Store.GetWithdrawalDestination(ctx, tenantID, destinationID)
	if err != nil {
		return mapError(err)
	}
	return s.authorizeWalletForClaims(ctx, tenantID, dest.WalletID, claims)
}
