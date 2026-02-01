package store

const (
	OwnerTypeUser     = "user"
	OwnerTypeSystem   = "system"
	OwnerTypeMerchant = "merchant"
	OwnerTypePSP      = "psp"
)

const (
	SystemTreasury    = "treasury"
	SystemFees        = "fees"
	SystemSuspense    = "suspense"
	SystemPSPClearing = "psp_clearing"
	SystemFXGainLoss  = "fx_gain_loss"
)

func SystemWalletCodes() []string {
	return []string{
		SystemTreasury,
		SystemFees,
		SystemSuspense,
		SystemPSPClearing,
		SystemFXGainLoss,
	}
}
