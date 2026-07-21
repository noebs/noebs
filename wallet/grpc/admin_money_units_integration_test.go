package walletgrpc

import (
	"context"
	"testing"
	"time"

	walletv1 "github.com/adonese/noebs/gen/proto/noebs/wallet/v1"
	"github.com/adonese/noebs/internal/tenantauth"
	"google.golang.org/grpc/metadata"
)

func TestAdminMoneyPoliciesResolveUnitVersionsAtBoundary(t *testing.T) {
	server, tenantID, _, _ := newWalletServerWithUsers(t)
	db := server.Service.Store.DB

	feeContext := metadata.NewIncomingContext(
		context.Background(),
		operatorMetadataForTenant(tenantID, tenantauth.PermissionWalletFeesWrite),
	)
	feeResponse, err := server.RenderWalletAdmin(feeContext, &walletv1.RenderWalletAdminRequest{
		Action: walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_FEE,
		Form: map[string]string{
			"transaction_type": "deposit",
			"currency":         "USD",
			"tier_min":         "0",
			"percentage_fee":   "0",
			"flat_fee":         "10",
			"min_fee":          "0",
			"is_active":        "true",
		},
	})
	if err != nil {
		t.Fatalf("create admin fee: %v", err)
	}
	wantFeeRedirect := "/backoffice/t/" + tenantID + "/wallet/fees"
	if feeResponse.GetRedirectLocation() != wantFeeRedirect {
		t.Fatalf("fee redirect = %q, want %q", feeResponse.GetRedirectLocation(), wantFeeRedirect)
	}
	var feeUnitID int64
	if err := db.GetContext(context.Background(), &feeUnitID, `SELECT currency_unit_version_id
		FROM fee_configs
		WHERE tenant_id = $1 AND transaction_type = 'deposit' AND currency = 'USD'`, tenantID); err != nil {
		t.Fatalf("load created fee unit: %v", err)
	}
	var currentUSDUnitID int64
	if err := db.GetContext(context.Background(), &currentUSDUnitID, `SELECT id
		FROM currency_unit_versions
		WHERE currency_code = 'USD' AND valid_to IS NULL`); err != nil {
		t.Fatalf("load current USD unit: %v", err)
	}
	if feeUnitID != currentUSDUnitID {
		t.Fatalf("fee unit id = %d, want boundary-current unit %d", feeUnitID, currentUSDUnitID)
	}

	now := time.Now().UTC()
	transitionDate := time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)
	var currentCLFUnitID int64
	if err := db.GetContext(context.Background(), &currentCLFUnitID, `SELECT id
		FROM currency_unit_versions
		WHERE currency_code = 'CLF' AND valid_to IS NULL`); err != nil {
		t.Fatalf("load current CLF unit: %v", err)
	}
	transitionTx, err := db.BeginTxx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin USD unit transition: %v", err)
	}
	defer func() { _ = transitionTx.Rollback() }()
	if _, err := transitionTx.ExecContext(context.Background(), `UPDATE currency_unit_versions
		SET valid_to = $1
		WHERE id = $2 AND currency_code = 'CLF' AND valid_to IS NULL`, transitionDate, currentCLFUnitID); err != nil {
		t.Fatalf("close old CLF unit: %v", err)
	}
	var futureCLFUnitID int64
	if err := transitionTx.GetContext(context.Background(), &futureCLFUnitID, `INSERT INTO currency_unit_versions(
		currency_code, iso_minor_exponent, display_exponent, cash_exponent,
		cash_rounding_increment, valid_from, source, source_revision, source_published_on
	) VALUES ('CLF', 3, 3, 3, 1, $1, 'test', 'clf-2030', $1)
	RETURNING id`, transitionDate); err != nil {
		t.Fatalf("insert future CLF unit: %v", err)
	}
	if err := transitionTx.Commit(); err != nil {
		t.Fatalf("commit USD unit transition: %v", err)
	}

	rateContext := metadata.NewIncomingContext(
		context.Background(),
		operatorMetadataForTenant(tenantID, tenantauth.PermissionWalletRatesWrite),
	)
	rateResponse, err := server.RenderWalletAdmin(rateContext, &walletv1.RenderWalletAdminRequest{
		Action: walletv1.AdminWalletAction_ADMIN_WALLET_ACTION_CREATE_RATE,
		Form: map[string]string{
			"base_currency":  "CLF",
			"quote_currency": "EUR",
			"buy_rate":       "0.9",
			"sell_rate":      "0.91",
			"effective_from": transitionDate.Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("create admin rate: %v", err)
	}
	wantRateRedirect := "/backoffice/t/" + tenantID + "/wallet/rates"
	if rateResponse.GetRedirectLocation() != wantRateRedirect {
		t.Fatalf("rate redirect = %q, want %q", rateResponse.GetRedirectLocation(), wantRateRedirect)
	}
	var rateUnits struct {
		BaseID  int64 `db:"base_currency_unit_version_id"`
		QuoteID int64 `db:"quote_currency_unit_version_id"`
	}
	if err := db.GetContext(context.Background(), &rateUnits, `SELECT
		base_currency_unit_version_id, quote_currency_unit_version_id
		FROM exchange_rates
		WHERE tenant_id = $1 AND base_currency = 'CLF' AND quote_currency = 'EUR'`, tenantID); err != nil {
		t.Fatalf("load created rate units: %v", err)
	}
	if rateUnits.BaseID != futureCLFUnitID {
		t.Fatalf("rate base unit id = %d, want effective-date unit %d", rateUnits.BaseID, futureCLFUnitID)
	}
	if rateUnits.QuoteID <= 0 {
		t.Fatalf("rate quote unit id = %d, want positive", rateUnits.QuoteID)
	}
}
