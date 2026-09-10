package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInteropDisabled     = errors.New("interop_disabled")
	ErrInteropInvalid      = errors.New("invalid_interop_request")
	ErrInteropConflict     = errors.New("interop_idempotency_conflict")
	ErrInteropNotFound     = errors.New("interop_not_found")
	ErrInteropQuoteExpired = errors.New("interop_quote_expired")
	ErrInteropState        = errors.New("invalid_interop_state")
)

const (
	SystemMojaloopClearing = "mojaloop_clearing"
	SystemMojaloopSuspense = "mojaloop_suspense"
)

type InteropBinding struct {
	TenantID         string    `db:"tenant_id"`
	FSPID            string    `db:"fsp_id"`
	Currency         string    `db:"currency"`
	CurrencyUnitID   int64     `db:"currency_unit_version_id"`
	ClearingWalletID uuid.UUID `db:"clearing_wallet_id"`
	SuspenseWalletID uuid.UUID `db:"suspense_wallet_id"`
	Enabled          bool      `db:"enabled"`
}

type InteropAlias struct {
	TenantID    string    `db:"tenant_id"`
	Identifier  string    `db:"identifier"`
	WalletID    uuid.UUID `db:"wallet_id"`
	DisplayName string    `db:"display_name"`
}

type InteropQuote struct {
	ID             uuid.UUID      `db:"id"`
	TransferID     uuid.UUID      `db:"transfer_id"`
	TenantID       string         `db:"tenant_id"`
	OwnerID        string         `db:"owner_id"`
	WalletID       uuid.UUID      `db:"wallet_id"`
	Direction      string         `db:"direction"`
	IdempotencyKey string         `db:"idempotency_key"`
	Amount         int64          `db:"amount"`
	Currency       string         `db:"currency"`
	CurrencyUnitID int64          `db:"currency_unit_version_id"`
	Request        RawJSON        `db:"request"`
	Response       RawJSON        `db:"response"`
	SDKState       RawJSON        `db:"sdk_state"`
	Status         string         `db:"status"`
	ExpiresAt      sql.NullTime   `db:"expires_at"`
	ErrorCode      sql.NullString `db:"error_code"`
	LeaseUntil     sql.NullTime   `db:"lease_until"`
	LeaseToken     uuid.NullUUID  `db:"lease_token"`
	CreatedAt      time.Time      `db:"created_at"`
}

type InteropTransfer struct {
	ID                  uuid.UUID      `db:"id"`
	TenantID            string         `db:"tenant_id"`
	QuoteID             uuid.UUID      `db:"quote_id"`
	OwnerID             string         `db:"owner_id"`
	IdempotencyKey      string         `db:"idempotency_key"`
	Status              string         `db:"status"`
	HubState            string         `db:"hub_state"`
	HoldID              sql.NullInt64  `db:"hold_id"`
	LedgerTransactionID sql.NullInt64  `db:"ledger_transaction_id"`
	OriginalResponse    RawJSON        `db:"original_response"`
	OriginalPrepare     RawJSON        `db:"original_prepare"`
	SubmittedAt         sql.NullTime   `db:"submitted_at"`
	LeaseToken          uuid.NullUUID  `db:"lease_token"`
	LeaseUntil          sql.NullTime   `db:"lease_until"`
	NextAttemptAt       time.Time      `db:"next_attempt_at"`
	ErrorCode           sql.NullString `db:"error_code"`
	CreatedAt           time.Time      `db:"created_at"`
	UpdatedAt           time.Time      `db:"updated_at"`
}

func (s *Store) GetInteropBinding(ctx context.Context, tenantID string) (*InteropBinding, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var b InteropBinding
	err = db.GetContext(ctx, &b, db.Rebind(`SELECT * FROM interop_bindings WHERE tenant_id = ?`), tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInteropDisabled
	}
	return &b, err
}

