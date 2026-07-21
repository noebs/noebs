package walletgrpc

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/groosh"
	"github.com/adonese/noebs/wallet"
	walletmoney "github.com/adonese/noebs/wallet/money"
	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	publicQueryDefaultLimit = 100
	publicQueryMaxLimit     = 500
	publicQueryMaxOffset    = 100_000
)

func (s *Server) ListPaymentMethodsPublic(ctx context.Context, req *walletv1.ListPaymentMethodsPublicRequest) (*walletv1.ListPaymentMethodsPublicResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
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
	amount, err := publicNonNegativeAmount(req.Amount)
	if err != nil {
		return nil, err
	}
	currencyUnitID := int64(0)
	var scopedCurrency *walletmoney.Currency
	if req.Currency == "" {
		if amount > 0 {
			return nil, status.Error(codes.InvalidArgument, walletstore.ErrMissingCurrency.Error())
		}
	} else {
		if _, err := walletstore.ValidateCurrencyCode(req.Currency); err != nil {
			return nil, mapError(err)
		}
		currency, err := walletmoney.NewService(s.Service.Store).GetCurrency(ctx, req.Currency, time.Now().UTC(), true)
		if err != nil {
			return nil, mapError(err)
		}
		currencyUnitID = currency.Definition.ID
		scopedCurrency = &currency
	}
	limit, offset, err := publicLimitOffset(req.Limit, req.Offset, publicQueryDefaultLimit)
	if err != nil {
		return nil, err
	}
	methods, err := s.Service.Store.ListAvailablePSPMethods(ctx, walletstore.PSPMethodFilter{
		TenantID:       tenantID,
		Direction:      req.Direction,
		Currency:       req.Currency,
		CurrencyUnitID: currencyUnitID,
		Region:         req.Region,
		Amount:         amount,
		Limit:          limit,
		Offset:         offset,
	})
	if err != nil {
		return nil, mapError(err)
	}
	resp := make([]*walletv1.PaymentMethod, 0, len(methods))
	for _, method := range methods {
		mapped, err := paymentMethodProto(method, scopedCurrency)
		if err != nil {
			return nil, mapError(err)
		}
		resp = append(resp, mapped)
	}
	return &walletv1.ListPaymentMethodsPublicResponse{Methods: resp}, nil
}

func (s *Server) ListWalletTransactionsPublic(ctx context.Context, req *walletv1.ListWalletTransactionsPublicRequest) (*walletv1.ListWalletTransactionsPublicResponse, error) {
	if s == nil || s.Service == nil || s.Service.Store == nil {
		return nil, status.Error(codes.FailedPrecondition, wallet.ErrMissingStore.Error())
	}
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
	limit, offset, err := publicLimitOffset(req.Limit, req.Offset, publicQueryDefaultLimit)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeWalletForClaims(ctx, tenantID, walletID, claims); err != nil {
		return nil, err
	}
	entries, err := s.Service.Store.ListWalletLedgerEntries(ctx, walletstore.WalletLedgerEntryFilter{
		TenantID:  tenantID,
		WalletID:  walletID,
		EntryType: req.EntryType,
		Limit:     limit,
		Offset:    offset,
	})
	if err != nil {
		return nil, mapError(err)
	}
	resp := make([]*walletv1.WalletLedgerEntry, 0, len(entries))
	currencies := make(map[int64]walletmoney.Currency)
	moneyService := walletmoney.NewService(s.Service.Store)
	for _, entry := range entries {
		currency, ok := currencies[entry.CurrencyUnitID]
		if !ok {
			currency, err = moneyService.GetCurrencyByUnitID(ctx, entry.CurrencyUnitID)
			if err != nil {
				return nil, mapError(err)
			}
			currencies[entry.CurrencyUnitID] = currency
		}
		protoEntry, err := walletLedgerEntryProto(entry, currency)
		if err != nil {
			return nil, mapError(err)
		}
		resp = append(resp, protoEntry)
	}
	return &walletv1.ListWalletTransactionsPublicResponse{Transactions: resp}, nil
}

func publicLimitOffset(reqLimit, reqOffset int32, defaultLimit int) (int, int, error) {
	if defaultLimit <= 0 || defaultLimit > publicQueryMaxLimit {
		return 0, 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidLimit.Error())
	}
	if reqLimit < 0 {
		return 0, 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidLimit.Error())
	}
	if reqOffset < 0 {
		return 0, 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidOffset.Error())
	}
	limit := int(reqLimit)
	if limit == 0 {
		limit = defaultLimit
	}
	if limit > publicQueryMaxLimit {
		return 0, 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidLimit.Error())
	}
	if reqOffset > publicQueryMaxOffset {
		return 0, 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidOffset.Error())
	}
	return limit, int(reqOffset), nil
}

