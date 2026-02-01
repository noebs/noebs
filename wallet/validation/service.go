package validation

import (
	"context"

	walletfees "github.com/adonese/noebs/wallet/fees"
	walletlimits "github.com/adonese/noebs/wallet/limits"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
)

type P2PValidationRequest struct {
	TenantID        string
	TransactionType string
	FromWalletID    uuid.UUID
	ToWalletID      uuid.UUID
	Currency        string
	Amount          int64
	FromOwnerType   string
	FromOwnerID     string
	ToOwnerType     string
	ToOwnerID       string
}

type P2PValidationResult struct {
	FromWalletID uuid.UUID
	ToWalletID   uuid.UUID
	Currency     string
	Amount       int64
	Fee          *walletfees.FeeResult
	TotalDebit   int64
	Limits       *walletlimits.CheckResult
}

type P2PRule func(ctx context.Context, req P2PValidationRequest, fromWallet, toWallet *walletstore.Wallet) error

type Service struct {
	Store    *walletstore.Store
	P2PRules []P2PRule
}

func ValidateP2PRequest(req P2PValidationRequest) error {
	if req.TenantID == "" {
		return walletstore.ErrMissingTenantID
	}
	if req.TransactionType == "" {
		return walletstore.ErrMissingTransactionType
	}
	if req.Currency == "" {
		return walletstore.ErrMissingCurrency
	}
	if req.FromWalletID == uuid.Nil || req.ToWalletID == uuid.Nil {
		return walletstore.ErrMissingWalletID
	}
	if req.FromWalletID == req.ToWalletID {
		return walletstore.ErrInvalidWalletPair
	}
	if req.Amount <= 0 {
		return walletstore.ErrInvalidAmount
	}
	return nil
}

func (s *Service) ValidateP2P(ctx context.Context, req P2PValidationRequest) (*P2PValidationResult, error) {
	if s == nil || s.Store == nil {
		return nil, ErrMissingStore
	}
	if err := ValidateP2PRequest(req); err != nil {
		return nil, err
	}

	fromWallet, err := s.Store.GetWallet(ctx, req.TenantID, req.FromWalletID)
	if err != nil {
		return nil, err
	}
	toWallet, err := s.Store.GetWallet(ctx, req.TenantID, req.ToWalletID)
	if err != nil {
		return nil, err
	}
	if fromWallet.Status != "active" || toWallet.Status != "active" {
		return nil, ErrWalletInactive
	}
	if fromWallet.Currency != req.Currency || toWallet.Currency != req.Currency {
		return nil, walletstore.ErrCurrencyMismatch
	}
	if req.FromOwnerType != "" && fromWallet.OwnerType != req.FromOwnerType {
		return nil, ErrWalletOwnerMismatch
	}
	if req.FromOwnerID != "" && fromWallet.OwnerID != req.FromOwnerID {
		return nil, ErrWalletOwnerMismatch
	}
	if req.ToOwnerType != "" && toWallet.OwnerType != req.ToOwnerType {
		return nil, ErrWalletOwnerMismatch
	}
	if req.ToOwnerID != "" && toWallet.OwnerID != req.ToOwnerID {
		return nil, ErrWalletOwnerMismatch
	}

	feeEngine := walletfees.FeeEngine{Store: s.Store}
	feeResult, err := feeEngine.Calculate(ctx, req.TenantID, req.TransactionType, req.Currency, req.Amount)
	if err != nil {
		return nil, err
	}
	totalDebit := req.Amount + feeResult.TotalFee
	if fromWallet.AvailableBalance < totalDebit {
		return nil, walletstore.ErrInsufficientFunds
	}

	limitEnforcer := walletlimits.Enforcer{Store: s.Store}
	limitResult, err := limitEnforcer.Check(ctx, req.TenantID, req.FromWalletID, req.TransactionType, req.Currency, req.Amount)
	if err != nil {
		return nil, err
	}
	if !limitResult.Allowed {
		return nil, LimitExceededError{Reason: limitResult.Reason}
	}

	for _, rule := range s.P2PRules {
		if rule == nil {
			continue
		}
		if err := rule(ctx, req, fromWallet, toWallet); err != nil {
			return nil, err
		}
	}

	return &P2PValidationResult{
		FromWalletID: req.FromWalletID,
		ToWalletID:   req.ToWalletID,
		Currency:     req.Currency,
		Amount:       req.Amount,
		Fee:          feeResult,
		TotalDebit:   totalDebit,
		Limits:       limitResult,
	}, nil
}