// ValidateInteropAdmission checks explicit monetary configuration before an
// external obligation is accepted. Binding identities are immutable in SQL.
func (s *Store) ValidateInteropAdmission(ctx context.Context, b *InteropBinding) error {
	if b == nil || !b.Enabled {
		return ErrInteropDisabled
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	var valid bool
	if err = db.GetContext(ctx, &valid, db.Rebind(`SELECT iso_minor_exponent=2 AND display_exponent=2 FROM currency_unit_versions WHERE id=? AND currency_code='SDG'`), b.CurrencyUnitID); err != nil {
		return err
	}
	if !valid {
		return ErrInteropInvalid
	}
	for id, code := range map[uuid.UUID]string{b.ClearingWalletID: SystemMojaloopClearing, b.SuspenseWalletID: SystemMojaloopSuspense} {
		w, err := s.GetWallet(ctx, b.TenantID, id)
		if err != nil {
			return err
		}
		if w.OwnerType != OwnerTypeSystem || w.OwnerID != code || w.Currency != b.Currency || w.CurrencyUnitID != b.CurrencyUnitID || w.Status != WalletStatusActive {
			return ErrInteropState
		}
	}
	return nil
}

func (s *Store) GetInteropAlias(ctx context.Context, tenantID string, walletID uuid.UUID, identifier string) (*InteropAlias, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if (walletID == uuid.Nil) == (identifier == "") {
		return nil, ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var a InteropAlias
	if walletID != uuid.Nil {
		err = db.GetContext(ctx, &a, db.Rebind(`SELECT * FROM interop_aliases WHERE tenant_id = ? AND wallet_id = ?`), tenantID, walletID)
	} else {
		err = db.GetContext(ctx, &a, db.Rebind(`SELECT * FROM interop_aliases WHERE tenant_id = ? AND identifier = ?`), tenantID, identifier)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInteropNotFound
	}
	return &a, err
}

func ValidateInteropQuote(q InteropQuote) error {
	if _, err := ValidateTenantID(q.TenantID); err != nil {
		return err
	}
	if q.ID == uuid.Nil || q.TransferID == uuid.Nil || q.WalletID == uuid.Nil || q.OwnerID == "" {
		return ErrInteropInvalid
	}
	if q.IdempotencyKey == "" || len(q.IdempotencyKey) > 256 || strings.TrimSpace(q.IdempotencyKey) != q.IdempotencyKey {
		return ErrMissingIdempotencyKey
	}
	if q.Amount <= 0 {
		return ErrInvalidAmount
	}
	if q.Currency == "" {
		return ErrMissingCurrency
	}
	if err := ValidateCurrencyUnitID(q.CurrencyUnitID); err != nil {
		return err
	}
	if !json.Valid(q.Request) {
		return ErrInteropInvalid
	}
	if q.Direction == "OUT" {
		if q.Status != "REQUESTED" || len(q.Response) != 0 || len(q.SDKState) != 0 || q.ExpiresAt.Valid {
			return ErrInteropInvalid
		}
	} else if q.Direction == "IN" {
		if q.Status != "READY" || !json.Valid(q.Response) || !q.ExpiresAt.Valid {
			return ErrInteropInvalid
		}
	} else {
		return ErrInteropInvalid
	}
	return nil
}

// CreateInteropQuote records immutable intent. Worker authority performs remote
// discovery and agreement; no customer funds are moved by this method.
func (s *Store) CreateInteropQuote(ctx context.Context, q InteropQuote) (*InteropQuote, error) {
	if err := ValidateInteropQuote(q); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	// Recover a semantic retry before testing current eligibility/expiry.
	var old InteropQuote
	err = db.GetContext(ctx, &old, db.Rebind(`SELECT * FROM interop_quotes WHERE tenant_id=? AND owner_id=? AND direction=? AND idempotency_key=?`), q.TenantID, q.OwnerID, q.Direction, q.IdempotencyKey)
	if err == nil {
		return compareInteropQuote(old, q)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	b, err := s.GetInteropBinding(ctx, q.TenantID)
	if err != nil {
		return nil, err
	}
	if !b.Enabled {
		return nil, ErrInteropDisabled
	}
	w, err := s.GetWallet(ctx, q.TenantID, q.WalletID)
	if err != nil {
		return nil, err
	}
	if w.OwnerType != OwnerTypeUser || w.OwnerID != q.OwnerID {
		return nil, ErrInteropNotFound
	}
	if w.Status != WalletStatusActive {
		return nil, ErrWalletInactive
	}
	if w.Currency != q.Currency || b.Currency != q.Currency {
		return nil, ErrCurrencyMismatch
	}
	if w.CurrencyUnitID != q.CurrencyUnitID || b.CurrencyUnitID != q.CurrencyUnitID {
		return nil, ErrInvalidCurrencyUnitID
	}
	query := db.Rebind(`INSERT INTO interop_quotes(id,transfer_id,tenant_id,owner_id,wallet_id,direction,idempotency_key,amount,currency,currency_unit_version_id,request,response,sdk_state,status,expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING RETURNING *`)
	if q.Direction == "OUT" {
		err = db.GetContext(ctx, &old, db.Rebind(`INSERT INTO interop_quotes(id,transfer_id,tenant_id,owner_id,wallet_id,idempotency_key,amount,currency,currency_unit_version_id,request) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING RETURNING *`), q.ID, q.TransferID, q.TenantID, q.OwnerID, q.WalletID, q.IdempotencyKey, q.Amount, q.Currency, q.CurrencyUnitID, q.Request)
	} else {
		err = db.GetContext(ctx, &old, query, q.ID, q.TransferID, q.TenantID, q.OwnerID, q.WalletID, q.Direction, q.IdempotencyKey, q.Amount, q.Currency, q.CurrencyUnitID, q.Request, q.Response, q.SDKState, q.Status, q.ExpiresAt)
	}
	if errors.Is(err, sql.ErrNoRows) {
		err = db.GetContext(ctx, &old, db.Rebind(`SELECT * FROM interop_quotes WHERE tenant_id=? AND owner_id=? AND direction=? AND idempotency_key=?`), q.TenantID, q.OwnerID, q.Direction, q.IdempotencyKey)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInteropConflict
		}
		if err == nil {
			return compareInteropQuote(old, q)
		}
	}
	return &old, err
}

func compareInteropQuote(old, q InteropQuote) (*InteropQuote, error) {
	if old.WalletID != q.WalletID || old.Amount != q.Amount || old.Currency != q.Currency || old.CurrencyUnitID != q.CurrencyUnitID || !interopJSONEqual(old.Request, q.Request) {
		return nil, ErrInteropConflict
	}
	if q.Direction == "IN" && (old.ID != q.ID || old.TransferID != q.TransferID) {
		return nil, ErrInteropConflict
	}
	return &old, nil
}

func interopJSONEqual(a, b []byte) bool {
	var left, right any
	d1, d2 := json.NewDecoder(bytes.NewReader(a)), json.NewDecoder(bytes.NewReader(b))
	d1.UseNumber()
	d2.UseNumber()
	return d1.Decode(&left) == nil && d2.Decode(&right) == nil && reflect.DeepEqual(left, right)
}

func (s *Store) GetInteropQuote(ctx context.Context, tenantID string, id uuid.UUID) (*InteropQuote, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var q InteropQuote
	err = db.GetContext(ctx, &q, db.Rebind(`SELECT * FROM interop_quotes WHERE tenant_id=? AND id=?`), tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInteropNotFound
	}
	return &q, err
}

func (s *Store) GetInteropTransfer(ctx context.Context, tenantID, ownerID string, id uuid.UUID, key string) (*InteropTransfer, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if ownerID == "" || (id == uuid.Nil) == (key == "") {
		return nil, ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var t InteropTransfer
	if id != uuid.Nil {
		err = db.GetContext(ctx, &t, db.Rebind(`SELECT * FROM interop_transfers WHERE tenant_id=? AND owner_id=? AND id=?`), tenantID, ownerID, id)
	} else {
		err = db.GetContext(ctx, &t, db.Rebind(`SELECT * FROM interop_transfers WHERE tenant_id=? AND owner_id=? AND idempotency_key=?`), tenantID, ownerID, key)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInteropNotFound
	}
	return &t, err
}

// RequestInteropTransfer admits an authorized command only. It cannot post funds.
// The worker atomically arms its balance/usage reservations before SDK dispatch.
func (s *Store) RequestInteropTransfer(ctx context.Context, tenantID, ownerID string, quoteID uuid.UUID, key string) (*InteropTransfer, error) {
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	if ownerID == "" || quoteID == uuid.Nil || key == "" || len(key) > 256 || strings.TrimSpace(key) != key {
		return nil, ErrInteropInvalid
	}
	old, err := s.GetInteropTransfer(ctx, tenantID, ownerID, uuid.Nil, key)
	if err == nil {
		if old.QuoteID != quoteID {
			return nil, ErrInteropConflict
		}
		return old, nil
	}
	if !errors.Is(err, ErrInteropNotFound) {
		return nil, err
	}
	q, err := s.GetInteropQuote(ctx, tenantID, quoteID)
	if err != nil {
		return nil, err
	}
	if q.OwnerID != ownerID || q.Direction != "OUT" {
		return nil, ErrInteropNotFound
	}
	if q.Status != "READY" {
		return nil, ErrInteropState
	}
	b, err := s.GetInteropBinding(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if !b.Enabled {
		return nil, ErrInteropDisabled
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var t InteropTransfer
	err = db.GetContext(ctx, &t, db.Rebind(`INSERT INTO interop_transfers(id,tenant_id,quote_id,owner_id,idempotency_key)
		SELECT transfer_id,tenant_id,id,owner_id,? FROM interop_quotes WHERE tenant_id=? AND id=? AND direction='OUT' AND status='READY' AND expires_at>clock_timestamp()+interval '5 seconds'
		ON CONFLICT DO NOTHING RETURNING *`), key, tenantID, quoteID)
	if errors.Is(err, sql.ErrNoRows) {
		old, readErr := s.GetInteropTransfer(ctx, tenantID, ownerID, uuid.Nil, key)
		if readErr == nil && old.QuoteID == quoteID {
			return old, nil
		}
		if q.ExpiresAt.Valid && !q.ExpiresAt.Time.After(time.Now().Add(5*time.Second)) {
			return nil, ErrInteropQuoteExpired
		}
		return nil, ErrInteropConflict
	}
	return &t, err
}

func (s *Store) ClaimInteropQuote(ctx context.Context, tenantID string, token uuid.UUID) (*InteropQuote, error) {
	if token == uuid.Nil {
		return nil, ErrInteropInvalid
	}
	if _, err := ValidateTenantID(tenantID); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var q InteropQuote
	err = db.GetContext(ctx, &q, db.Rebind(`UPDATE interop_quotes SET lease_token=?,status='QUOTING',lease_until=clock_timestamp()+interval '90 seconds'
		WHERE id=(SELECT id FROM interop_quotes WHERE tenant_id=? AND direction='OUT' AND status IN ('REQUESTED','QUOTING') AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY created_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *`), token, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInteropNotFound
	}
	return &q, err
}

func (s *Store) CompleteInteropQuote(ctx context.Context, tenantID string, id, token uuid.UUID, response, state RawJSON, expires time.Time, errorCode string) error {
	if err := validateInteropWork(tenantID, id, token); err != nil {
		return err
	}
	if errorCode == "" && (!json.Valid(response) || !json.Valid(state) || expires.IsZero()) {
		return ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	var result sql.Result
	if errorCode != "" {
		result, err = db.ExecContext(ctx, db.Rebind(`UPDATE interop_quotes SET status='FAILED',error_code=?,lease_until=NULL,lease_token=NULL WHERE tenant_id=? AND id=? AND status='QUOTING' AND lease_token=? AND lease_until>clock_timestamp()`), errorCode, tenantID, id, token)
	} else {
		result, err = db.ExecContext(ctx, db.Rebind(`UPDATE interop_quotes SET status='READY',response=?,sdk_state=?,expires_at=?,lease_until=NULL,lease_token=NULL WHERE tenant_id=? AND id=? AND status='QUOTING' AND lease_token=? AND lease_until>clock_timestamp()`), response, state, expires, tenantID, id, token)
	}
	if err != nil {
		return err
	}
	if err = requireOneRow(result); err != nil {
		return ErrInteropState
	}
	return nil
}

func (s *Store) GetInteropQuoteByTransfer(ctx context.Context, tenant string, id uuid.UUID) (*InteropQuote, error) {
	if _, err := ValidateTenantID(tenant); err != nil {
		return nil, err
	}
	if id == uuid.Nil {
		return nil, ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var q InteropQuote
	err = db.GetContext(ctx, &q, db.Rebind(`SELECT * FROM interop_quotes WHERE tenant_id=? AND transfer_id=?`), tenant, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInteropNotFound
	}
	return &q, err
}
