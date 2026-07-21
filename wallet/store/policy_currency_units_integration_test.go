package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"
)

func TestCurrencyUnitEffectiveAtUsesHalfOpenUTCDateRange(t *testing.T) {
	unit := &CurrencyUnitVersion{
		ValidFrom: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
		ValidTo: sql.NullTime{
			Time:  time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC),
			Valid: true,
		},
	}
	tests := []struct {
		name    string
		instant time.Time
		want    bool
	}{
		{name: "before", instant: time.Date(2025, time.December, 31, 23, 59, 59, 0, time.UTC)},
		{name: "valid from", instant: time.Date(2026, time.January, 1, 23, 59, 59, 0, time.UTC), want: true},
		{name: "before valid to", instant: time.Date(2026, time.January, 31, 23, 59, 59, 0, time.UTC), want: true},
		{name: "at valid to", instant: time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := currencyUnitEffectiveAt(unit, tt.instant); got != tt.want {
				t.Fatalf("currencyUnitEffectiveAt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCurrencyCatalogRejectsIdentityMutationDeletionAndGappedTransition(t *testing.T) {
	ctx, walletStore, _ := newWalletStoreIntegration(t)
	unitID := testCurrencyUnitID(t, ctx, walletStore, "USD")

	for name, statement := range map[string]string{
		"delete currency":       `DELETE FROM currencies WHERE code = (SELECT currency_code FROM currency_unit_versions WHERE id = $1)`,
		"repurpose currency":    `UPDATE currencies SET numeric_code = '999' WHERE code = (SELECT currency_code FROM currency_unit_versions WHERE id = $1)`,
		"delete unit":           `DELETE FROM currency_unit_versions WHERE id = $1`,
		"rewrite unit exponent": `UPDATE currency_unit_versions SET iso_minor_exponent = 3 WHERE id = $1`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := walletStore.DB.ExecContext(ctx, statement, unitID); err == nil {
				t.Fatalf("catalog mutation %q unexpectedly succeeded", name)
			}
		})
	}

	transitionDate := time.Date(2031, time.January, 1, 0, 0, 0, 0, time.UTC)
	tx, err := walletStore.DB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin invalid unit transition: %v", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE currency_unit_versions SET valid_to = $1 WHERE id = $2`, transitionDate, unitID,
	); err != nil {
		_ = tx.Rollback()
		t.Fatalf("stage current-unit closure: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO currency_unit_versions(
		currency_code, iso_minor_exponent, display_exponent, cash_exponent,
		cash_rounding_increment, valid_from, valid_to, source, source_revision, source_published_on
	) VALUES('USD', 2, 2, 2, 1, $1, $2, 'test', 'finite-successor', $1)`,
		transitionDate, transitionDate.AddDate(0, 1, 0),
	); err != nil {
		_ = tx.Rollback()
		t.Fatalf("stage finite successor: %v", err)
	}
	if err := tx.Commit(); err == nil {
		t.Fatal("currency-unit transition without an open-ended successor committed")
	}

	var openUnits int
	if err := walletStore.DB.GetContext(ctx, &openUnits,
		`SELECT count(*) FROM currency_unit_versions WHERE currency_code = 'USD' AND valid_to IS NULL`,
	); err != nil {
		t.Fatalf("count current units after rejected transition: %v", err)
	}
	if openUnits != 1 {
		t.Fatalf("open USD units after rejected transition = %d, want 1", openUnits)
	}
}

func TestFeeAndLimitPoliciesSelectExactCurrencyUnitVersion(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	operatorID := insertWalletOperator(t, ctx, walletStore, "policy-unit-operator")
	oldUnitID := testCurrencyUnitID(t, ctx, walletStore, "USD")

	transitionDate := time.Date(2030, time.January, 1, 0, 0, 0, 0, time.UTC)
	tx, err := walletStore.DB.BeginTxx(ctx, nil)
	if err != nil {
		t.Fatalf("begin USD unit transition: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	closeCurrent := walletStore.DB.Rebind(`UPDATE currency_unit_versions
		SET valid_to = ?
		WHERE id = ? AND currency_code = ? AND valid_to IS NULL`)
	if result, err := tx.ExecContext(ctx, closeCurrent, transitionDate, oldUnitID, "USD"); err != nil {
		t.Fatalf("close current USD unit: %v", err)
	} else if affected, rowsErr := result.RowsAffected(); rowsErr != nil || affected != 1 {
		t.Fatalf("close current USD unit rows = %d, err = %v", affected, rowsErr)
	}
	insertUnit := walletStore.DB.Rebind(`INSERT INTO currency_unit_versions(
		currency_code, iso_minor_exponent, display_exponent, cash_exponent,
		cash_rounding_increment, valid_from, source, source_revision, source_published_on
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)
	RETURNING id`)
	var newUnitID int64
	if err := tx.GetContext(
		ctx,
		&newUnitID,
		insertUnit,
		"USD",
		3,
		3,
		3,
		1,
		transitionDate,
		"test",
		"usd-redenomination-2030",
		transitionDate,
	); err != nil {
		t.Fatalf("insert future USD unit: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit USD unit transition: %v", err)
	}

	for _, cfg := range []FeeConfig{
		{
			TenantID:            tenantID,
			TransactionType:     "deposit",
			Currency:            "USD",
			CurrencyUnitID:      oldUnitID,
			PercentageFee:       decimal.Zero,
			FlatFee:             10,
			IsActive:            true,
			CreatedByOperatorID: operatorID,
		},
		{
			TenantID:            tenantID,
			TransactionType:     "deposit",
			Currency:            "USD",
			CurrencyUnitID:      newUnitID,
			PercentageFee:       decimal.Zero,
			FlatFee:             20,
			IsActive:            true,
			CreatedByOperatorID: operatorID,
		},
	} {
		if _, err := walletStore.CreateFeeConfig(ctx, cfg); err != nil {
			t.Fatalf("create fee config for unit %d: %v", cfg.CurrencyUnitID, err)
		}
	}

	oldFee, err := walletStore.GetFeeConfigForAmount(ctx, tenantID, "deposit", "USD", oldUnitID, 100)
	if err != nil {
		t.Fatalf("get old-unit fee: %v", err)
	}
	newFee, err := walletStore.GetFeeConfigForAmount(ctx, tenantID, "deposit", "USD", newUnitID, 100)
	if err != nil {
		t.Fatalf("get new-unit fee: %v", err)
	}
	if oldFee.FlatFee != 10 || newFee.FlatFee != 20 {
		t.Fatalf("unit-pinned fees = (%d, %d), want (10, 20)", oldFee.FlatFee, newFee.FlatFee)
	}

	insertLimit := walletStore.DB.Rebind(`INSERT INTO transaction_limits(
		tenant_id, kyc_tier, transaction_type, currency, currency_unit_version_id,
		daily_limit, monthly_limit, per_transaction_limit, is_active
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, TRUE)`)
	for _, item := range []struct {
		unitID int64
		limit  int64
	}{
		{unitID: oldUnitID, limit: 100},
		{unitID: newUnitID, limit: 200},
	} {
		if _, err := walletStore.DB.ExecContext(
			ctx,
			insertLimit,
			tenantID,
			KYCTierUnverified,
			"deposit",
			"USD",
			item.unitID,
			item.limit,
			item.limit,
			item.limit,
		); err != nil {
			t.Fatalf("insert limit for unit %d: %v", item.unitID, err)
		}
	}

	oldLimit, err := walletStore.GetLimits(ctx, tenantID, KYCTierUnverified, "deposit", "USD", oldUnitID)
	if err != nil {
		t.Fatalf("get old-unit limit: %v", err)
	}
	newLimit, err := walletStore.GetLimits(ctx, tenantID, KYCTierUnverified, "deposit", "USD", newUnitID)
	if err != nil {
		t.Fatalf("get new-unit limit: %v", err)
	}
	if oldLimit.PerTransactionLimit != 100 || newLimit.PerTransactionLimit != 200 {
		t.Fatalf("unit-pinned limits = (%d, %d), want (100, 200)", oldLimit.PerTransactionLimit, newLimit.PerTransactionLimit)
	}

	for _, unitID := range []int64{oldUnitID, newUnitID} {
		_, err = walletStore.EnsureWallet(ctx, EnsureWalletParams{
			TenantID:       tenantID,
			OwnerType:      OwnerTypeSystem,
			OwnerID:        "policy-unit-wallet",
			Currency:       "USD",
			CurrencyUnitID: unitID,
			KYCTier:        KYCTierUnverified,
		})
		if !errors.Is(err, ErrCurrencyUnitTransitionUnsupported) {
			t.Fatalf("EnsureWallet(unit %d) error = %v, want %v", unitID, err, ErrCurrencyUnitTransitionUnsupported)
		}
	}
}

func TestCurrencyUnitTransitionAndFirstWalletSerializeBothRaceOrders(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	now := time.Now().UTC()
	transitionDate := time.Date(now.Year()+1, time.January, 1, 0, 0, 0, 0, time.UTC)

	t.Run("wallet commits first", func(t *testing.T) {
		unitID := testCurrencyUnitID(t, ctx, walletStore, "USD")
		walletTx, err := walletStore.DB.BeginTxx(ctx, nil)
		if err != nil {
			t.Fatalf("begin wallet admission: %v", err)
		}
		defer func() { _ = walletTx.Rollback() }()
		if _, err := walletTx.ExecContext(ctx, `INSERT INTO wallets(
			tenant_id, owner_type, owner_id, currency, currency_unit_version_id, kyc_tier
		) VALUES($1, 'system', 'wallet-first', 'USD', $2, 'unverified')`, tenantID, unitID); err != nil {
			t.Fatalf("stage first wallet: %v", err)
		}

		transitionResult := make(chan error, 1)
		go func() {
			tx, beginErr := walletStore.DB.BeginTxx(ctx, nil)
			if beginErr != nil {
				transitionResult <- beginErr
				return
			}
			defer func() { _ = tx.Rollback() }()
			if _, updateErr := tx.ExecContext(ctx, `/* groosh_wallet_first_transition */
				UPDATE currency_unit_versions SET valid_to = $1 WHERE id = $2`, transitionDate, unitID); updateErr != nil {
				transitionResult <- updateErr
				return
			}
			_, insertErr := tx.ExecContext(ctx, `INSERT INTO currency_unit_versions(
				currency_code, iso_minor_exponent, display_exponent, cash_exponent,
				cash_rounding_increment, valid_from, source, source_revision, source_published_on
			) VALUES('USD', 2, 2, 2, 1, $1, 'test', 'wallet-first-successor', $1)`, transitionDate)
			transitionResult <- insertErr
		}()
		waitForAdvisoryQuery(t, ctx, walletStore, "groosh_wallet_first_transition")
		if err := walletTx.Commit(); err != nil {
			t.Fatalf("commit first wallet: %v", err)
		}
		err = receiveTransitionResult(t, transitionResult)
		assertPostgresConstraint(t, err, "currency_unit_versions_wallet_transition")
	})

	t.Run("transition commits first", func(t *testing.T) {
		unitID := testCurrencyUnitID(t, ctx, walletStore, "EUR")
		transitionTx, err := walletStore.DB.BeginTxx(ctx, nil)
		if err != nil {
			t.Fatalf("begin transition: %v", err)
		}
		defer func() { _ = transitionTx.Rollback() }()
		if _, err := transitionTx.ExecContext(ctx,
			`UPDATE currency_unit_versions SET valid_to = $1 WHERE id = $2`, transitionDate, unitID,
		); err != nil {
			t.Fatalf("stage current-unit closure: %v", err)
		}
		if _, err := transitionTx.ExecContext(ctx, `INSERT INTO currency_unit_versions(
			currency_code, iso_minor_exponent, display_exponent, cash_exponent,
			cash_rounding_increment, valid_from, source, source_revision, source_published_on
		) VALUES('EUR', 2, 2, 2, 1, $1, 'test', 'transition-first-successor', $1)`, transitionDate); err != nil {
			t.Fatalf("stage successor: %v", err)
		}

		walletResult := make(chan error, 1)
		go func() {
			_, insertErr := walletStore.DB.ExecContext(ctx, `/* groosh_transition_first_wallet */
				INSERT INTO wallets(
					tenant_id, owner_type, owner_id, currency, currency_unit_version_id, kyc_tier
				) VALUES($1, 'system', 'transition-first', 'EUR', $2, 'unverified')`, tenantID, unitID)
			walletResult <- insertErr
		}()
		waitForAdvisoryQuery(t, ctx, walletStore, "groosh_transition_first_wallet")
		if err := transitionTx.Commit(); err != nil {
			t.Fatalf("commit transition: %v", err)
		}
		err = receiveTransitionResult(t, walletResult)
		assertPostgresConstraint(t, err, "wallets_open_currency_unit_required")
	})
}

func waitForAdvisoryQuery(t *testing.T, ctx context.Context, walletStore *Store, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting int
		if err := walletStore.DB.GetContext(ctx, &waiting, `SELECT count(*)
			FROM pg_stat_activity
			WHERE datname = current_database()
			  AND query LIKE '%' || $1 || '%'
			  AND wait_event_type = 'Lock'
			  AND wait_event = 'advisory'`, marker); err != nil {
			t.Fatalf("inspect advisory wait: %v", err)
		}
		if waiting == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("query %q did not block on the currency advisory lock", marker)
}

func receiveTransitionResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent currency-unit operation did not finish")
		return nil
	}
}

func assertPostgresConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	if err == nil {
		t.Fatalf("operation unexpectedly bypassed %s", constraint)
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) ||
		postgresError.Code != "23514" ||
		postgresError.ConstraintName != constraint {
		t.Fatalf("operation error = %#v, want PostgreSQL constraint %s", err, constraint)
	}
}

func TestPolicyCreationRejectsCurrencyUnitCodeMismatch(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	operatorID := insertWalletOperator(t, ctx, walletStore, "policy-unit-mismatch-operator")
	usdUnitID := testCurrencyUnitID(t, ctx, walletStore, "USD")
	eurUnitID := testCurrencyUnitID(t, ctx, walletStore, "EUR")
	now := time.Now().UTC()

	_, err := walletStore.CreateFeeConfig(ctx, FeeConfig{
		TenantID:            tenantID,
		TransactionType:     "deposit",
		Currency:            "USD",
		CurrencyUnitID:      eurUnitID,
		PercentageFee:       decimal.Zero,
		IsActive:            true,
		CreatedByOperatorID: operatorID,
	})
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("CreateFeeConfig() error = %v, want %v", err, ErrCurrencyMismatch)
	}

	_, err = walletStore.CreateExchangeRate(ctx, ExchangeRate{
		TenantID:            tenantID,
		BaseCurrency:        "USD",
		BaseCurrencyUnitID:  eurUnitID,
		QuoteCurrency:       "EUR",
		QuoteCurrencyUnitID: eurUnitID,
		BuyRate:             decimal.NewFromInt(1),
		SellRate:            decimal.NewFromInt(1),
		SetByOperatorID:     operatorID,
		EffectiveFrom:       now,
	})
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("CreateExchangeRate() error = %v, want %v", err, ErrCurrencyMismatch)
	}

	_, err = walletStore.CreateExchangeRate(ctx, ExchangeRate{
		TenantID:            tenantID,
		BaseCurrency:        "USD",
		BaseCurrencyUnitID:  usdUnitID,
		QuoteCurrency:       "EUR",
		QuoteCurrencyUnitID: eurUnitID,
		BuyRate:             decimal.NewFromInt(1),
		SellRate:            decimal.NewFromInt(1),
		SetByOperatorID:     operatorID,
		EffectiveFrom:       time.Date(2025, time.December, 31, 0, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("CreateExchangeRate(ineffective unit) error = %v, want %v", err, ErrCurrencyMismatch)
	}

	_, err = walletStore.GetLimits(ctx, tenantID, KYCTierUnverified, "deposit", "USD", eurUnitID)
	if !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("GetLimits() error = %v, want %v", err, ErrCurrencyMismatch)
	}
}

func TestPolicyDecimalInputsNeverRoundSilently(t *testing.T) {
	ctx, walletStore, tenantID := newWalletStoreIntegration(t)
	operatorID := insertWalletOperator(t, ctx, walletStore, "policy-decimal-operator")
	usdUnitID := testCurrencyUnitID(t, ctx, walletStore, "USD")
	eurUnitID := testCurrencyUnitID(t, ctx, walletStore, "EUR")
	now := time.Now().UTC()

	var feeCountBefore int
	feeCountQuery := walletStore.DB.Rebind(`SELECT COUNT(*) FROM fee_configs WHERE tenant_id = ?`)
	if err := walletStore.DB.GetContext(ctx, &feeCountBefore, feeCountQuery, tenantID); err != nil {
		t.Fatalf("count fee configs before precision rejection: %v", err)
	}
	for _, tt := range []struct {
		name       string
		percentage string
	}{
		{name: "fractional overflow", percentage: "0.00001"},
		{name: "integer overflow", percentage: "10000"},
	} {
		t.Run("fee "+tt.name, func(t *testing.T) {
			_, err := walletStore.CreateFeeConfig(ctx, FeeConfig{
				TenantID:            tenantID,
				TransactionType:     "precision-test-" + tt.name,
				Currency:            "USD",
				CurrencyUnitID:      usdUnitID,
				PercentageFee:       decimal.RequireFromString(tt.percentage),
				IsActive:            true,
				CreatedByOperatorID: operatorID,
			})
			if !errors.Is(err, ErrFeePercentageNotRepresentable) {
				t.Fatalf("CreateFeeConfig() error = %v, want %v", err, ErrFeePercentageNotRepresentable)
			}
		})
	}
	var feeCountAfter int
	if err := walletStore.DB.GetContext(ctx, &feeCountAfter, feeCountQuery, tenantID); err != nil {
		t.Fatalf("count fee configs after precision rejection: %v", err)
	}
	if feeCountAfter != feeCountBefore {
		t.Fatalf("fee config count after rejected decimals = %d, want %d", feeCountAfter, feeCountBefore)
	}

	baseRate := ExchangeRate{
		TenantID:            tenantID,
		BaseCurrency:        "USD",
		BaseCurrencyUnitID:  usdUnitID,
		QuoteCurrency:       "EUR",
		QuoteCurrencyUnitID: eurUnitID,
		BuyRate:             decimal.NewFromInt(1),
		SellRate:            decimal.NewFromInt(1),
		SetByOperatorID:     operatorID,
		EffectiveFrom:       now,
	}
	for _, tt := range []struct {
		name    string
		mutate  func(*ExchangeRate)
		wantErr error
	}{
		{
			name: "buy fractional overflow",
			mutate: func(rate *ExchangeRate) {
				rate.BuyRate = decimal.RequireFromString("1.000000001")
			},
			wantErr: ErrLegacyRateNotRepresentable,
		},
		{
			name: "sell integer overflow",
			mutate: func(rate *ExchangeRate) {
				rate.SellRate = decimal.RequireFromString("10000000000")
			},
			wantErr: ErrLegacyRateNotRepresentable,
		},
		{
			name: "spread fractional overflow",
			mutate: func(rate *ExchangeRate) {
				rate.Spread = decimal.NullDecimal{Decimal: decimal.RequireFromString("0.00001"), Valid: true}
			},
			wantErr: ErrSpreadNotRepresentable,
		},
		{
			name: "spread integer overflow",
			mutate: func(rate *ExchangeRate) {
				rate.Spread = decimal.NullDecimal{Decimal: decimal.RequireFromString("10000"), Valid: true}
			},
			wantErr: ErrSpreadNotRepresentable,
		},
	} {
		t.Run("rate "+tt.name, func(t *testing.T) {
			rate := baseRate
			tt.mutate(&rate)
			_, err := walletStore.CreateExchangeRate(ctx, rate)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CreateExchangeRate() error = %v, want %v", err, tt.wantErr)
			}
		})
	}

	var rateCount int
	rateCountQuery := walletStore.DB.Rebind(`SELECT COUNT(*) FROM exchange_rates WHERE tenant_id = ?`)
	if err := walletStore.DB.GetContext(ctx, &rateCount, rateCountQuery, tenantID); err != nil {
		t.Fatalf("count rates after precision rejection: %v", err)
	}
	if rateCount != 0 {
		t.Fatalf("exchange-rate count after rejected decimals = %d, want 0", rateCount)
	}

	exactFee, err := walletStore.CreateFeeConfig(ctx, FeeConfig{
		TenantID:            tenantID,
		TransactionType:     "precision-boundary",
		Currency:            "USD",
		CurrencyUnitID:      usdUnitID,
		PercentageFee:       decimal.RequireFromString("9999.9999"),
		IsActive:            true,
		CreatedByOperatorID: operatorID,
	})
	if err != nil {
		t.Fatalf("create exactly representable fee: %v", err)
	}
	if !exactFee.PercentageFee.Equal(decimal.RequireFromString("9999.9999")) {
		t.Fatalf("stored fee percentage = %s, want exact boundary", exactFee.PercentageFee)
	}

	exactRate := baseRate
	exactRate.EffectiveFrom = now.Add(time.Second)
	exactRate.BuyRate = decimal.RequireFromString("9999999999.99999999")
	exactRate.SellRate = decimal.RequireFromString("9999999999.99999998")
	exactRate.Spread = decimal.NullDecimal{Decimal: decimal.RequireFromString("9999.9999"), Valid: true}
	storedRate, err := walletStore.CreateExchangeRate(ctx, exactRate)
	if err != nil {
		t.Fatalf("create exactly representable exchange rate: %v", err)
	}
	if !storedRate.BuyRate.Equal(exactRate.BuyRate) || !storedRate.SellRate.Equal(exactRate.SellRate) ||
		!storedRate.Spread.Valid || !storedRate.Spread.Decimal.Equal(exactRate.Spread.Decimal) {
		t.Fatalf("stored exchange rate decimals = %+v, want exact %+v", storedRate, exactRate)
	}
}
