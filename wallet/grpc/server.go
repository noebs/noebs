package walletgrpc

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/groosh"
	"github.com/adonese/noebs/wallet"
	walletmoney "github.com/adonese/noebs/wallet/money"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	walletworker "github.com/adonese/noebs/wallet/worker"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	walletv1.UnimplementedWalletPublicServiceServer
	walletv1.UnimplementedWalletAdminServiceServer
	Service         *wallet.Service
	TemporalClient  temporalClient
	TemporalOptions walletworker.Options
	temporalOnce    sync.Once
	temporalErr     error
}

func NewServer(service *wallet.Service) *Server {
	return &Server{Service: service}
}

func (s *Server) GetWalletPublic(ctx context.Context, req *walletv1.GetWalletPublicRequest) (*walletv1.GetWalletPublicResponse, error) {
	claims, err := s.requireGatewayClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	if req.WalletId == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	walletID, err := uuid.Parse(req.WalletId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingWalletID.Error())
	}
	w, err := s.Service.GetWallet(ctx, tenantID, walletID)
	if err != nil {
		return nil, mapError(err)
	}
	if !walletOwnedByClaims(w, claims) {
		return nil, status.Error(codes.NotFound, walletstore.ErrWalletNotFound.Error())
	}
	response, err := s.walletProto(ctx, w)
	if err != nil {
		return nil, mapError(err)
	}
	return &walletv1.GetWalletPublicResponse{Wallet: response}, nil
}

func (s *Server) EnsureWalletPublic(ctx context.Context, req *walletv1.EnsureWalletPublicRequest) (*walletv1.EnsureWalletPublicResponse, error) {
	claims, err := s.requireGatewayClaims(ctx)
	if err != nil {
		return nil, err
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenantID, err := bindTenantToClaims(req.TenantId, claims)
	if err != nil {
		return nil, err
	}
	userID, err := bindUserIDToClaims(req.UserId, claims)
	if err != nil {
		return nil, err
	}
	boundReq := &walletv1.EnsureWalletPublicRequest{
		TenantId: tenantID,
		UserId:   userID,
		Currency: req.Currency,
	}
	wallet, err := s.ensureWallet(ctx, boundReq)
	if err != nil {
		return nil, err
	}
	return &walletv1.EnsureWalletPublicResponse{Wallet: wallet}, nil
}

func (s *Server) ensureWallet(ctx context.Context, req *walletv1.EnsureWalletPublicRequest) (*walletv1.Wallet, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenantID, err := validateGRPCTenantID(req.TenantId)
	if err != nil {
		return nil, err
	}
	if req.UserId <= 0 {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrInvalidUserID.Error())
	}
	currency := req.Currency
	if currency == "" {
		return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
	}
	existing, err := s.Service.GetUserWalletByCurrency(ctx, tenantID, req.UserId, currency)
	if err == nil {
		response, renderErr := s.walletProto(ctx, existing)
		if renderErr != nil {
			return nil, mapError(renderErr)
		}
		return response, nil
	}
	if !errors.Is(err, walletstore.ErrWalletNotFound) {
		return nil, mapError(err)
	}
	currentCurrency, err := walletmoney.NewService(s.Service.Store).GetCurrency(ctx, currency, time.Now().UTC(), true)
	if err != nil {
		return nil, mapError(err)
	}
	w, err := s.Service.EnsureUserWallet(ctx, wallet.EnsureUserWalletParams{
		TenantID:       tenantID,
		UserID:         req.UserId,
		Currency:       currency,
		CurrencyUnitID: currentCurrency.Unit.VersionID(),
	})
	if err != nil {
		return nil, mapError(err)
	}
	response, err := s.walletProto(ctx, w)
	if err != nil {
		return nil, mapError(err)
	}
	return response, nil
}

