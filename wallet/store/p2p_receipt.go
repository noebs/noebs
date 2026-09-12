package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var ErrP2PFeeChanged = errors.New("p2p_fee_changed")
var ErrP2PCommandFailed = errors.New("p2p_command_failed")

func ValidateP2PExpectation(fee, unit *int64, actualFee, actualUnit int64) error {
	if fee != nil && (*fee < 0 || *fee != actualFee) {
		return ErrP2PFeeChanged
	}
	if unit != nil && (*unit <= 0 || *unit != actualUnit) {
		return ErrCurrencyMismatch
	}
	return nil
}

// Only ledger-owned routing facts are returned. Canonical names are resolved by
// the gateway through identity-auth; this database never reads identity tables.
type P2PRecipient struct {
	WalletID       uuid.UUID
	OwnerID        string
	Currency       string
	CurrencyUnitID int64
}

func (s *Store) GetP2PRecipient(ctx context.Context, tenant string, id uuid.UUID) (*P2PRecipient, error) {
	w, err := s.GetWallet(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	if w.Status != WalletStatusActive || w.OwnerType != OwnerTypeUser || !w.UserID.Valid || w.UserID.Int64 <= 0 || w.OwnerID != strconv.FormatInt(w.UserID.Int64, 10) {
		return nil, ErrWalletNotFound
	}
	return &P2PRecipient{WalletID: w.ID, OwnerID: w.OwnerID, Currency: w.Currency, CurrencyUnitID: w.CurrencyUnitID}, nil
}

type P2PReceipt struct {
	Command        *P2PCommand
	Payload        P2PCommandPayload
	Status         string
	ErrorCode      string
	TransactionID  int64
	Fee            int64
	CurrencyUnitID int64
	CreatedAt      time.Time
}

// A receipt proves completion from the committed ledger, not workflow state.
func (s *Store) GetP2PReceipt(ctx context.Context, tenant, owner, key string) (*P2PReceipt, error) {
	if owner == "" {
		return nil, ErrMissingOwnerID
	}
	c, err := s.GetP2PCommand(ctx, tenant, key)
	if err != nil {
		return nil, err
	}
	if c.FromOwnerType != OwnerTypeUser || c.FromOwnerID != owner {
		return nil, ErrP2PCommandNotFound
	}
	p, err := DecodeP2PCommand(c, tenant, key, c.WorkflowID)
	if err != nil {
		return nil, err
	}
	r := &P2PReceipt{Command: c, Payload: p, Status: "reserved", CreatedAt: c.CreatedAt}
	if c.RunID.Valid {
		r.Status = "running"
	}
	if p.ExpectedFeeAmount != nil {
		r.Fee = *p.ExpectedFeeAmount
	}
	if p.ExpectedCurrencyUnitVersion != nil {
		r.CurrencyUnitID = *p.ExpectedCurrencyUnitVersion
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	// The complete principal leg and matching command reference are required.
	var settled struct {
		TransactionID int64 `db:"id"`
		Unit          int64 `db:"currency_unit_version_id"`
		Debit         int64 `db:"total_debit"`
	}
	query := db.Rebind(`SELECT lt.id, lt.currency_unit_version_id,
	 (SELECT SUM(e.amount) FROM ledger_entries e WHERE e.tenant_id=lt.tenant_id AND e.transaction_id=lt.id AND e.wallet_id=? AND e.entry_type='debit' AND e.status='completed') AS total_debit
	 FROM ledger_transactions lt WHERE lt.tenant_id=? AND lt.idempotency_key=? AND lt.status='completed' AND lt.reference_type='p2p' AND lt.reference_id=? AND lt.currency=?
	 AND EXISTS(SELECT 1 FROM ledger_entries e WHERE e.tenant_id=lt.tenant_id AND e.transaction_id=lt.id AND e.wallet_id=? AND e.entry_type='credit' AND e.amount=? AND e.status='completed')`)
	err = db.GetContext(ctx, &settled, query, c.FromWalletID, tenant, key, p.ReferenceID, p.Currency, c.ToWalletID, p.Amount)
	if err == nil {
		if settled.Debit < p.Amount {
			return nil, ErrInvalidP2PCommand
		}
		fee := settled.Debit - p.Amount
		if err := ValidateP2PExpectation(p.ExpectedFeeAmount, p.ExpectedCurrencyUnitVersion, fee, settled.Unit); err != nil {
			return nil, err
		}
		r.Status = "completed"
		r.TransactionID = settled.TransactionID
		r.Fee = fee
		r.CurrencyUnitID = settled.Unit
		return r, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	err = db.GetContext(ctx, &r.ErrorCode, db.Rebind(`SELECT error_code FROM p2p_command_failures WHERE tenant_id=? AND idempotency_key=?`), tenant, key)
	if err == nil {
		r.Status = "failed"
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if r.CurrencyUnitID == 0 {
		w, e := s.GetWallet(ctx, tenant, c.FromWalletID)
		if e != nil {
			return nil, e
		}
		r.CurrencyUnitID = w.CurrencyUnitID
	}
	return r, nil
}

// An advisory transaction lock is shared with posting. It needs no UPDATE
// privilege on the immutable command and serializes late activities with failure.
func lockP2PCommand(ctx context.Context, tx *sqlx.Tx, tenant, key string) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "p2p:"+tenant+":"+key)
	return err
}

func (s *Store) RecordP2PFailure(ctx context.Context, tenant, key, code string) error {
	if code != "payment_not_completed" && code != "p2p_fee_changed" && code != "currency_mismatch" {
		return ErrInvalidP2PCommand
	}
	c, err := s.GetP2PCommand(ctx, tenant, key)
	if err != nil {
		return err
	}
	p, err := DecodeP2PCommand(c, tenant, key, c.WorkflowID)
	if err != nil {
		return err
	}
	if p.ExpectedFeeAmount == nil {
		return ErrInvalidP2PCommand
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockP2PCommand(ctx, tx, tenant, key); err != nil {
		return err
	}
	var exists bool
	err = tx.GetContext(ctx, &exists, db.Rebind(`SELECT EXISTS(SELECT 1 FROM ledger_transactions lt WHERE lt.tenant_id=? AND lt.idempotency_key=? AND lt.reference_type='p2p' AND lt.reference_id=? AND lt.currency=? AND lt.status='completed'
 AND EXISTS(SELECT 1 FROM ledger_entries e WHERE e.tenant_id=lt.tenant_id AND e.transaction_id=lt.id AND e.wallet_id=? AND e.entry_type='credit' AND e.amount=? AND e.status='completed')
 AND EXISTS(SELECT 1 FROM ledger_entries e WHERE e.tenant_id=lt.tenant_id AND e.transaction_id=lt.id AND e.wallet_id=? AND e.entry_type='debit' AND e.amount=? AND e.status='completed'))`), tenant, key, p.ReferenceID, p.Currency, c.ToWalletID, p.Amount, c.FromWalletID, p.Amount)
	if err != nil {
		return err
	}
	if exists {
		return tx.Commit()
	}
	_, err = tx.ExecContext(ctx, db.Rebind(`INSERT INTO p2p_command_failures(tenant_id,idempotency_key,error_code) VALUES(?,?,?) ON CONFLICT DO NOTHING`), c.TenantID, c.IdempotencyKey, code)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) validateP2PSettlement(ctx context.Context, tx *sqlx.Tx, params MultiLegSettlementParams, wallets map[uuid.UUID]*Wallet) error {
	var c P2PCommand
	err := tx.GetContext(ctx, &c, s.DB.Rebind(`SELECT tenant_id,idempotency_key,workflow_id,from_wallet_id,to_wallet_id,from_owner_type,from_owner_id,to_owner_type,to_owner_id,command,run_id,created_at FROM p2p_commands WHERE tenant_id=? AND idempotency_key=?`), params.TenantID, params.P2PCommandID)
	if err != nil {
		return err
	}
	p, err := DecodeP2PCommand(&c, params.TenantID, params.P2PCommandID, c.WorkflowID)
	if err != nil {
		return err
	}
	if params.ReferenceType != "p2p" || params.IdempotencyKey != c.IdempotencyKey || params.ReferenceID != p.ReferenceID || params.Currency != p.Currency || len(params.Transfers) < 1 || len(params.Transfers) > 2 {
		return ErrInvalidP2PCommand
	}
	if params.LimitUsage.WalletID != c.FromWalletID || params.LimitUsage.CommandID != "p2p:"+c.IdempotencyKey || params.LimitUsage.TransactionType != "p2p" || params.LimitUsage.Amount != p.Amount {
		return ErrInvalidP2PCommand
	}
	first := params.Transfers[0]
	if first.DebitWalletID != c.FromWalletID || first.CreditWalletID != c.ToWalletID || first.Amount != p.Amount {
		return ErrInvalidP2PCommand
	}
	from, to := wallets[c.FromWalletID], wallets[c.ToWalletID]
	if from == nil || to == nil || from.OwnerType != c.FromOwnerType || from.OwnerID != c.FromOwnerID || to.OwnerType != c.ToOwnerType || to.OwnerID != c.ToOwnerID {
		return ErrInvalidP2PCommand
	}
	if p.ExpectedFeeAmount != nil && (from.OwnerType != OwnerTypeUser || to.OwnerType != OwnerTypeUser || !from.UserID.Valid || !to.UserID.Valid || from.OwnerID != strconv.FormatInt(from.UserID.Int64, 10) || to.OwnerID != strconv.FormatInt(to.UserID.Int64, 10)) {
		return ErrInvalidP2PCommand
	}
	fee := int64(0)
	if len(params.Transfers) == 2 {
		f := params.Transfers[1]
		fees := wallets[f.CreditWalletID]
		if f.DebitWalletID != c.FromWalletID || fees == nil || fees.OwnerType != OwnerTypeSystem || fees.OwnerID != SystemFees {
			return ErrInvalidP2PCommand
		}
		fee = f.Amount
	}
	if err = ValidateP2PExpectation(p.ExpectedFeeAmount, p.ExpectedCurrencyUnitVersion, fee, from.CurrencyUnitID); err != nil {
		return err
	}
	var failed bool
	err = tx.GetContext(ctx, &failed, s.DB.Rebind(`SELECT EXISTS(SELECT 1 FROM p2p_command_failures WHERE tenant_id=? AND idempotency_key=?)`), params.TenantID, params.P2PCommandID)
	if err != nil {
		return err
	}
	if failed {
		return ErrP2PCommandFailed
	}
	return nil
}
