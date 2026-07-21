package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// PSPAmountPolicy gives min/max integers an exact unit meaning. Region is the
// empty string for a provider-wide policy; an exact region wins when present.
type PSPAmountPolicy struct {
	ID             int64         `db:"id"`
	TenantID       string        `db:"tenant_id"`
	ProviderCode   string        `db:"provider_code"`
	Currency       string        `db:"currency"`
	CurrencyUnitID int64         `db:"currency_unit_version_id"`
	Direction      string        `db:"direction"`
	Region         string        `db:"region"`
	MinAmount      sql.NullInt64 `db:"min_amount"`
	MaxAmount      sql.NullInt64 `db:"max_amount"`
	IsActive       bool          `db:"is_active"`
	CreatedAt      time.Time     `db:"created_at"`
	UpdatedAt      time.Time     `db:"updated_at"`
}

func validatePSPAmountPolicyScope(scope PSPConfigScope) (PSPConfigScope, error) {
	currency, err := ValidateCurrencyCode(scope.Currency)
	if err != nil {
		return PSPConfigScope{}, err
	}
	if err := ValidateCurrencyUnitID(scope.CurrencyUnitID); err != nil {
		return PSPConfigScope{}, err
	}
	if scope.Direction != "deposit" && scope.Direction != "withdrawal" {
		if scope.Direction == "" {
			return PSPConfigScope{}, ErrMissingDirection
		}
		return PSPConfigScope{}, ErrInvalidDirection
	}
	if scope.Region != strings.TrimSpace(scope.Region) || len(scope.Region) > 128 {
		return PSPConfigScope{}, ErrInvalidRegion
	}
	scope.Currency = currency
	return scope, nil
}

func (s *Store) GetActivePSPAmountPolicy(ctx context.Context, tenantID, providerCode string, scope PSPConfigScope) (*PSPAmountPolicy, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if providerCode == "" {
		return nil, ErrMissingProviderCode
	}
	scope, err = validatePSPAmountPolicyScope(scope)
	if err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	if err := s.validateCurrencyUnitIdentity(ctx, scope.Currency, scope.CurrencyUnitID); err != nil {
		return nil, err
	}
	query := db.Rebind(`SELECT *
		FROM psp_amount_policies
		WHERE tenant_id = ?
		  AND provider_code = ?
		  AND currency = ?
		  AND currency_unit_version_id = ?
		  AND direction = ?
		  AND is_active = TRUE
		  AND (region = '' OR region = ?)
		ORDER BY CASE WHEN region = ? AND region <> '' THEN 1 ELSE 0 END DESC,
		         id DESC
		LIMIT 1`)
	var policy PSPAmountPolicy
	if err := db.GetContext(
		ctx,
		&policy,
		query,
		tenantID,
		providerCode,
		scope.Currency,
		scope.CurrencyUnitID,
		scope.Direction,
		scope.Region,
		scope.Region,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrPSPAmountPolicyNotFound
		}
		return nil, err
	}
	return &policy, nil
}
