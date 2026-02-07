package walletgrpc

import (
	"context"
	"errors"
	"sync"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/wallet"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	walletv1.UnimplementedWalletInternalServiceServer
	walletv1.UnimplementedWalletPublicServiceServer
	Service         *wallet.Service
	TemporalClient  temporalClient
	TemporalOptions walletworker.Options
	temporalOnce    sync.Once
	temporalErr     error
}

func NewServer(service *wallet.Service) *Server {
	return &Server{Service: service}
}

func (s *Server) GetWallet(ctx context.Context, req *walletv1.GetWalletRequest) (*walletv1.Wallet, error) {
	return s.getWallet(ctx, req)
}

func (s *Server) GetWalletPublic(ctx context.Context, req *walletv1.GetWalletRequest) (*walletv1.Wallet, error) {
	return s.getWallet(ctx, req)
}

func (s *Server) EnsureWallet(ctx context.Context, req *walletv1.EnsureWalletRequest) (*walletv1.Wallet, error) {
	return s.ensureWallet(ctx, req)
}

func (s *Server) EnsureWalletPublic(ctx context.Context, req *walletv1.EnsureWalletRequest) (*walletv1.Wallet, error) {
	return s.ensureWallet(ctx, req)
}

func (s *Server) ValidateP2P(ctx context.Context, req *walletv1.ValidateP2PRequest) (*walletv1.ValidateP2PResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTenantID.Error())
	}
	if req.FromWalletId == "" || req.ToWalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	if req.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	if req.Amount <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	fromID, err := uuid.Parse(req.FromWalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	toID, err := uuid.Parse(req.ToWalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}

	validator := walletvalidation.Service{Store: s.Service.Store}
	result, err := validator.ValidateP2P(ctx, walletvalidation.P2PValidationRequest{
		TenantID:        req.TenantId,
		TransactionType: "p2p",
		FromWalletID:    fromID,
		ToWalletID:      toID,
		Currency:        req.Currency,
		Amount:          req.Amount,
	})
	if err != nil {
		if isP2PRejection(err) {
			return &walletv1.ValidateP2PResponse{
				Allowed: false,
				Reason:  err.Error(),
			}, nil
		}
		return nil, mapError(err)
	}

	fee := int64(0)
	if result.Fee != nil {
		fee = result.Fee.TotalFee
	}
	return &walletv1.ValidateP2PResponse{
		Allowed:    true,
		TotalDebit: result.TotalDebit,
		Fee:        fee,
	}, nil
}

func (s *Server) getWallet(ctx context.Context, req *walletv1.GetWalletRequest) (*walletv1.Wallet, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTenantID.Error())
	}
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	w, err := s.Service.GetWallet(ctx, req.TenantId, walletID)
	if err != nil {
		return nil, mapError(err)
	}
	return toWalletProto(w), nil
}

func (s *Server) ensureWallet(ctx context.Context, req *walletv1.EnsureWalletRequest) (*walletv1.Wallet, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingTenantID.Error())
	}
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
	}
	currency := req.Currency
	if currency == "" {
		currency = s.Service.Config.WalletDefaultCurrency
	}
	if currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	w, err := s.Service.EnsureUserWallet(ctx, req.TenantId, req.UserId, currency)
	if err != nil {
		return nil, mapError(err)
	}
	return toWalletProto(w), nil
}

func toWalletProto(w *walletstore.Wallet) *walletv1.Wallet {
	if w == nil {
		return nil
	}
	return &walletv1.Wallet{
		Id:               w.ID.String(),
		TenantId:         w.TenantID,
		OwnerType:        w.OwnerType,
		OwnerId:          w.OwnerID,
		Currency:         w.Currency,
		Balance:          w.Balance,
		AvailableBalance: w.AvailableBalance,
		Status:           w.Status,
		KycTier:          w.KYCTier,
	}
}

func mapError(err error) error {
	switch {
	case errors.Is(err, walletstore.ErrWalletNotFound),
		errors.Is(err, walletstore.ErrPSPConfigNotFound),
		errors.Is(err, walletstore.ErrPSPConfigOverrideNotFound),
		errors.Is(err, walletstore.ErrAdminUserNotFound),
		errors.Is(err, walletstore.ErrAdminRoleNotFound),
		errors.Is(err, walletstore.ErrDestinationNotFound),
		errors.Is(err, walletstore.ErrFundingSourceNotFound),
		errors.Is(err, walletstore.ErrVerificationNotFound),
		errors.Is(err, walletstore.ErrUserTwoFANotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, walletstore.ErrMissingTenantID),
		errors.Is(err, walletstore.ErrMissingCurrency),
		errors.Is(err, walletstore.ErrMissingWalletID),
		errors.Is(err, walletstore.ErrMissingOwnerID),
		errors.Is(err, walletstore.ErrMissingOwnerType),
		errors.Is(err, walletstore.ErrMissingProviderCode),
		errors.Is(err, walletstore.ErrMissingClientReference),
		errors.Is(err, walletstore.ErrMissingPSPTransactionID),
		errors.Is(err, walletstore.ErrMissingDestinationID),
		errors.Is(err, walletstore.ErrMissingDestinationType),
		errors.Is(err, walletstore.ErrMissingDestinationDetails),
		errors.Is(err, walletstore.ErrMissingSourceType),
		errors.Is(err, walletstore.ErrInvalidUserID),
		errors.Is(err, walletstore.ErrInvalidAmount),
		errors.Is(err, walletstore.ErrInvalidWalletPair),
		errors.Is(err, walletstore.ErrMissingIdempotencyKey),
		errors.Is(err, walletstore.ErrInvalidLimit),
		errors.Is(err, walletstore.ErrMissingReferenceType),
		errors.Is(err, walletstore.ErrMissingReferenceID),
		errors.Is(err, walletstore.ErrMissingWalletPIN),
		errors.Is(err, walletstore.ErrInvalidWalletPIN),
		errors.Is(err, walletstore.ErrMissingTwoFACode),
		errors.Is(err, walletstore.ErrInvalidTwoFACode),
		errors.Is(err, walletstore.ErrMissingTwoFASecret),
		errors.Is(err, walletstore.ErrMissingApprovalTimeout),
		errors.Is(err, walletstore.ErrMissingApprovalReason),
		errors.Is(err, walletstore.ErrMissingProofOfPayment),
		errors.Is(err, walletstore.ErrMissingVerificationTimeout),
		errors.Is(err, walletstore.ErrMissingVerificationID),
		errors.Is(err, walletstore.ErrMissingVerificationType),
		errors.Is(err, walletstore.ErrMissingMaxAttempts),
		errors.Is(err, walletstore.ErrMissingHoldExpiry),
		errors.Is(err, walletstore.ErrMissingTransferType),
		errors.Is(err, walletstore.ErrMissingReason),
		errors.Is(err, walletstore.ErrMissingApproverID),
		errors.Is(err, walletstore.ErrMissingWorkflowID),
		errors.Is(err, walletstore.ErrApproverIsRequester):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, wallet.ErrMissingStore),
		errors.Is(err, walletvalidation.ErrMissingStore):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

func isP2PRejection(err error) bool {
	return errors.Is(err, walletvalidation.ErrLimitExceeded) ||
		errors.Is(err, walletvalidation.ErrWalletInactive) ||
		errors.Is(err, walletvalidation.ErrWalletOwnerMismatch) ||
		errors.Is(err, walletstore.ErrInsufficientFunds) ||
		errors.Is(err, walletstore.ErrCurrencyMismatch)
}
