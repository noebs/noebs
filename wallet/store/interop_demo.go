package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// SeedInteropDemo is an explicit migration/operator action for the isolated
// synthetic tenant. It is never invoked by a public API or a worker. Re-running
// preserves the original binding, aliases and opening journal, including the
// operator-controlled admission switch.
func (s *Store) SeedInteropDemo(ctx context.Context, tenant, fsp string) error {
	if tenant != "tenant-mojaloop" || fsp != "noebs" {
		return ErrInteropInvalid
	}
	unit, err := s.GetCurrencyUnit(ctx, "SDG", time.Now().UTC())
	if err != nil {
		return err
	}
	if unit.DisplayExponent != 2 || !unit.ISOMinorExponent.Valid || unit.ISOMinorExponent.Int16 != 2 {
		return ErrInteropInvalid
	}
	ensure := func(kind, owner string, user int64) (*Wallet, error) {
		return s.EnsureWallet(ctx, EnsureWalletParams{TenantID: tenant, OwnerType: kind, OwnerID: owner, UserID: user, Currency: "SDG", CurrencyUnitID: unit.ID, KYCTier: KYCTierUnverified})
	}
	clearing, err := ensure(OwnerTypeSystem, SystemMojaloopClearing, 0)
	if err != nil {
		return err
	}
	suspense, err := ensure(OwnerTypeSystem, SystemMojaloopSuspense, 0)
	if err != nil {
		return err
	}
	treasury, err := ensure(OwnerTypeSystem, SystemTreasury, 0)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `INSERT INTO interop_bindings(tenant_id,fsp_id,currency,currency_unit_version_id,clearing_wallet_id,suspense_wallet_id,enabled) VALUES($1,$2,'SDG',$3,$4,$5,false) ON CONFLICT(tenant_id) DO NOTHING`, tenant, fsp, unit.ID, clearing.ID, suspense.ID)
	if err != nil {
		return err
	}
	binding, err := s.GetInteropBinding(ctx, tenant)
	if err != nil {
		return err
	}
	if binding.FSPID != fsp || binding.CurrencyUnitID != unit.ID || binding.ClearingWalletID != clearing.ID || binding.SuspenseWalletID != suspense.ID {
		return ErrInteropConflict
	}
	for i, user := range []int64{900000001, 900000002} {
		wallet, err := ensure(OwnerTypeUser, strconv.FormatInt(user, 10), user)
		if err != nil {
			return err
		}
		alias := fmt.Sprintf("24990000002%d", i+1)
		_, err = s.DB.ExecContext(ctx, `INSERT INTO interop_aliases(tenant_id,identifier,wallet_id,display_name) VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id,wallet_id) DO NOTHING`, tenant, alias, wallet.ID, fmt.Sprintf("Synthetic noebs wallet %d", i+1))
		if err != nil {
			return err
		}
		stored, err := s.GetInteropAlias(ctx, tenant, wallet.ID, "")
		if err != nil {
			return err
		}
		if stored.Identifier != alias {
			return ErrInteropConflict
		}
		if i == 0 {
			err = s.ensureInteropDemoOpening(ctx, DoubleEntryParams{TenantID: tenant, IdempotencyKey: "mojaloop-demo-opening-v1", DebitWalletID: treasury.ID, CreditWalletID: wallet.ID, Amount: 100000, Currency: "SDG", ReferenceType: "synthetic-opening", ReferenceID: "noebs-mojaloop-demo-v1", Description: "One-time synthetic SDG demonstration funds"}, unit.ID)
			if err != nil {
				return err
			}
		}
	}
	for _, direction := range []string{"interop_out", "interop_in"} {
		_, err = s.DB.ExecContext(ctx, `INSERT INTO transaction_limits(tenant_id,kyc_tier,transaction_type,currency,currency_unit_version_id,daily_limit,monthly_limit,per_transaction_limit) VALUES($1,$2,$3,'SDG',$4,1000000,10000000,100000) ON CONFLICT DO NOTHING`, tenant, KYCTierUnverified, direction, unit.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureInteropDemoOpening(ctx context.Context, entry DoubleEntryParams, unitID int64) error {
	tx, err := s.DB.BeginTxx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, err := s.loadExistingEntries(ctx, tx, entry)
	if err == nil {
		if existing.DebitEntry.CurrencyUnitID != unitID || existing.CreditEntry.CurrencyUnitID != unitID {
			return ErrInteropConflict
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err = tx.Rollback(); err != nil {
		return err
	}
	_, err = s.PostSystemDebitDoubleEntry(ctx, entry)
	return err
}
