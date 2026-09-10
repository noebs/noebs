package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Interop records are a transactional outbox. Lease ownership controls work;
// immutable transfer identifiers and conditional transitions control effects.
func (s *Store) ClaimInteropTransfer(ctx context.Context, tenant string, token uuid.UUID) (*InteropTransfer, error) {
	if _, err := ValidateTenantID(tenant); err != nil {
		return nil, err
	}
	if token == uuid.Nil {
		return nil, ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var t InteropTransfer
	err = db.GetContext(ctx, &t, db.Rebind(`UPDATE interop_transfers SET lease_token=?,lease_until=clock_timestamp()+interval '90 seconds'
 WHERE id=(SELECT id FROM interop_transfers WHERE tenant_id=? AND status IN ('REQUESTED','ARMED','PENDING','IN_DOUBT')
 AND next_attempt_at<=clock_timestamp() AND (lease_until IS NULL OR lease_until<clock_timestamp()) ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 1) RETURNING *`), token, tenant)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInteropNotFound
	}
	return &t, err
}

func (s *Store) lockInterop(ctx context.Context, tx *sqlx.Tx, tenant string, id uuid.UUID) (*InteropTransfer, *InteropQuote, error) {
	var t InteropTransfer
	var q InteropQuote
	err := tx.GetContext(ctx, &t, s.DB.Rebind(`SELECT * FROM interop_transfers WHERE tenant_id=? AND id=? FOR UPDATE`), tenant, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrInteropNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	err = tx.GetContext(ctx, &q, s.DB.Rebind(`SELECT * FROM interop_quotes WHERE tenant_id=? AND id=?`), tenant, t.QuoteID)
	return &t, &q, err
}

func interopUsage(q *InteropQuote) LimitUsageParams {
	return LimitUsageParams{TenantID: q.TenantID, CommandID: "mojaloop:" + q.TransferID.String(), WalletID: q.WalletID, TransactionType: "interop_" + strings.ToLower(q.Direction), Currency: q.Currency, Amount: q.Amount}
}

func validateInteropWork(tenant string, id, token uuid.UUID) error {
	if _, err := ValidateTenantID(tenant); err != nil {
		return err
	}
	if id == uuid.Nil || token == uuid.Nil {
		return ErrInteropInvalid
	}
	return nil
}

// ArmInteropTransfer reserves BOTH usage and available balance and records the
// obligation in one commit. A committed hold is not subject to the TTL sweeper.
func (s *Store) ArmInteropTransfer(ctx context.Context, tenant string, id, token uuid.UUID) (*InteropTransfer, error) {
	if err := validateInteropWork(tenant, id, token); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	t, q, err := s.lockInterop(ctx, tx, tenant, id)
	if err != nil {
		return nil, err
	}
	if t.LeaseToken.UUID != token || !t.LeaseToken.Valid {
		return nil, ErrInteropState
	}
	if t.Status != "REQUESTED" {
		return t, tx.Commit()
	}
	if q.Direction != "OUT" || q.Status != "READY" {
		return nil, ErrInteropState
	}
	var now time.Time
	if err = tx.GetContext(ctx, &now, `SELECT clock_timestamp()`); err != nil {
		return nil, err
	}
	if !q.ExpiresAt.Valid || !q.ExpiresAt.Time.After(now.Add(5*time.Second)) {
		return nil, ErrInteropQuoteExpired
	}
	b, err := s.GetInteropBinding(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if err = s.ValidateInteropAdmission(ctx, b); err != nil {
		return nil, err
	}
	w, err := s.lockWallet(ctx, tx, tenant, q.WalletID)
	if err != nil {
		return nil, err
	}
	if w.OwnerType != OwnerTypeUser || w.OwnerID != q.OwnerID || w.Currency != q.Currency || w.CurrencyUnitID != q.CurrencyUnitID || b.CurrencyUnitID != q.CurrencyUnitID {
		return nil, ErrInteropState
	}
	if w.AvailableBalance < q.Amount {
		return nil, ErrInsufficientFunds
	}
	if _, err = s.reserveLimitUsageInTx(ctx, tx, interopUsage(q), w); err != nil {
		return nil, err
	}
	var holdID int64
	err = tx.GetContext(ctx, &holdID, db.Rebind(`INSERT INTO balance_holds(tenant_id,wallet_id,amount,amount_remaining,reason,reference_type,reference_id,idempotency_key,status,expires_at,created_at,committed_at)
 VALUES(?,?,?,?,'Mojaloop transfer','mojaloop',?,?,'committed',?,?,?) RETURNING id`), tenant, q.WalletID, q.Amount, q.Amount, id.String(), "mojaloop:"+id.String(), q.ExpiresAt.Time, now, now)
	if err != nil {
		return nil, err
	}
	if err = s.updateWalletBalance(ctx, tx, tenant, w.ID, w.Balance, w.AvailableBalance-q.Amount, now); err != nil {
		return nil, err
	}
	err = tx.GetContext(ctx, t, db.Rebind(`UPDATE interop_transfers SET hold_id=?,status='ARMED',updated_at=clock_timestamp() WHERE tenant_id=? AND id=? RETURNING *`), holdID, tenant, id)
	if err != nil {
		return nil, err
	}
	return t, tx.Commit()
}

// MarkInteropSubmitted must commit BEFORE the first byte of SDK acceptance can
// be sent. Any later failure, including a crash before send, requires hub query.
func (s *Store) MarkInteropSubmitted(ctx context.Context, tenant string, id, token uuid.UUID, prepare RawJSON) error {
	if err := validateInteropWork(tenant, id, token); err != nil {
		return err
	}
	if !json.Valid(prepare) {
		return ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, db.Rebind(`UPDATE interop_transfers SET status='PENDING',submitted_at=clock_timestamp(),original_prepare=?,updated_at=clock_timestamp()
 WHERE tenant_id=? AND id=? AND lease_token=? AND lease_until>clock_timestamp() AND status='ARMED' AND submitted_at IS NULL
 AND EXISTS(SELECT 1 FROM interop_quotes q WHERE q.id=quote_id AND q.expires_at>clock_timestamp()+interval '5 seconds')`), prepare, tenant, id, token)
	if err != nil {
		return err
	}
	if err = requireOneRow(result); err != nil {
		return ErrInteropState
	}
	return nil
}

func (s *Store) DeferInteropTransfer(ctx context.Context, tenant string, id, token uuid.UUID, code string) error {
	if err := validateInteropWork(tenant, id, token); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, db.Rebind(`UPDATE interop_transfers SET status=CASE WHEN submitted_at IS NOT NULL OR original_response IS NOT NULL THEN 'IN_DOUBT' ELSE status END,
 error_code=?,next_attempt_at=clock_timestamp()+interval '10 seconds',lease_until=NULL,lease_token=NULL,updated_at=clock_timestamp()
 WHERE tenant_id=? AND id=? AND lease_token=? AND status IN ('REQUESTED','ARMED','PENDING','IN_DOUBT')`), code, tenant, id, token)
	return err
}

// ReserveIncoming persists the exact callback request and ORIGINAL response.
// It never credits spendable money. Callback retries replay byte-equivalent JSON
// even after commitment; matching only an identifier is deliberately insufficient.
func (s *Store) ReserveInteropIncoming(ctx context.Context, tenant string, quoteID uuid.UUID, prepare, response RawJSON, prepareExpires time.Time) (RawJSON, error) {
	if _, err := ValidateTenantID(tenant); err != nil {
		return nil, err
	}
	if quoteID == uuid.Nil || !json.Valid(prepare) || !json.Valid(response) || prepareExpires.IsZero() {
		return nil, ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var q InteropQuote
	if err = tx.GetContext(ctx, &q, db.Rebind(`SELECT * FROM interop_quotes WHERE tenant_id=? AND id=? FOR UPDATE`), tenant, quoteID); err != nil {
		return nil, err
	}
	if q.Direction != "IN" || q.Status != "READY" {
		return nil, ErrInteropState
	}
	var t InteropTransfer
	err = tx.GetContext(ctx, &t, db.Rebind(`SELECT * FROM interop_transfers WHERE tenant_id=? AND id=? FOR UPDATE`), tenant, q.TransferID)
	if err == nil {
		if !interopJSONEqual(prepare, t.OriginalPrepare) {
			return nil, ErrInteropConflict
		}
		return t.OriginalResponse, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var valid bool
	if err = tx.GetContext(ctx, &valid, db.Rebind(`SELECT expires_at>clock_timestamp() FROM interop_quotes WHERE id=?`), quoteID); err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrInteropQuoteExpired
	}
	b, err := s.GetInteropBinding(ctx, tenant)
	if err != nil {
		return nil, err
	}
	if err = s.ValidateInteropAdmission(ctx, b); err != nil {
		return nil, err
	}
	w, err := s.lockWallet(ctx, tx, tenant, q.WalletID)
	if err != nil {
		return nil, err
	}
	if w.OwnerType != OwnerTypeUser || w.OwnerID != q.OwnerID || w.CurrencyUnitID != q.CurrencyUnitID {
		return nil, ErrInteropState
	}
	if _, err = s.reserveLimitUsageInTx(ctx, tx, interopUsage(&q), w); err != nil {
		return nil, err
	}
	inserted, err := tx.ExecContext(ctx, db.Rebind(`INSERT INTO interop_transfers(id,tenant_id,quote_id,owner_id,idempotency_key,status,hub_state,original_prepare,original_response)
 SELECT ?,?,?,?,?,'PENDING','UNKNOWN',?,? WHERE clock_timestamp() < ? AND ? <= ?`), q.TransferID, tenant, q.ID, q.OwnerID, "incoming:"+q.TransferID.String(), prepare, response, prepareExpires, prepareExpires, q.ExpiresAt.Time)
	if err != nil {
		return nil, err
	}
	if err = requireOneRow(inserted); err != nil {
		return nil, ErrInteropQuoteExpired
	}
	return response, tx.Commit()
}

type InteropEvent struct {
	TenantID   string
	TransferID uuid.UUID
	Kind       string
	Authority  string
	Payload    RawJSON
}

// RecordInteropEvent is called only after protocol/provenance validation.
// Persistence precedes callback acknowledgement; settlement is retryable.
func (s *Store) RecordInteropEvent(ctx context.Context, event InteropEvent) (int64, error) {
	if _, err := ValidateTenantID(event.TenantID); err != nil {
		return 0, err
	}
	if event.TransferID == uuid.Nil || (event.Kind != "COMMITTED" && event.Kind != "ABORTED") || (event.Authority != "sdk-loopback" && event.Authority != "sdk-hub-query") || !json.Valid(event.Payload) {
		return 0, ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return 0, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	t, q, err := s.lockInterop(ctx, tx, event.TenantID, event.TransferID)
	if err != nil {
		return 0, err
	}
	if (q.Direction == "OUT" && !t.SubmittedAt.Valid) || (q.Direction == "IN" && len(t.OriginalResponse) == 0) {
		return 0, ErrInteropState
	}
	conflict := t.HubState != "UNKNOWN" && t.HubState != event.Kind
	digest := sha256.Sum256(event.Payload)
	var id int64
	err = tx.GetContext(ctx, &id, db.Rebind(`INSERT INTO interop_inbox(tenant_id,transfer_id,event_kind,authority,payload,payload_sha256,quarantined) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING RETURNING id`), event.TenantID, event.TransferID, event.Kind, event.Authority, event.Payload, hex.EncodeToString(digest[:]), conflict)
	if errors.Is(err, sql.ErrNoRows) {
		err = tx.GetContext(ctx, &id, db.Rebind(`SELECT id FROM interop_inbox WHERE tenant_id=? AND transfer_id=? AND event_kind=? AND payload_sha256=?`), event.TenantID, event.TransferID, event.Kind, hex.EncodeToString(digest[:]))
	}
	if err != nil {
		return 0, err
	}
	if !conflict && t.HubState == "UNKNOWN" {
		_, err = tx.ExecContext(ctx, db.Rebind(`UPDATE interop_transfers SET hub_state=?,updated_at=clock_timestamp() WHERE tenant_id=? AND id=?`), event.Kind, event.TenantID, event.TransferID)
		if err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	if conflict {
		return id, ErrInteropConflict
	}
	return id, nil
}

func (s *Store) PendingInteropEvents(ctx context.Context, tenant string) ([]int64, error) {
	if _, err := ValidateTenantID(tenant); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var ids []int64
	err = db.SelectContext(ctx, &ids, db.Rebind(`SELECT id FROM interop_inbox WHERE tenant_id=? AND applied_at IS NULL AND NOT quarantined AND next_attempt_at<=clock_timestamp() ORDER BY next_attempt_at,id LIMIT 100`), tenant)
	return ids, err
}

func (s *Store) ApplyInteropEvent(ctx context.Context, tenant string, eventID int64) error {
	if _, err := ValidateTenantID(tenant); err != nil {
		return err
	}
	if eventID <= 0 {
		return ErrInteropInvalid
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
	var e struct {
		TransferID uuid.UUID    `db:"transfer_id"`
		Kind       string       `db:"event_kind"`
		Applied    sql.NullTime `db:"applied_at"`
	}
	if err = tx.GetContext(ctx, &e, db.Rebind(`SELECT transfer_id,event_kind,applied_at FROM interop_inbox WHERE tenant_id=? AND id=? AND NOT quarantined`), tenant, eventID); err != nil {
		return err
	}
	if e.Applied.Valid {
		return tx.Commit()
	}
	t, q, err := s.lockInterop(ctx, tx, tenant, e.TransferID)
	if err != nil {
		return err
	}
	if err = tx.GetContext(ctx, &e, db.Rebind(`SELECT transfer_id,event_kind,applied_at FROM interop_inbox WHERE tenant_id=? AND id=? AND NOT quarantined FOR UPDATE`), tenant, eventID); err != nil {
		return err
	}
	if e.Applied.Valid {
		return tx.Commit()
	}
	if t.HubState != "UNKNOWN" && t.HubState != e.Kind {
		return ErrInteropConflict
	}
	if t.Status != "SUCCEEDED" && t.Status != "SUSPENSE" && t.Status != "FAILED" {
		switch e.Kind {
		case "COMMITTED":
			if err = s.settleInteropInTx(ctx, tx, t, q); err != nil {
				return err
			}
		case "ABORTED":
			if err = s.failInteropInTx(ctx, tx, t, q, "hub_aborted"); err != nil {
				return err
			}
		default:
			return ErrInteropInvalid
		}
		if _, err = tx.ExecContext(ctx, db.Rebind(`UPDATE interop_transfers SET hub_state=?,updated_at=clock_timestamp(),lease_until=NULL,lease_token=NULL WHERE tenant_id=? AND id=?`), e.Kind, tenant, t.ID); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, db.Rebind(`UPDATE interop_inbox SET applied_at=clock_timestamp() WHERE id=?`), eventID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) settleInteropInTx(ctx context.Context, tx *sqlx.Tx, t *InteropTransfer, q *InteropQuote) error {
	if t.Status == "FAILED" {
		return ErrInteropConflict
	}
	b, err := s.GetInteropBinding(ctx, q.TenantID)
	if err != nil {
		return err
	}
	entry := DoubleEntryParams{TenantID: q.TenantID, IdempotencyKey: "mojaloop:" + t.ID.String(), Currency: q.Currency, ReferenceType: "mojaloop", ReferenceID: t.ID.String(), Amount: q.Amount, Description: "Mojaloop transfer"}
	mode := doubleEntryMode{}
	status := "SUCCEEDED"
	if q.Direction == "OUT" {
		if !t.SubmittedAt.Valid || !t.HoldID.Valid {
			return ErrInteropState
		}
		entry.DebitWalletID = q.WalletID
		entry.CreditWalletID = b.ClearingWalletID
		mode.DebitHoldID = t.HoldID.Int64
		mode.SettleCommittedInteropHold = true
		hold, err := s.lockHold(ctx, tx, q.TenantID, t.HoldID.Int64)
		if err != nil {
			return err
		}
		if hold.Status != HoldStatusCommitted {
			return ErrInteropState
		}
	} else {
		if len(t.OriginalResponse) == 0 {
			return ErrInteropState
		}
		// Lock all potential targets in stable order before deciding where to credit.
		rows, _, err := s.lockSettlementWallets(ctx, tx, MultiLegSettlementParams{TenantID: q.TenantID, Transfers: []SettlementTransfer{{DebitWalletID: b.ClearingWalletID, CreditWalletID: q.WalletID}, {DebitWalletID: b.ClearingWalletID, CreditWalletID: b.SuspenseWalletID}}})
		if err != nil {
			return err
		}
		w := rows[q.WalletID]
		entry.DebitWalletID = b.ClearingWalletID
		entry.CreditWalletID = q.WalletID
		mode.AllowSystemDebitOverdraft = true
		if w.Status != WalletStatusActive {
			entry.CreditWalletID = b.SuspenseWalletID
			status = "SUSPENSE"
		}
	}
	if err = ValidateDoubleEntryParams(entry); err != nil {
		return err
	}
	posting, err := s.postDoubleEntryInTx(ctx, tx, entry, mode)
	if err != nil {
		return err
	}
	if err = s.consumeLimitUsageInTx(ctx, tx, ConsumeLimitUsageParams{Reservation: interopUsage(q), LedgerTransactionID: posting.TransactionID}); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, s.DB.Rebind(`UPDATE interop_transfers SET status=?,ledger_transaction_id=?,hub_state='COMMITTED',error_code=NULL WHERE tenant_id=? AND id=?`), status, posting.TransactionID, q.TenantID, t.ID)
	return err
}

// RejectInteropBeforeSubmit is only for a deterministic local admission failure.
// The submitted_at guard makes network/SDK failures ineligible for release.
func (s *Store) RejectInteropBeforeSubmit(ctx context.Context, tenant string, id, token uuid.UUID, code string) error {
	if err := validateInteropWork(tenant, id, token); err != nil {
		return err
	}
	if code == "" {
		return ErrInteropInvalid
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
	t, q, err := s.lockInterop(ctx, tx, tenant, id)
	if err != nil {
		return err
	}
	if t.SubmittedAt.Valid || len(t.OriginalResponse) != 0 || !t.LeaseToken.Valid || t.LeaseToken.UUID != token || q.Direction != "OUT" {
		return ErrInteropState
	}
	if err = s.failInteropInTx(ctx, tx, t, q, code); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) failInteropInTx(ctx context.Context, tx *sqlx.Tx, t *InteropTransfer, q *InteropQuote, code string) error {
	if t.HubState == "COMMITTED" {
		return ErrInteropConflict
	}
	if t.Status == "SUCCEEDED" || t.Status == "SUSPENSE" {
		return ErrInteropConflict
	}
	if t.Status == "FAILED" {
		return nil
	}
	if t.HoldID.Valid {
		hold, err := s.lockHold(ctx, tx, q.TenantID, t.HoldID.Int64)
		if err != nil {
			return err
		}
		if hold.Status != HoldStatusCommitted {
			return ErrInteropState
		}
		if hold.TenantID != q.TenantID || hold.WalletID != q.WalletID || hold.ReferenceType != "mojaloop" || hold.ReferenceID != t.ID.String() || hold.Amount != q.Amount || hold.AmountRemaining != q.Amount {
			return ErrInteropState
		}
		w, err := s.lockWallet(ctx, tx, q.TenantID, q.WalletID)
		if err != nil {
			return err
		}
		available, err := checkedAddInt64(w.AvailableBalance, hold.AmountRemaining)
		if err != nil {
			return err
		}
		if err = s.updateWalletBalance(ctx, tx, q.TenantID, w.ID, w.Balance, available, time.Now().UTC()); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, s.DB.Rebind(`UPDATE balance_holds SET status='released',amount_remaining=0,released_at=clock_timestamp() WHERE tenant_id=? AND id=?`), q.TenantID, hold.ID); err != nil {
			return err
		}
	}
	if t.HoldID.Valid || len(t.OriginalResponse) != 0 {
		r, err := s.lockLimitReservation(ctx, tx, q.TenantID, interopUsage(q).CommandID)
		if err != nil {
			return err
		}
		if r.Status != LimitReservationStatusReserved {
			return ErrInteropState
		}
		if _, err = s.lockReservationPeriodUsage(ctx, tx, r); err != nil {
			return err
		}
		if err = s.moveReservedLimitUsage(ctx, tx, r, false); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, s.DB.Rebind(`UPDATE transaction_limit_reservations SET status='released',released_at=clock_timestamp() WHERE tenant_id=? AND id=?`), q.TenantID, r.ID); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, s.DB.Rebind(`UPDATE interop_transfers SET status='FAILED',error_code=?,lease_token=NULL,lease_until=NULL,updated_at=clock_timestamp() WHERE tenant_id=? AND id=?`), code, q.TenantID, t.ID)
	return err
}

// Defer a failed posting so one damaged aggregate cannot starve other receipts.
func (s *Store) DeferInteropEvent(ctx context.Context, tenant string, id int64) error {
	if _, err := ValidateTenantID(tenant); err != nil {
		return err
	}
	if id <= 0 {
		return ErrInteropInvalid
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, db.Rebind(`UPDATE interop_inbox SET next_attempt_at=clock_timestamp()+interval '30 seconds' WHERE tenant_id=? AND id=? AND applied_at IS NULL`), tenant, id)
	return err
}