func publicNonNegativeAmount(amount int64) (int64, error) {
	if amount < 0 {
		return 0, status.Error(codes.InvalidArgument, walletstore.ErrInvalidAmount.Error())
	}
	return amount, nil
}

func paymentMethodProto(method walletstore.PSPPaymentMethod, currency *walletmoney.Currency) (*walletv1.PaymentMethod, error) {
	response := &walletv1.PaymentMethod{
		ProviderCode:          method.ProviderCode,
		ProviderName:          method.ProviderName,
		DisplayName:           method.DisplayName,
		MethodType:            method.MethodType,
		Direction:             method.Direction,
		Currencies:            method.Currencies,
		Regions:               method.Regions,
		MinAmount:             optionalInt64(method.MinAmount),
		MaxAmount:             optionalInt64(method.MaxAmount),
		InputSchemaJson:       string(method.InputSchema),
		PresentationJson:      string(method.Presentation),
		SupportsDeposit:       method.SupportsDeposit,
		SupportsWithdrawal:    method.SupportsWithdraw,
		CurrencyUnitVersionId: method.CurrencyUnitID,
	}
	hasBounds := method.MinAmount.Valid || method.MaxAmount.Valid
	if (method.CurrencyUnitID != 0 || hasBounds) && !paymentMethodCurrencyMatches(method, currency) {
		return nil, fmt.Errorf("%w: payment method money identity does not match unit %d", walletmoney.ErrInvalidCurrencyUnitData, method.CurrencyUnitID)
	}
	if !hasBounds {
		return response, nil
	}
	if method.MinAmount.Valid {
		amount, err := groosh.NewMoney(method.MinAmount.Int64, currency.Unit)
		if err != nil {
			return nil, err
		}
		response.MinAmountMoney, err = moneyAmountProto(amount, groosh.RoundHalfEven)
		if err != nil {
			return nil, err
		}
	}
	if method.MaxAmount.Valid {
		amount, err := groosh.NewMoney(method.MaxAmount.Int64, currency.Unit)
		if err != nil {
			return nil, err
		}
		response.MaxAmountMoney, err = moneyAmountProto(amount, groosh.RoundHalfEven)
		if err != nil {
			return nil, err
		}
	}
	return response, nil
}

func paymentMethodCurrencyMatches(method walletstore.PSPPaymentMethod, currency *walletmoney.Currency) bool {
	return currency != nil && method.CurrencyUnitID > 0 &&
		currency.Definition.ID == method.CurrencyUnitID &&
		currency.Unit.VersionID() == method.CurrencyUnitID &&
		currency.Definition.CurrencyCode != "" &&
		currency.Unit.Code() == currency.Definition.CurrencyCode &&
		len(method.Currencies) == 1 && method.Currencies[0] == currency.Definition.CurrencyCode
}

func walletLedgerEntryProto(entry walletstore.WalletLedgerEntry, currency walletmoney.Currency) (*walletv1.WalletLedgerEntry, error) {
	if currency.Definition.ID != entry.CurrencyUnitID || currency.Definition.CurrencyCode != entry.Currency {
		return nil, fmt.Errorf("%w: ledger currency %s/%d does not match unit %s/%d", walletmoney.ErrInvalidCurrencyUnitData, entry.Currency, entry.CurrencyUnitID, currency.Definition.CurrencyCode, currency.Definition.ID)
	}
	amount, err := groosh.NewMoney(entry.Amount, currency.Unit)
	if err != nil {
		return nil, err
	}
	balanceAfter, err := groosh.NewMoney(entry.BalanceAfter, currency.Unit)
	if err != nil {
		return nil, err
	}
	amountMoney, err := moneyAmountProto(amount, groosh.RoundHalfEven)
	if err != nil {
		return nil, err
	}
	balanceAfterMoney, err := moneyAmountProto(balanceAfter, groosh.RoundHalfEven)
	if err != nil {
		return nil, err
	}
	return &walletv1.WalletLedgerEntry{
		Id:                entry.ID,
		TenantId:          entry.TenantID,
		TransactionId:     entry.TransactionID,
		WalletId:          entry.WalletID.String(),
		EntryType:         entry.EntryType,
		Amount:            entry.Amount,
		Currency:          entry.Currency,
		BalanceAfter:      entry.BalanceAfter,
		WalletSequence:    entry.WalletSequence,
		Status:            entry.Status,
		ReferenceType:     entry.ReferenceType,
		ReferenceId:       optionalString(entry.ReferenceID),
		Description:       optionalString(entry.Description),
		MetadataJson:      string(entry.Metadata),
		CreatedAt:         entry.CreatedAt.Format(time.RFC3339Nano),
		AmountMoney:       amountMoney,
		BalanceAfterMoney: balanceAfterMoney,
	}, nil
}

func optionalInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}
