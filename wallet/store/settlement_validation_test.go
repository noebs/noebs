package store

import (
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
)

func TestValidateLimitUsageParams(t *testing.T) {
	walletID := uuid.New()
	valid := LimitUsageParams{
		TenantID: "tenant-a", CommandID: "p2p:command", WalletID: walletID,
		TransactionType: "p2p", Currency: "AED", Amount: 100,
	}
	tests := []struct {
		name    string
		mutate  func(*LimitUsageParams)
		wantErr error
	}{
		{name: "missing tenant", mutate: func(params *LimitUsageParams) { params.TenantID = "" }, wantErr: ErrMissingTenantID},
		{name: "missing command", mutate: func(params *LimitUsageParams) { params.CommandID = "" }, wantErr: ErrMissingLimitCommandID},
		{name: "missing wallet", mutate: func(params *LimitUsageParams) { params.WalletID = uuid.Nil }, wantErr: ErrMissingWalletID},
		{name: "missing transaction type", mutate: func(params *LimitUsageParams) { params.TransactionType = "" }, wantErr: ErrMissingTransactionType},
		{name: "missing currency", mutate: func(params *LimitUsageParams) { params.Currency = "" }, wantErr: ErrMissingCurrency},
		{name: "invalid amount", mutate: func(params *LimitUsageParams) { params.Amount = 0 }, wantErr: ErrInvalidAmount},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := valid
			test.mutate(&params)
			if err := ValidateLimitUsageParams(params); !errors.Is(err, test.wantErr) {
				t.Fatalf("validation error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateHeldWithdrawalSettlementParams(t *testing.T) {
	debitID := uuid.New()
	valid := HeldWithdrawalSettlementParams{
		HoldID: 1,
		Settlement: MultiLegSettlementParams{
			TenantID: "tenant-a", IdempotencyKey: "withdrawal:key", Currency: "AED",
			ReferenceType: "withdrawal", ReferenceID: "reference",
			Transfers: []SettlementTransfer{{DebitWalletID: debitID, CreditWalletID: uuid.New(), Amount: 100}},
			LimitUsage: LimitUsageParams{
				TenantID: "tenant-a", CommandID: "withdrawal:key", WalletID: debitID,
				TransactionType: "withdrawal", Currency: "AED", Amount: 100,
			},
		},
		FundingSourceID:            1,
		FundingReservationID:       2,
		FundingTransferIndex:       0,
		FundingReservationProvider: "bankpay",
	}
	if err := ValidateHeldWithdrawalSettlementParams(valid); err != nil {
		t.Fatalf("valid held withdrawal: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*HeldWithdrawalSettlementParams)
		wantErr error
	}{
		{name: "invalid hold", mutate: func(params *HeldWithdrawalSettlementParams) { params.HoldID = 0 }, wantErr: ErrInvalidHoldID},
		{name: "missing source", mutate: func(params *HeldWithdrawalSettlementParams) { params.FundingSourceID = 0 }, wantErr: ErrMissingFundingSourceID},
		{name: "missing reservation", mutate: func(params *HeldWithdrawalSettlementParams) { params.FundingReservationID = 0 }, wantErr: ErrMissingFundingSourceReservation},
		{name: "missing provider", mutate: func(params *HeldWithdrawalSettlementParams) { params.FundingReservationProvider = "" }, wantErr: ErrMissingProviderCode},
		{name: "invalid destination", mutate: func(params *HeldWithdrawalSettlementParams) { params.WithdrawalDestinationID = -1 }, wantErr: ErrInvalidDestinationID},
		{name: "invalid funding leg", mutate: func(params *HeldWithdrawalSettlementParams) { params.FundingTransferIndex = 1 }, wantErr: ErrInvalidSettlementTransfer},
		{name: "foreign debit wallet", mutate: func(params *HeldWithdrawalSettlementParams) {
			params.Settlement.Transfers = append(params.Settlement.Transfers, SettlementTransfer{
				DebitWalletID: uuid.New(), CreditWalletID: uuid.New(), Amount: 1,
			})
		}, wantErr: ErrInvalidSettlementTransfer},
		{name: "aggregate overflow", mutate: func(params *HeldWithdrawalSettlementParams) {
			params.Settlement.Transfers[0].Amount = math.MaxInt64
			params.Settlement.LimitUsage.Amount = math.MaxInt64
			params.Settlement.Transfers = append(params.Settlement.Transfers, SettlementTransfer{
				DebitWalletID: debitID, CreditWalletID: uuid.New(), Amount: 1,
			})
		}, wantErr: ErrAmountOverflow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := valid
			params.Settlement.Transfers = append([]SettlementTransfer(nil), valid.Settlement.Transfers...)
			test.mutate(&params)
			if err := ValidateHeldWithdrawalSettlementParams(params); !errors.Is(err, test.wantErr) {
				t.Fatalf("validation error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestValidateMultiLegSettlementParams(t *testing.T) {
	debitID := uuid.New()
	creditID := uuid.New()
	valid := MultiLegSettlementParams{
		TenantID: "tenant-a", IdempotencyKey: "p2p:key", Currency: "AED",
		ReferenceType: "p2p", ReferenceID: "reference",
		Transfers: []SettlementTransfer{{DebitWalletID: debitID, CreditWalletID: creditID, Amount: 100}},
		LimitUsage: LimitUsageParams{
			TenantID: "tenant-a", CommandID: "p2p:key", WalletID: debitID,
			TransactionType: "p2p", Currency: "AED", Amount: 100,
		},
	}
	tests := []struct {
		name    string
		mutate  func(*MultiLegSettlementParams)
		wantErr error
	}{
		{name: "missing idempotency", mutate: func(params *MultiLegSettlementParams) { params.IdempotencyKey = "" }, wantErr: ErrMissingIdempotencyKey},
		{name: "missing reference", mutate: func(params *MultiLegSettlementParams) { params.ReferenceID = "" }, wantErr: ErrMissingReferenceID},
		{name: "missing transfers", mutate: func(params *MultiLegSettlementParams) { params.Transfers = nil }, wantErr: ErrMissingSettlementTransfers},
		{name: "same transfer wallet", mutate: func(params *MultiLegSettlementParams) { params.Transfers[0].CreditWalletID = debitID }, wantErr: ErrInvalidWalletPair},
		{name: "limit tenant mismatch", mutate: func(params *MultiLegSettlementParams) { params.LimitUsage.TenantID = "tenant-b" }, wantErr: ErrDuplicateLimitReservation},
		{name: "limit wallet absent", mutate: func(params *MultiLegSettlementParams) { params.LimitUsage.WalletID = uuid.New() }, wantErr: ErrWalletNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			params := valid
			params.Transfers = append([]SettlementTransfer(nil), valid.Transfers...)
			test.mutate(&params)
			if err := ValidateMultiLegSettlementParams(params); !errors.Is(err, test.wantErr) {
				t.Fatalf("validation error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