func (s *Server) walletProto(ctx context.Context, w *walletstore.Wallet) (*walletv1.Wallet, error) {
	if w == nil {
		return nil, nil
	}
	currency, err := walletmoney.NewService(s.Service.Store).GetCurrencyByUnitID(ctx, w.CurrencyUnitID)
	if err != nil {
		return nil, err
	}
	return walletProtoWithCurrency(w, currency)
}

func walletProtoWithCurrency(w *walletstore.Wallet, currency walletmoney.Currency) (*walletv1.Wallet, error) {
	if w == nil {
		return nil, nil
	}
	if currency.Definition.ID != w.CurrencyUnitID || currency.Definition.CurrencyCode != w.Currency {
		return nil, fmt.Errorf("%w: wallet currency %s/%d does not match unit currency %s/%d", walletmoney.ErrInvalidCurrencyUnitData, w.Currency, w.CurrencyUnitID, currency.Definition.CurrencyCode, currency.Definition.ID)
	}
	balance, err := groosh.NewMoney(w.Balance, currency.Unit)
	if err != nil {
		return nil, err
	}
	availableBalance, err := groosh.NewMoney(w.AvailableBalance, currency.Unit)
	if err != nil {
		return nil, err
	}
	balanceMoney, err := moneyAmountProto(balance, groosh.RoundHalfEven)
	if err != nil {
		return nil, err
	}
	availableBalanceMoney, err := moneyAmountProto(availableBalance, groosh.RoundHalfEven)
	if err != nil {
		return nil, err
	}
	return &walletv1.Wallet{
		Id:                    w.ID.String(),
		TenantId:              w.TenantID,
		OwnerType:             w.OwnerType,
		OwnerId:               w.OwnerID,
		Currency:              w.Currency,
		Balance:               w.Balance,
		AvailableBalance:      w.AvailableBalance,
		Status:                w.Status,
		KycTier:               w.KYCTier,
		CreatedAt:             w.CreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:             w.UpdatedAt.Format(time.RFC3339Nano),
		BalanceMoney:          balanceMoney,
		AvailableBalanceMoney: availableBalanceMoney,
	}, nil
}

