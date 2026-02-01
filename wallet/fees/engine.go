package fees

import (
	"context"
	"errors"

	walletstore "github.com/adonese/noebs/wallet/store"
	"github.com/shopspring/decimal"
)

var ErrMissingStore = errors.New("missing fee store")

type FeeEngine struct {
	Store *walletstore.Store
}

type FeeResult struct {
	TotalFee      int64
	PercentageFee int64
	FlatFee       int64
	AppliedTier   *walletstore.FeeConfig
}

func (e *FeeEngine) Calculate(ctx context.Context, tenantID, txType, currency string, amount int64) (*FeeResult, error) {
	if e == nil || e.Store == nil {
		return nil, ErrMissingStore
	}
	config, err := e.Store.GetFeeConfigForAmount(ctx, tenantID, txType, currency, amount)
	if err != nil {
		return nil, err
	}

	percentageFee := decimal.NewFromInt(amount).
		Mul(config.PercentageFee).
		Div(decimal.NewFromInt(100)).
		Round(0).
		IntPart()

	totalFee := percentageFee + config.FlatFee
	if totalFee < config.MinFee {
		totalFee = config.MinFee
	}
	if config.MaxFee.Valid && totalFee > config.MaxFee.Int64 {
		totalFee = config.MaxFee.Int64
	}

	return &FeeResult{
		TotalFee:      totalFee,
		PercentageFee: percentageFee,
		FlatFee:       config.FlatFee,
		AppliedTier:   config,
	}, nil
}
