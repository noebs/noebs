package walletgrpc

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	walletstore "github.com/adonese/noebs/wallet/store"
	walletvalidation "github.com/adonese/noebs/wallet/validation"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) PreviewP2PTransfer(ctx context.Context, r *walletv1.PreviewP2PTransferRequest) (*walletv1.P2PPreview, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	from, err := uuid.Parse(r.FromWalletId)
	if err != nil || from == uuid.Nil {
		return nil, status.Error(codes.InvalidArgument, "invalid from_wallet_id")
	}
	to, err := uuid.Parse(r.ToWalletId)
	if err != nil || to == uuid.Nil || to == from {
		return nil, status.Error(codes.InvalidArgument, "invalid to_wallet_id")
	}
	if r.Amount <= 0 || r.Currency == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid amount or currency")
	}
	sender, err := s.Service.Store.GetP2PRecipient(ctx, tenant, from)
	if err != nil {
		return nil, mapError(err)
	}
	if sender.OwnerID != owner {
		return nil, status.Error(codes.NotFound, "wallet not found")
	}
	recipient, err := s.Service.Store.GetP2PRecipient(ctx, tenant, to)
	if err != nil {
		return nil, mapError(err)
	}
	validator := walletvalidation.Service{Store: s.Service.Store}
	result, err := validator.ValidateP2P(ctx, walletvalidation.P2PValidationRequest{TenantID: tenant, TransactionType: "p2p", FromWalletID: from, ToWalletID: to, Currency: r.Currency, Amount: r.Amount, FromOwnerType: "user", FromOwnerID: owner, ToOwnerType: "user", ToOwnerID: recipient.OwnerID})
	if err != nil {
		return nil, mapError(err)
	}
	w, err := s.Service.Store.GetWallet(ctx, tenant, from)
	if err != nil {
		return nil, mapError(err)
	}
	limits, err := s.Service.Store.GetLimits(ctx, tenant, w.KYCTier, "p2p", r.Currency, result.CurrencyUnitID)
	if err != nil {
		return nil, mapError(err)
	}
	if r.Amount > limits.PerTransactionLimit {
		return nil, status.Error(codes.FailedPrecondition, "transaction_limit_exceeded")
	}
	return &walletv1.P2PPreview{FromWalletId: from.String(), ToWalletId: to.String(), ToOwnerType: "user", ToOwnerId: recipient.OwnerID, AmountMinor: strconv.FormatInt(r.Amount, 10), FeeAmountMinor: strconv.FormatInt(result.Fee.TotalFee, 10), TotalDebitMinor: strconv.FormatInt(result.TotalDebit, 10), Currency: r.Currency, CurrencyUnitVersion: strconv.FormatInt(result.CurrencyUnitID, 10)}, nil
}

func (s *Server) GetP2PTransferStatus(ctx context.Context, r *walletv1.GetP2PTransferStatusRequest) (*walletv1.P2PTransferStatus, error) {
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "missing request")
	}
	tenant, owner, err := s.walletIdentity(ctx, r.TenantId)
	if err != nil {
		return nil, err
	}
	receipt, err := s.Service.Store.GetP2PReceipt(ctx, tenant, owner, r.IdempotencyKey)
	if errors.Is(err, walletstore.ErrP2PCommandNotFound) {
		return nil, status.Error(codes.NotFound, "p2p_not_found")
	}
	if err != nil {
		return nil, mapError(err)
	}
	p, c := receipt.Payload, receipt.Command
	if receipt.Fee < 0 || p.Amount > math.MaxInt64-receipt.Fee {
		return nil, mapError(walletstore.ErrAmountOverflow)
	}
	response := &walletv1.P2PTransferStatus{Status: receipt.Status, IdempotencyKey: c.IdempotencyKey, ReferenceId: p.ReferenceID, FromWalletId: c.FromWalletID.String(), ToWalletId: c.ToWalletID.String(), ToOwnerId: c.ToOwnerID, AmountMinor: strconv.FormatInt(p.Amount, 10), FeeAmountMinor: strconv.FormatInt(receipt.Fee, 10), TotalDebitMinor: strconv.FormatInt(p.Amount+receipt.Fee, 10), Currency: p.Currency, CurrencyUnitVersion: strconv.FormatInt(receipt.CurrencyUnitID, 10), ErrorCode: receipt.ErrorCode, CreatedAt: receipt.CreatedAt.UTC().Format(time.RFC3339Nano)}
	response.LifecycleStatus, response.Substatus = receipt.LifecycleStatus, receipt.Substatus
	if receipt.TransactionID > 0 {
		response.TransactionId = strconv.FormatInt(receipt.TransactionID, 10)
	}
	return response, nil
}
