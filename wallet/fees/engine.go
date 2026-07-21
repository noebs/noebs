package fees

import (
	"context"
	"errors"
	"math/big"

	"github.com/adonese/noebs/groosh"
	walletstore "github.com/adonese/noebs/wallet/store"
)

var ErrMissingStore = errors.New("missing fee store")

// PercentageFeeRoundingMode is part of the server's financial policy. Fee
// percentages are evaluated exactly and midpoint results are rounded away
// from zero to the nearest operational minor unit.
const PercentageFeeRoundingMode = groosh.RoundHalfAwayFromZero

type FeeEngine struct {
	Store *walletstore.Store
}

type FeeResult struct {
	TotalFee      int64
	PercentageFee int64
	FlatFee       int64
	AppliedTier   *walletstore.FeeConfig
}

func (e *FeeEngine) Calculate(ctx context.Context, tenantID, txType, currency string, currencyUnitID, amount int64) (*FeeResult, error) {
	if e == nil || e.Store == nil {
		return nil, ErrMissingStore
	}
	config, err := e.Store.GetFeeConfigForAmount(ctx, tenantID, txType, currency, currencyUnitID, amount)
	if err != nil {
		return nil, err
	}
	return calculateFee(config, amount)
}

func calculateFee(config *walletstore.FeeConfig, amount int64) (*FeeResult, error) {
	exactPercentageFee := new(big.Rat).SetInt64(amount)
	exactPercentageFee.Mul(exactPercentageFee, config.PercentageFee.Rat())
	exactPercentageFee.Quo(exactPercentageFee, big.NewRat(100, 1))
	percentageFee, err := groosh.RoundMinorUnits(exactPercentageFee, PercentageFeeRoundingMode)
	if err != nil {
		if errors.Is(err, groosh.ErrOverflow) {
			return nil, walletstore.ErrAmountOverflow
		}
		return nil, err
	}

	totalFee, err := checkedAddInt64(percentageFee, config.FlatFee)
	if err != nil {
		return nil, err
	}
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
