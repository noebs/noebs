package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
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

func (s *Store) EnsureTenant(ctx context.Context, tenantID string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tenantID, err = ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("INSERT INTO tenants(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(id) DO NOTHING")
	_, err = db.ExecContext(ctx, stmt, tenantID, tenantID, time.Now().UTC())
	return err
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

func (s *Store) ListCardsByUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Card, error) {
	return nil, ErrLegacyCardOperation
}

func (s *Store) ListCardsByMobile(ctx context.Context, tenantID, mobile string) ([]ebs_fields.Card, error) {
	return nil, ErrLegacyCardOperation
}

func (s *Store) AddCards(ctx context.Context, tenantID string, userID int64, cards []ebs_fields.Card) error {
	return ErrLegacyCardOperation
}

func (s *Store) UpdateCard(ctx context.Context, tenantID string, userID int64, card ebs_fields.Card) error {
	return ErrLegacyCardOperation
}

func (s *Store) DeleteCard(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	return ErrLegacyCardOperation
}

func (s *Store) SetMainCard(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	return ErrLegacyCardOperation
}

func (s *Store) GetPanByMobile(ctx context.Context, tenantID, mobile string) (string, error) {
	return "", ErrLegacyCardOperation
}

func (s *Store) ListBeneficiaries(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Beneficiary, error) {
	return nil, ErrBeneficiaryRetired
}

func (s *Store) UpsertBeneficiary(ctx context.Context, tenantID string, userID int64, b ebs_fields.Beneficiary) error {
	return ErrBeneficiaryRetired
}

func (s *Store) DeleteBeneficiary(ctx context.Context, tenantID string, userID int64, data string) error {
	return ErrBeneficiaryRetired
}

func (s *Store) CardExists(ctx context.Context, tenantID, pan string) (bool, error) {
	return false, ErrLegacyCardOperation
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

func (s *Store) CreateToken(ctx context.Context, tenantID string, token *ebs_fields.Token) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if token == nil {
		return ErrMissingToken
	}
	if token.UUID == "" {
		return ErrMissingUUID
	}
	if token.UserID <= 0 {
		return ErrInvalidUserID
	}
	if token.Amount < 0 {
		return ErrInvalidAmount
	}
	token.IsPaid = false
	token.PaymentStatus = ebs_fields.PaymentTokenStatusAvailable
	token.RailUUID = uuid.NewString()
	if s == nil {
		return fmt.Errorf("nil db")
	}
	if err := s.requireDataKeyForSensitiveValue(token.ToCard); err != nil {
		return err
	}
	toCardValue := token.ToCard
	toCardEnc := ""
	if s.crypto != nil && token.ToCard != "" {
		var encErr error
		toCardValue, toCardEnc, encErr = s.encryptTokenFields(token)
		if encErr != nil {
			return encErr
		}
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := `INSERT INTO tokens(tenant_id, user_id, amount, cart_id, uuid, note, to_card, to_card_enc, is_paid, payment_status, rail_uuid, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []any{tenantID, token.UserID, token.Amount, token.CartID, token.UUID, token.Note, toCardValue, toCardEnc, token.IsPaid, token.PaymentStatus, token.RailUUID, now, now}
	if s.DB.Driver == DriverPostgres {
		stmt = stmt + " RETURNING id"
		stmt = s.DB.Rebind(stmt)
		if err := db.QueryRowContext(ctx, stmt, args...).Scan(&token.ID); err != nil {
			return err
		}
	} else {
		stmt = s.DB.Rebind(stmt)
		res, err := db.ExecContext(ctx, stmt, args...)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		token.ID = id
	}
	return nil
}

func (s *Store) GetTokenByUUID(ctx context.Context, tenantID, uuid string) (*ebs_fields.Token, error) {
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
	stmt := s.DB.Rebind("SELECT * FROM tokens WHERE tenant_id = ? AND uuid = ?")
	var token ebs_fields.Token
	if err := db.GetContext(ctx, &token, stmt, tenantID, uuid); err != nil {
		return nil, err
	}
	if err := s.hydrateTokenFields(ctx, tenantID, &token); err != nil {
		return nil, err
	}
	return &token, nil
}

func (s *Store) ClaimTokenForPayment(ctx context.Context, tenantID, uuid string, payerUserID int64, amount int) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if uuid == "" {
		return ErrMissingUUID
	}
	if payerUserID <= 0 {
		return ErrInvalidUserID
	}
	if amount <= 0 {
		return ErrInvalidAmount
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE tokens
		SET payment_status = ?, payer_user_id = ?, claimed_amount = ?, processing_at = ?, finalized_at = NULL, updated_at = ?
		WHERE tenant_id = ? AND uuid = ? AND is_paid = FALSE AND payment_status = ?`)
	now := time.Now().UTC()
	err = execContextRequireRowsAffected(ctx, db, stmt,
		ebs_fields.PaymentTokenStatusProcessing,
		payerUserID,
		amount,
		now,
		now,
		tenantID,
		uuid,
		ebs_fields.PaymentTokenStatusAvailable,
	)
	if !ErrNotFound(err) {
		return err
	}
	return s.paymentTokenStateError(ctx, db, tenantID, uuid)
}

