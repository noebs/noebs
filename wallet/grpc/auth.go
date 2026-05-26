package walletgrpc

import (
	"context"
	"strconv"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func (s *Server) claimsFromContext(ctx context.Context) (*gateway.TokenClaims, error) {
	if s == nil || s.Service == nil {
		return nil, status.Error(codes.FailedPrecondition, "missing wallet service")
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, nil
	}
	for _, header := range md.Get("authorization") {
		token := bearerToken(header)
		if token == "" {
			continue
		}
		auth := gateway.JWTAuth{NoebsConfig: s.Service.Config}
		auth.Init()
		claims, err := auth.VerifyJWT(token)
		if err != nil {
			return nil, status.Error(codes.Unauthenticated, "missing or invalid authorization token")
		}
		return claims, nil
	}
	return nil, nil
}

func (s *Server) requireJWTClaims(ctx context.Context) (*gateway.TokenClaims, error) {
	claims, err := s.claimsFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if claims == nil {
		return nil, status.Error(codes.Unauthenticated, "missing or invalid authorization token")
	}
	return claims, nil
}

func bearerToken(header string) string {
	header = strings.TrimSpace(header)
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func bindTenantToClaims(tenantID string, claims *gateway.TokenClaims) (string, error) {
	tenantID = strings.TrimSpace(tenantID)
	if claims == nil {
		return validateGRPCTenantID(tenantID)
	}
	authenticated, err := validateGRPCTenantID(claims.TenantID)
	if err != nil {
		return "", status.Error(codes.Unauthenticated, "missing tenant in authorization token")
	}
	if tenantID == "" {
		return authenticated, nil
	}
	tenantID, err = validateGRPCTenantID(tenantID)
	if err != nil {
		return "", err
	}
	if tenantID != authenticated {
		return "", status.Error(codes.PermissionDenied, "tenant mismatch")
	}
	return authenticated, nil
}

func validateGRPCTenantID(tenantID string) (string, error) {
	tenantID, err := walletstore.ValidateTenantID(tenantID)
	if err != nil {
		return "", status.Error(codes.InvalidArgument, err.Error())
	}
	return tenantID, nil
}

func bindUserIDToClaims(userID int64, claims *gateway.TokenClaims) (int64, error) {
	if claims == nil {
		return userID, nil
	}
	if claims.UserID <= 0 {
		return 0, status.Error(codes.Unauthenticated, "missing user in authorization token")
	}
	if userID == 0 {
		return claims.UserID, nil
	}
	if userID != claims.UserID {
		return 0, status.Error(codes.PermissionDenied, "user mismatch")
	}
	return claims.UserID, nil
}

func bindOwnerToClaims(ownerType, ownerID string, claims *gateway.TokenClaims) (string, string, error) {
	ownerType = strings.TrimSpace(ownerType)
	ownerID = strings.TrimSpace(ownerID)
	if claims == nil {
		return ownerType, ownerID, nil
	}
	if claims.UserID <= 0 {
		return "", "", status.Error(codes.Unauthenticated, "missing user in authorization token")
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

func walletOwnedByClaims(walletRow *walletstore.Wallet, claims *gateway.TokenClaims) bool {
	if claims == nil {
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

func (s *Server) authorizeWalletForClaims(ctx context.Context, tenantID string, walletID uuid.UUID, claims *gateway.TokenClaims) error {
	if claims == nil {
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

func (s *Server) authorizeDestinationForClaims(ctx context.Context, tenantID string, destinationID int64, claims *gateway.TokenClaims) error {
	if claims == nil {
		return nil
	}
	dest, err := s.Service.Store.GetWithdrawalDestination(ctx, tenantID, destinationID)
	if err != nil {
		return mapError(err)
	}
	return s.authorizeWalletForClaims(ctx, tenantID, dest.WalletID, claims)
}

func (s *Server) authorizeVerificationForClaims(ctx context.Context, tenantID string, verificationID int64, claims *gateway.TokenClaims) (*walletstore.OwnershipVerification, error) {
	if claims == nil {
		return nil, nil
	}
	verification, err := s.Service.Store.GetOwnershipVerification(ctx, tenantID, verificationID)
	if err != nil {
		return nil, mapError(err)
	}
	if err := s.authorizeDestinationForClaims(ctx, tenantID, verification.DestinationID, claims); err != nil {
		return nil, err
	}
	return verification, nil
}