func mapError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, walletstore.ErrConversionQuoteLimitExceeded):
		return status.Error(codes.ResourceExhausted, err.Error())
	case errors.Is(err, walletstore.ErrWalletNotFound),
		errors.Is(err, walletstore.ErrDepositIntentNotFound),
		errors.Is(err, walletstore.ErrPSPTransactionNotFound),
		errors.Is(err, walletstore.ErrPSPConfigNotFound),
		errors.Is(err, walletstore.ErrPSPConfigOverrideNotFound),
		errors.Is(err, walletstore.ErrTransactionLimitNotFound),
		errors.Is(err, walletstore.ErrOperatorIdentityNotFound),
		errors.Is(err, walletstore.ErrDestinationNotFound),
		errors.Is(err, walletstore.ErrFundingSourceNotFound),
		errors.Is(err, walletstore.ErrFundingSourceReservationNotFound),
		errors.Is(err, walletstore.ErrLimitReservationNotFound),
		errors.Is(err, walletstore.ErrLedgerEntryNotFound),
		errors.Is(err, walletstore.ErrManualTransferNotFound),
		errors.Is(err, walletstore.ErrFeeConfigNotFound),
		errors.Is(err, walletstore.ErrExchangeRateNotFound),
		errors.Is(err, walletstore.ErrCurrencyNotFound),
		errors.Is(err, walletstore.ErrFXSourceNotFound),
		errors.Is(err, walletstore.ErrFXObservationNotFound),
		errors.Is(err, walletstore.ErrConversionQuoteNotFound),
		errors.Is(err, walletstore.ErrInactiveCurrency):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, walletstore.ErrDuplicateTransaction),
		errors.Is(err, walletstore.ErrDuplicateWallet),
		errors.Is(err, walletstore.ErrDuplicateHold),
		errors.Is(err, walletstore.ErrDuplicateFundingSource),
		errors.Is(err, walletstore.ErrDuplicateFundingSourceReservation),
		errors.Is(err, walletstore.ErrDuplicateFundingLink),
		errors.Is(err, walletstore.ErrDuplicateDestinationLink),
		errors.Is(err, walletstore.ErrDuplicateAmount),
		errors.Is(err, walletstore.ErrDuplicateManualTransfer),
		errors.Is(err, walletstore.ErrDuplicateManualApproval),
		errors.Is(err, walletstore.ErrDuplicateP2PCommand),
		errors.Is(err, walletstore.ErrDuplicateDepositIntent),
		errors.Is(err, walletstore.ErrDuplicateLimitReservation),
		errors.Is(err, walletstore.ErrWorkflowDecisionConflict),
		errors.Is(err, walletstore.ErrFXObservationConflict),
		errors.Is(err, walletstore.ErrConversionQuoteIdempotencyConflict):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, walletstore.ErrMissingTenantID),
		errors.Is(err, walletstore.ErrInvalidTenantID),
		errors.Is(err, walletstore.ErrMissingOperatorID),
		errors.Is(err, walletstore.ErrMissingOperatorIssuer),
		errors.Is(err, walletstore.ErrInvalidOperatorIssuer),
		errors.Is(err, walletstore.ErrMissingOperatorSubject),
		errors.Is(err, walletstore.ErrInvalidOperatorSubject),
		errors.Is(err, walletstore.ErrMissingCurrency),
		errors.Is(err, walletstore.ErrInvalidCurrency),
		errors.Is(err, walletstore.ErrMissingWalletID),
		errors.Is(err, walletstore.ErrMissingOwnerID),
		errors.Is(err, walletstore.ErrMissingOwnerType),
		errors.Is(err, walletstore.ErrInvalidOwnerType),
		errors.Is(err, walletstore.ErrMissingProviderCode),
		errors.Is(err, walletstore.ErrMissingClientReference),
		errors.Is(err, walletstore.ErrMissingPSPTransactionID),
		errors.Is(err, walletstore.ErrMissingDepositIntentID),
		errors.Is(err, walletstore.ErrInvalidDepositIntentID),
		errors.Is(err, walletstore.ErrInvalidDepositIntent),
		errors.Is(err, walletstore.ErrMissingDirection),
		errors.Is(err, walletstore.ErrInvalidDirection),
		errors.Is(err, walletstore.ErrInvalidRegion),
		errors.Is(err, walletstore.ErrMissingDestinationID),
		errors.Is(err, walletstore.ErrInvalidDestinationID),
		errors.Is(err, walletstore.ErrMissingDestinationType),
		errors.Is(err, walletstore.ErrMissingDestinationDetails),
		errors.Is(err, walletstore.ErrMissingSourceType),
		errors.Is(err, walletstore.ErrMissingFundingSourceID),
		errors.Is(err, walletstore.ErrMissingFundingSourceReservation),
		errors.Is(err, walletstore.ErrMissingLedgerEntryID),
		errors.Is(err, walletstore.ErrInvalidUserID),
		errors.Is(err, walletstore.ErrInvalidAmount),
		errors.Is(err, walletstore.ErrAmountOverflow),
		errors.Is(err, walletstore.ErrInvalidWalletPair),
		errors.Is(err, walletstore.ErrMissingIdempotencyKey),
		errors.Is(err, walletstore.ErrInvalidIdempotencyKey),
		errors.Is(err, walletstore.ErrInvalidLimit),
		errors.Is(err, walletstore.ErrInvalidOffset),
		errors.Is(err, walletstore.ErrMissingReferenceType),
		errors.Is(err, walletstore.ErrMissingReferenceID),
		errors.Is(err, walletstore.ErrMissingApprovalTimeout),
		errors.Is(err, walletstore.ErrInvalidApprovalTimeout),
		errors.Is(err, walletstore.ErrMissingApprovalPolicy),
		errors.Is(err, walletstore.ErrApprovalNotRequired),
		errors.Is(err, walletstore.ErrMissingApprovalReason),
		errors.Is(err, walletstore.ErrMissingApprovalTime),
		errors.Is(err, walletstore.ErrMissingCompletionTime),
		errors.Is(err, walletstore.ErrMissingProofOfPayment),
		errors.Is(err, walletstore.ErrMissingStatusTimeout),
		errors.Is(err, walletstore.ErrMissingVerificationTime),
		errors.Is(err, walletstore.ErrInvalidVerificationTime),
		errors.Is(err, walletstore.ErrMissingHoldExpiry),
		errors.Is(err, walletstore.ErrInvalidHoldExpiry),
		errors.Is(err, walletstore.ErrMissingReturnToSourcePolicy),
		errors.Is(err, walletstore.ErrMissingTransferType),
		errors.Is(err, walletstore.ErrInvalidTransferType),
		errors.Is(err, walletstore.ErrInvalidStatus),
		errors.Is(err, walletstore.ErrInvalidDecision),
		errors.Is(err, walletstore.ErrMissingDecisionKind),
		errors.Is(err, walletstore.ErrInvalidDecisionKind),
		errors.Is(err, walletstore.ErrMissingDecisionSubject),
		errors.Is(err, walletstore.ErrMissingSourceDetails),
		errors.Is(err, walletstore.ErrMissingTransactionType),
		errors.Is(err, walletstore.ErrMissingLimitCommandID),
		errors.Is(err, walletstore.ErrMissingSettlementTransfers),
		errors.Is(err, walletstore.ErrInvalidLedgerTransactionID),
		errors.Is(err, walletstore.ErrMissingBaseCurrency),
		errors.Is(err, walletstore.ErrMissingQuoteCurrency),
		errors.Is(err, walletstore.ErrMissingDecision),
		errors.Is(err, walletstore.ErrMissingReason),
		errors.Is(err, walletstore.ErrMissingRequesterID),
		errors.Is(err, walletstore.ErrMissingApproverID),
		errors.Is(err, walletstore.ErrMissingWorkflowID),
		errors.Is(err, walletstore.ErrInvalidWorkflowID),
		errors.Is(err, walletstore.ErrMissingWorkflowRunID),
		errors.Is(err, walletstore.ErrInvalidWorkflowRunID),
		errors.Is(err, walletstore.ErrMissingP2PCommand),
		errors.Is(err, walletstore.ErrInvalidP2PCommand),
		errors.Is(err, walletstore.ErrMissingInteractionType),
		errors.Is(err, walletstore.ErrInvalidPercentage),
		errors.Is(err, walletstore.ErrFeePercentageNotRepresentable),
		errors.Is(err, walletstore.ErrInvalidRate),
		errors.Is(err, walletstore.ErrRateNotRepresentable),
		errors.Is(err, walletstore.ErrLegacyRateNotRepresentable),
		errors.Is(err, walletstore.ErrSpreadNotRepresentable),
		errors.Is(err, walletstore.ErrPSPFXRateNotRepresentable),
		errors.Is(err, walletstore.ErrMissingFXRateFraction),
		errors.Is(err, walletstore.ErrInvalidFXRateFraction),
		errors.Is(err, walletstore.ErrPSPFXRateFractionNotRepresentable),
		errors.Is(err, walletstore.ErrMissingFXConversionTime),
		errors.Is(err, walletstore.ErrInvalidFXConversionTime),
		errors.Is(err, walletstore.ErrPSPFXProvenanceMismatch),
		errors.Is(err, walletstore.ErrMissingCurrencyUnitID),
		errors.Is(err, walletstore.ErrInvalidCurrencyUnitID),
		errors.Is(err, walletstore.ErrIdenticalCurrencies),
		errors.Is(err, walletstore.ErrMissingFXSource),
		errors.Is(err, walletstore.ErrInvalidFXSource),
		errors.Is(err, walletstore.ErrMissingFXSourcePair),
		errors.Is(err, walletstore.ErrMissingFXObservation),
		errors.Is(err, walletstore.ErrMissingObservationTime),
		errors.Is(err, walletstore.ErrMissingRetrievalTime),
		errors.Is(err, walletstore.ErrMissingExpiryTime),
		errors.Is(err, walletstore.ErrMissingPayloadHash),
		errors.Is(err, walletstore.ErrInvalidPayloadHash),
		errors.Is(err, walletstore.ErrMissingSourceRevision),
		errors.Is(err, walletstore.ErrMissingRateSide),
		errors.Is(err, walletstore.ErrInvalidRateSide),
		errors.Is(err, walletstore.ErrMissingRatePurpose),
		errors.Is(err, walletstore.ErrInvalidRatePurpose),
		errors.Is(err, walletstore.ErrMissingRoundingMode),
		errors.Is(err, walletstore.ErrInvalidRoundingMode),
		errors.Is(err, walletstore.ErrMissingConversionQuoteID),
		errors.Is(err, groosh.ErrInvalidCurrencyCode),
		errors.Is(err, groosh.ErrInvalidAmount),
		errors.Is(err, groosh.ErrInvalidAmountSyntax),
		errors.Is(err, groosh.ErrInexactAmount),
		errors.Is(err, groosh.ErrInvalidCanonicalMoney),
		errors.Is(err, groosh.ErrCurrencyMismatch),
		errors.Is(err, groosh.ErrUnitVersionMismatch),
		errors.Is(err, groosh.ErrOverflow),
		errors.Is(err, groosh.ErrInvalidRate),
		errors.Is(err, groosh.ErrInvalidRoundingMode),
		errors.Is(err, walletstore.ErrInvalidUsageTime),
		errors.Is(err, walletstore.ErrCurrencyMismatch),
		errors.Is(err, walletvalidation.ErrWalletOwnerMismatch),
		errors.Is(err, walletvalidation.ErrFeeExceedsAmount),
		errors.Is(err, walletstore.ErrFundingSourceNotVerified),
		errors.Is(err, walletstore.ErrDestinationNotVerified),
		errors.Is(err, walletstore.ErrFundingSourceNotWithdrawable),
		errors.Is(err, walletstore.ErrFundingSourceLimitExceeded),
		errors.Is(err, walletstore.ErrApproverIsRequester),
		errors.Is(err, walletvalidation.ErrPSPCurrencyInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, wallet.ErrMissingStore),
		errors.Is(err, walletmoney.ErrMissingRepository),
		errors.Is(err, walletstore.ErrMissingStore),
		errors.Is(err, walletvalidation.ErrMissingStore),
		errors.Is(err, walletvalidation.ErrPSPConfigDisabled),
		errors.Is(err, walletvalidation.ErrPSPConfigMissingCurrencies),
		errors.Is(err, walletvalidation.ErrPSPDirectionInvalid),
		errors.Is(err, walletstore.ErrWalletInactive),
		errors.Is(err, walletvalidation.ErrWalletInactive),
		errors.Is(err, walletstore.ErrInsufficientFunds),
		errors.Is(err, walletstore.ErrTransactionLimitExceeded),
		errors.Is(err, walletstore.ErrLimitReservationReleased),
		errors.Is(err, walletstore.ErrLimitReservationConsumed),
		errors.Is(err, walletstore.ErrWorkflowDecisionWindowClosed),
		errors.Is(err, walletstore.ErrInvalidStatusTransition),
		errors.Is(err, walletstore.ErrCurrencyUnitTransitionUnsupported):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, walletstore.ErrStaleFXObservation),
		errors.Is(err, walletstore.ErrCurrencyScaleUnavailable),
		errors.Is(err, groosh.ErrMissingISOMinorExponent):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		slog.Error("wallet gRPC internal error", "error", err)
		return status.Error(codes.Internal, "internal wallet error")
	}
}