func (s *Store) FinalizeTokenPayment(ctx context.Context, tenantID, uuid, railUUID string, payerUserID int64, status string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if uuid == "" {
		return ErrMissingUUID
	}
	if railUUID == "" {
		return ErrMissingUUID
	}
	if payerUserID <= 0 {
		return ErrInvalidUserID
	}
	if status != ebs_fields.PaymentTokenStatusPaid && status != ebs_fields.PaymentTokenStatusFailed {
		return ErrInvalidPaymentTokenStatus
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE tokens
		SET payment_status = ?, is_paid = ?, finalized_at = COALESCE(finalized_at, ?), updated_at = ?
		WHERE tenant_id = ? AND uuid = ? AND rail_uuid = ? AND payer_user_id = ? AND payment_status IN (?, ?)`)
	now := time.Now().UTC()
	err = execContextRequireRowsAffected(ctx, db, stmt,
		status,
		status == ebs_fields.PaymentTokenStatusPaid,
		now,
		now,
		tenantID,
		uuid,
		railUUID,
		payerUserID,
		ebs_fields.PaymentTokenStatusProcessing,
		status,
	)
	if !ErrNotFound(err) {
		return err
	}
	return s.paymentTokenStateError(ctx, db, tenantID, uuid)
}

func (s *Store) MarkTokenPaid(ctx context.Context, tenantID, uuid, railUUID string, payerUserID int64) error {
	return s.FinalizeTokenPayment(ctx, tenantID, uuid, railUUID, payerUserID, ebs_fields.PaymentTokenStatusPaid)
}

func (s *Store) MarkTokenFailed(ctx context.Context, tenantID, uuid, railUUID string, payerUserID int64) error {
	return s.FinalizeTokenPayment(ctx, tenantID, uuid, railUUID, payerUserID, ebs_fields.PaymentTokenStatusFailed)
}

func (s *Store) paymentTokenStateError(ctx context.Context, db *sqlx.DB, tenantID, uuid string) error {
	stmt := s.DB.Rebind("SELECT EXISTS(SELECT 1 FROM tokens WHERE tenant_id = ? AND uuid = ?)")
	var exists bool
	if err := db.GetContext(ctx, &exists, stmt, tenantID, uuid); err != nil {
		return err
	}
	if !exists {
		return sql.ErrNoRows
	}
	return ErrPaymentTokenUnavailable
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
		tenant_id, token_id, uuid, response_code, response_message, response_status, tran_date_time, tran_amount, tran_fee,
		pan, sender_pan, receiver_pan, terminal_id, system_trace_audit_number, approval_code, service_id, merchant_id,
		bill_type, bill_to, bill_info2, payload, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tenant_id, uuid) WHERE uuid IS NOT NULL AND btrim(uuid) <> '' DO NOTHING
	RETURNING id`)
	var id int64
	err = q.QueryRowxContext(ctx, stmt,
		tenantID,
		res.TokenID,
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

func (s *Store) GetNotifications(ctx context.Context, tenantID, userMobile string) ([]ebs_fields.PushDataRecord, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	userMobile = strings.TrimSpace(userMobile)
	if userMobile == "" {
		return nil, ErrMissingMobile
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM push_data WHERE tenant_id = ? AND (user_mobile = ? OR phone = ?) AND deleted_at IS NULL ORDER BY date DESC")
	rows, err := db.QueryxContext(ctx, stmt, tenantID, userMobile, userMobile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []ebs_fields.PushDataRecord
	for rows.Next() {
		var record ebs_fields.PushDataRecord
		var paymentReq string
		if err := rows.Scan(
			&record.UUID,
			&record.TenantID,
			&record.Type,
			&record.Date,
			&record.To,
			&record.Title,
			&record.Body,
			&record.CallToAction,
			&record.Phone,
			&record.IsRead,
			&record.DeviceID,
			&record.UserMobile,
			&record.EBSUUID,
			&paymentReq,
			&record.CreatedAt,
			&record.UpdatedAt,
			&record.DeletedAt,
		); err != nil {
			return nil, err
		}
		if paymentReq != "" {
			paymentRequest, err := decodePaymentRequestPayload(paymentReq, record.UUID)
			if err != nil {
				return nil, err
			}
			record.PaymentRequest = paymentRequest
		}
		if transactionUUID, err := normalizeCanonicalTransactionUUID(record.EBSUUID); err == nil {
			record.TransactionUUID = transactionUUID
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func normalizeCanonicalTransactionUUID(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return "", ErrInvalidTransactionUUID
	}
	return value, nil
}

func decodePaymentRequestPayload(payload, uuid string) (ebs_fields.QrData, error) {
	var paymentRequest ebs_fields.QrData
	if payload == "" {
		return paymentRequest, nil
	}
	if err := json.Unmarshal([]byte(payload), &paymentRequest); err != nil {
		return ebs_fields.QrData{}, fmt.Errorf("decode payment request payload %q: %w", uuid, err)
	}
	return paymentRequest, nil
}

func (s *Store) MarkNotificationsRead(ctx context.Context, tenantID, phone string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ErrMissingMobile
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE push_data SET is_read = TRUE, updated_at = ? WHERE tenant_id = ? AND (phone = ? OR user_mobile = ?)")
	_, err = db.ExecContext(ctx, stmt, time.Now().UTC(), tenantID, phone, phone)
	return err
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

func (s *Store) GetDeviceIDsByPan(ctx context.Context, tenantID, pan string) ([]string, error) {
	return nil, ErrLegacyCardOperation
}

func (s *Store) GetTokenWithTransaction(ctx context.Context, tenantID, uuid string) (*ebs_fields.Token, error) {
	token, err := s.GetTokenByUUID(ctx, tenantID, uuid)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (s *Store) GetAllTokensByUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Token, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM tokens WHERE tenant_id = ? AND user_id = ?")
	var tokens []ebs_fields.Token
	if err := db.SelectContext(ctx, &tokens, stmt, tenantID, userID); err != nil {
		return nil, err
	}
	for i := range tokens {
		if err := s.hydrateTokenFields(ctx, tenantID, &tokens[i]); err != nil {
			return nil, err
		}
	}
	return tokens, nil
}

func (s *Store) GetAllTokensByUserIDAndCartID(ctx context.Context, tenantID string, userID int64, cartID string) ([]ebs_fields.Token, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM tokens WHERE tenant_id = ? AND user_id = ? AND cart_id = ?")
	var tokens []ebs_fields.Token
	if err := db.SelectContext(ctx, &tokens, stmt, tenantID, userID, cartID); err != nil {
		return nil, err
	}
	for i := range tokens {
		if err := s.hydrateTokenFields(ctx, tenantID, &tokens[i]); err != nil {
			return nil, err
		}
	}
	return tokens, nil
}

func (s *Store) GetTokenByUUIDWithResult(ctx context.Context, tenantID, uuid string) (*ebs_fields.Token, error) {
	return s.GetTokenByUUID(ctx, tenantID, uuid)
}

func (s *Store) UpdateTokenCard(ctx context.Context, tenantID string, uuid, toCard string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if uuid == "" {
		return ErrMissingUUID
	}
	if err := s.requireDataKeyForSensitiveValue(toCard); err != nil {
		return err
	}
	toCardValue := toCard
	toCardEnc := ""
	if s.crypto != nil && toCard != "" {
		toCardValue = s.crypto.Hash(toCard)
		enc, encErr := s.crypto.Encrypt(toCard)
		if encErr != nil {
			return encErr
		}
		toCardEnc = enc
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE tokens SET to_card = ?, to_card_enc = ?, updated_at = ? WHERE tenant_id = ? AND uuid = ? AND payment_status = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, toCardValue, toCardEnc, time.Now().UTC(), tenantID, uuid, ebs_fields.PaymentTokenStatusAvailable)
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
