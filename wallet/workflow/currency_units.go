package workflow

import walletstore "github.com/adonese/noebs/wallet/store"

// validateRequiredCurrencyUnitIDs validates activity and persistence results
// before a workflow gives their monetary values domain meaning. A zero value
// is omitted input; a negative value is malformed input.
func validateRequiredCurrencyUnitIDs(currencyUnitIDs ...int64) error {
	for _, currencyUnitID := range currencyUnitIDs {
		if err := walletstore.ValidateCurrencyUnitID(currencyUnitID); err != nil {
			return err
		}
	}
	return nil
}
