package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/internal/tenantcatalog"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store provides manual-SQL data access.
type Store struct {
	DB     *DB
	crypto *dataCrypto
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func New(db *DB, opts ...Option) *Store {
	options := StoreOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	crypto, err := newDataCrypto(options.DataKey)
	if err != nil {
		crypto = nil
	}
	return &Store{DB: db, crypto: crypto}
}

func (s *Store) ensureDB() (*sqlx.DB, error) {
	if s == nil || s.DB == nil || s.DB.DB == nil {
		return nil, fmt.Errorf("nil db")
	}
	return s.DB.DB, nil
}

func (s *Store) ProvisionTenantCatalog(ctx context.Context, catalog tenantcatalog.Catalog) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tenants := catalog.All()
	if len(tenants) == 0 {
		return ErrTenantCatalogMismatch
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt := tx.Rebind(`INSERT INTO tenants(id, name, created_at) VALUES(?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = EXCLUDED.name
		WHERE tenants.name IS DISTINCT FROM EXCLUDED.name`)
	now := time.Now().UTC()
	wantedIDs := make([]string, len(tenants))
	for index, tenant := range tenants {
		if _, err := tx.ExecContext(ctx, stmt, tenant.ID, tenant.Name, now); err != nil {
			return err
		}
		wantedIDs[index] = string(tenant.ID)
	}
	var actualIDs []string
	if err := tx.SelectContext(ctx, &actualIDs, "SELECT id FROM tenants ORDER BY id"); err != nil {
		return err
	}
	if !slices.Equal(actualIDs, wantedIDs) {
		return fmt.Errorf("%w: database=%v catalog=%v", ErrTenantCatalogMismatch, actualIDs, wantedIDs)
	}
	return tx.Commit()
}

func (s *Store) ListTenants(ctx context.Context) ([]string, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT id FROM tenants ORDER BY id ASC")
	var tenants []string
	if err := db.SelectContext(ctx, &tenants, stmt); err != nil {
		return nil, err
	}
	return tenants, nil
}

func (s *Store) UpsertCacheBiller(ctx context.Context, tenantID, mobile, billerID string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	billerID = strings.TrimSpace(billerID)
	if billerID == "" {
		return ErrMissingBillerID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO cache_billers(tenant_id, mobile, biller_id, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, mobile) DO UPDATE SET biller_id = excluded.biller_id, updated_at = excluded.updated_at`)
	_, err = db.ExecContext(ctx, stmt, tenantID, mobile, billerID, now, now)
	return err
}

func (s *Store) GetCacheBiller(ctx context.Context, tenantID, mobile string) (*ebs_fields.CacheBillers, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return nil, ErrMissingMobile
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT mobile, biller_id FROM cache_billers WHERE tenant_id = ? AND mobile = ?")
	var c ebs_fields.CacheBillers
	if err := db.GetContext(ctx, &c, stmt, tenantID, mobile); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) CreateTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.UUID) == "" {
		return ErrMissingUUID
	}
	res.MaskPAN()
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, _, err = s.insertTransaction(ctx, db, tenantID, res, now)
	return err
}

type transactionQueryer interface {
	GetContext(ctx context.Context, dest any, query string, args ...any) error
	QueryRowxContext(ctx context.Context, query string, args ...any) *sqlx.Row
}

func (s *Store) insertTransaction(ctx context.Context, q transactionQueryer, tenantID string, res ebs_fields.EBSResponse, now time.Time) (int64, bool, error) {
	payload, err := marshalTransactionPayload(res)
	if err != nil {
		return 0, false, err
	}
	stmt := s.DB.Rebind(`INSERT INTO transactions(
		tenant_id, uuid, response_code, response_message, response_status, tran_date_time, tran_amount, tran_fee,
		pan, sender_pan, receiver_pan, terminal_id, system_trace_audit_number, approval_code, service_id, merchant_id,
		bill_type, bill_to, bill_info2, payload, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tenant_id, uuid) WHERE uuid IS NOT NULL AND btrim(uuid) <> '' DO NOTHING
	RETURNING id`)
	var id int64
	err = q.QueryRowxContext(ctx, stmt,
		tenantID,
		res.UUID,
		res.ResponseCode,
		res.ResponseMessage,
		res.ResponseStatus,
		res.TranDateTime,
		res.TranAmount,
		res.TranFee,
		res.PAN,
		res.SenderPAN,
		res.ReceiverPAN,
		res.TerminalID,
		res.SystemTraceAuditNumber,
		res.ApprovalCode,
		res.ServiceID,
		res.MerchantID,
		res.BillType,
		res.BillTo,
		res.BillInfo2,
		payload,
		now,
		now,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id, replayErr := s.validateExistingTransactionReplay(ctx, q, tenantID, res.UUID, payload)
		return id, true, replayErr
	}
	return id, false, err
}

func (s *Store) validateExistingTransactionReplay(ctx context.Context, q transactionQueryer, tenantID, uuid, payload string) (int64, error) {
	stmt := s.DB.Rebind(`SELECT id, payload FROM transactions
		WHERE tenant_id = ? AND uuid = ?`)
	var existing struct {
		ID      int64           `db:"id"`
		Payload json.RawMessage `db:"payload"`
	}
	if err := q.GetContext(ctx, &existing, stmt, tenantID, strings.TrimSpace(uuid)); err != nil {
		return 0, err
	}
	if !transactionPayloadMatches(existing.Payload, []byte(payload)) {
		return 0, ErrDuplicateTransaction
	}
	return existing.ID, nil
}

func marshalTransactionPayload(res ebs_fields.EBSResponse) (string, error) {
	payload, err := json.Marshal(res)
	if err != nil {
		return "", fmt.Errorf("marshal transaction payload: %w", err)
	}
	return string(payload), nil
}

func transactionPayloadMatches(stored, requested json.RawMessage) bool {
	var storedValue any
	var requestedValue any
	if err := json.Unmarshal(stored, &storedValue); err != nil {
		return string(stored) == string(requested)
	}
	if err := json.Unmarshal(requested, &requestedValue); err != nil {
		return string(stored) == string(requested)
	}
	return reflect.DeepEqual(storedValue, requestedValue)
}

func (s *Store) GetTransactionByUUID(ctx context.Context, tenantID, uuid string) (*ebs_fields.EBSResponse, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if uuid == "" {
		return nil, ErrMissingUUID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT payload FROM transactions WHERE tenant_id = ? AND uuid = ? ORDER BY id DESC LIMIT 1")
	var payload string
	if err := db.GetContext(ctx, &payload, stmt, tenantID, uuid); err != nil {
		return nil, err
	}
	res, err := decodeStoredTransactionPayload(payload, fmt.Sprintf("uuid %q", uuid))
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func decodeStoredTransactionPayload(payload, label string) (ebs_fields.EBSResponse, error) {
	var res ebs_fields.EBSResponse
	if payload == "" {
		return res, nil
	}
	if err := json.Unmarshal([]byte(payload), &res); err != nil {
		return ebs_fields.EBSResponse{}, fmt.Errorf("decode transaction payload %s: %w", label, err)
	}
	return res, nil
}

func (s *Store) CreatePushData(ctx context.Context, tenantID string, data *ebs_fields.PushDataRecord) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if data == nil {
		return ErrMissingPushData
	}
	data.UUID = strings.TrimSpace(data.UUID)
	if data.UUID == "" {
		return ErrMissingUUID
	}
	data.To = strings.TrimSpace(data.To)
	data.Phone = strings.TrimSpace(data.Phone)
	data.DeviceID = strings.TrimSpace(data.DeviceID)
	data.UserMobile = strings.TrimSpace(data.UserMobile)
	rawTransactionUUID := data.TransactionUUID
	data.TransactionUUID = strings.TrimSpace(rawTransactionUUID)
	data.EBSUUID = strings.TrimSpace(data.EBSUUID)
	if data.TransactionUUID != "" {
		if rawTransactionUUID != data.TransactionUUID {
			return ErrInvalidTransactionUUID
		}
		transactionUUID, err := normalizeCanonicalTransactionUUID(data.TransactionUUID)
		if err != nil {
			return err
		}
		if data.EBSUUID != "" && data.EBSUUID != transactionUUID {
			return ErrInvalidTransactionUUID
		}
		data.TransactionUUID = transactionUUID
		data.EBSUUID = transactionUUID
	}
	if data.To == "" && data.Phone == "" && data.DeviceID == "" && data.UserMobile == "" {
		return ErrMissingPushTarget
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	paymentReq, err := json.Marshal(data.PaymentRequest)
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`INSERT INTO push_data(
		uuid, tenant_id, type, date, to_device, title, body, call_to_action, phone, is_read, device_id, user_mobile, ebs_uuid, payment_request, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = db.ExecContext(ctx, stmt,
		data.UUID,
		tenantID,
		data.Type,
		data.Date,
		data.To,
		data.Title,
		data.Body,
		data.CallToAction,
		data.Phone,
		data.IsRead,
		data.DeviceID,
		data.UserMobile,
		data.EBSUUID,
		string(paymentReq),
		now,
		now,
	)
	return err
}

func normalizeCanonicalTransactionUUID(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return "", ErrInvalidTransactionUUID
	}
	return value, nil
}

func (s *Store) GetMeterName(ctx context.Context, tenantID, nec string) (string, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return "", err
	}
	db, err := s.ensureDB()
	if err != nil {
		return "", err
	}
	stmt := s.DB.Rebind("SELECT name FROM meter_names WHERE tenant_id = ? AND nec = ?")
	var name string
	if err := db.GetContext(ctx, &name, stmt, tenantID, nec); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) SaveEBSUUID(ctx context.Context, tenantID string, originalUUID string, res ebs_fields.EBSResponse) error {
	return s.CreateTransaction(ctx, tenantID, res)
}

func (s *Store) UpdatePaymentRequest(ctx context.Context, tenantID string, uuid string, data ebs_fields.QrData) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if uuid == "" {
		return ErrMissingUUID
	}
	payload, _ := json.Marshal(data)
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE push_data SET payment_request = ?, updated_at = ? WHERE tenant_id = ? AND uuid = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, string(payload), time.Now().UTC(), tenantID, uuid)
}

// ErrNotFound returns true if the provided error is a not found error.
func ErrNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sql.ErrNoRows)
}

func execContextRequireRowsAffected(ctx context.Context, db sqlExecer, stmt string, args ...any) error {
	result, err := db.ExecContext(ctx, stmt, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
