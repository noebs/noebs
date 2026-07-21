package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type EnsureWalletParams struct {
	TenantID       string
	OwnerType      string
	OwnerID        string
	UserID         int64
	Currency       string
	CurrencyUnitID int64
	KYCTier        string
}

func (s *Store) EnsureWallet(ctx context.Context, params EnsureWalletParams) (*Wallet, error) {
	tenantID, err := ValidateTenantID(params.TenantID)
	if err != nil {
		return nil, err
	}
	if params.OwnerType == "" {
		return nil, ErrMissingOwnerType
	}
	if !OwnerTypeValid(params.OwnerType) {
		return nil, ErrInvalidOwnerType
	}
	if params.OwnerID == "" {
		return nil, ErrMissingOwnerID
	}
	if _, err := ValidateCurrencyCode(params.Currency); err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(params.CurrencyUnitID); err != nil {
		return nil, err
	}
	if params.KYCTier == "" {
		return nil, ErrMissingKYCTier
	}
	if params.OwnerType == OwnerTypeUser {
		if params.UserID <= 0 {
			return nil, ErrInvalidUserID
		}
	} else if params.UserID != 0 {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	unit, err := s.GetCurrencyUnitByID(ctx, params.CurrencyUnitID)
	if err != nil {
		return nil, err
	}
	if unit.CurrencyCode != params.Currency {
		return nil, ErrCurrencyMismatch
	}
	now := time.Now().UTC()
	if unit.ValidTo.Valid || !currencyUnitEffectiveAt(unit, now) {
		return nil, ErrCurrencyUnitTransitionUnsupported
	}
	var uid sql.NullInt64
	if params.OwnerType == OwnerTypeUser {
		uid = sql.NullInt64{Int64: params.UserID, Valid: true}
	}
	stmt := s.DB.Rebind(`INSERT INTO wallets(
		tenant_id, owner_type, owner_id, user_id, currency, currency_unit_version_id,
		kyc_tier, balance, available_balance, status, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?)
	ON CONFLICT DO NOTHING`)
	if _, err := db.ExecContext(ctx, stmt, tenantID, params.OwnerType, params.OwnerID, uid, params.Currency, params.CurrencyUnitID, params.KYCTier, WalletStatusActive, now, now); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.Code == "23514" &&
			postgresError.ConstraintName == "wallets_open_currency_unit_required" {
			return nil, ErrCurrencyUnitTransitionUnsupported
		}
		return nil, err
	}
	params.TenantID = tenantID
	wallet, err := s.GetWalletByOwner(ctx, tenantID, params.OwnerType, params.OwnerID, params.Currency)
	if err != nil {
		if !errors.Is(err, ErrWalletNotFound) || params.OwnerType != OwnerTypeUser {
			return nil, err
		}
		wallet, err = s.getWalletByUser(ctx, tenantID, params.UserID, params.Currency)
		if err != nil {
			return nil, err
		}
	}
	if err := ValidateEnsureWalletReplay(wallet, params); err != nil {
		return nil, err
	}
	return wallet, nil
}

func (s *Store) EnsureSystemWallets(ctx context.Context, tenantID, currency string, currencyUnitID int64, kycTier string) (map[string]*Wallet, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if _, err := ValidateCurrencyCode(currency); err != nil {
		return nil, err
	}
	if err := ValidateCurrencyUnitID(currencyUnitID); err != nil {
		return nil, err
	}
	if kycTier == "" {
		return nil, ErrMissingKYCTier
	}
	systemWallets := make(map[string]*Wallet, len(SystemWalletCodes()))
	for _, code := range SystemWalletCodes() {
		w, err := s.EnsureWallet(ctx, EnsureWalletParams{
			TenantID:       tenantID,
			OwnerType:      OwnerTypeSystem,
			OwnerID:        code,
			Currency:       currency,
			CurrencyUnitID: currencyUnitID,
			KYCTier:        kycTier,
		})
		if err != nil {
			return nil, err
		}
		systemWallets[code] = w
	}
	return systemWallets, nil
}

func (s *Store) GetWallet(ctx context.Context, tenantID string, walletID uuid.UUID) (*Wallet, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if walletID == uuid.Nil {
		return nil, ErrMissingWalletID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND id = ?")
	var w Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, walletID); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &w, nil
}

func (s *Store) GetWalletByOwner(ctx context.Context, tenantID, ownerType, ownerID, currency string) (*Wallet, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if ownerType == "" {
		return nil, ErrMissingOwnerType
	}
	if !OwnerTypeValid(ownerType) {
		return nil, ErrInvalidOwnerType
	}
	if ownerID == "" {
		return nil, ErrMissingOwnerID
	}
	if _, err := ValidateCurrencyCode(currency); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM wallets WHERE tenant_id = ? AND owner_type = ? AND owner_id = ? AND currency = ?")
	var w Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, ownerType, ownerID, currency); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &w, nil
}

func (s *Store) getWalletByUser(ctx context.Context, tenantID string, userID int64, currency string) (*Wallet, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind(`SELECT * FROM wallets
		WHERE tenant_id = ? AND owner_type = ? AND user_id = ? AND currency = ?`)
	var w Wallet
	if err := db.GetContext(ctx, &w, stmt, tenantID, OwnerTypeUser, userID, currency); err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrWalletNotFound
		}
		return nil, err
	}
	return &w, nil
}

func ValidateEnsureWalletReplay(existing *Wallet, params EnsureWalletParams) error {
	if existing == nil ||
		existing.TenantID != params.TenantID ||
		existing.OwnerType != params.OwnerType ||
		existing.OwnerID != params.OwnerID ||
		existing.Currency != params.Currency ||
		existing.CurrencyUnitID != params.CurrencyUnitID ||
		existing.KYCTier != params.KYCTier {
		return ErrDuplicateWallet
	}
	if params.OwnerType == OwnerTypeUser {
		if !existing.UserID.Valid || existing.UserID.Int64 != params.UserID {
			return ErrDuplicateWallet
		}
		return nil
	}
	if existing.UserID.Valid || params.UserID != 0 {
		return ErrDuplicateWallet
	}
	return nil
}
