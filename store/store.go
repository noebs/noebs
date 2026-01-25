package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/jmoiron/sqlx"
)

const DefaultTenantID = "default"

// Store provides manual-SQL data access.
type Store struct {
	DB     *DB
	crypto *dataCrypto
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
	if tenantID == "" {
		tenantID = DefaultTenantID
	}
	stmt := s.DB.Rebind("INSERT INTO tenants(id, name, created_at) VALUES(?, ?, ?) ON CONFLICT(id) DO NOTHING")
	_, err = db.ExecContext(ctx, stmt, tenantID, tenantID, time.Now().UTC())
	return err
}

func (s *Store) CreateAPIKey(ctx context.Context, tenantID, email, apiKey string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("INSERT INTO api_keys(tenant_id, email, api_key, created_at) VALUES(?, ?, ?, ?) ON CONFLICT(tenant_id, email) DO UPDATE SET api_key = excluded.api_key")
	_, err = db.ExecContext(ctx, stmt, tenantID, strings.ToLower(email), apiKey, time.Now().UTC())
	return err
}

func (s *Store) ValidateAPIKey(ctx context.Context, tenantID, email, apiKey string) (bool, error) {
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	stmt := s.DB.Rebind("SELECT api_key FROM api_keys WHERE tenant_id = ? AND email = ?")
	var stored string
	if err := db.GetContext(ctx, &stored, stmt, tenantID, strings.ToLower(email)); err != nil {
		return false, err
	}
	return stored == apiKey, nil
}

func (s *Store) ValidateAPIKeyValue(ctx context.Context, tenantID, apiKey string) (bool, error) {
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	stmt := s.DB.Rebind("SELECT api_key FROM api_keys WHERE tenant_id = ? AND api_key = ? LIMIT 1")
	var stored string
	if err := db.GetContext(ctx, &stored, stmt, tenantID, apiKey); err != nil {
		return false, err
	}
	return stored == apiKey, nil
}

func (s *Store) CreateUser(ctx context.Context, tenantID string, user *ebs_fields.User) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	if tenantID == "" {
		tenantID = DefaultTenantID
	}
	s.encryptUserFields(user)
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO users(
		tenant_id, password, fullname, username, gender, birthday, email, is_merchant, public_key, device_id, otp, signed_otp,
		firebase_token, is_password_otp, main_card, main_card_enc, main_expdate, language, is_verified, mobile, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	res, err := db.ExecContext(ctx, stmt,
		tenantID,
		user.Password,
		user.Fullname,
		user.Username,
		user.Gender,
		user.Birthday,
		strings.ToLower(user.Email),
		user.IsMerchant,
		user.PublicKey,
		user.DeviceID,
		user.OTP,
		user.SignedOTP,
		user.DeviceToken,
		user.IsPasswordOTP,
		user.MainCard,
		user.MainCardEnc,
		user.ExpDate,
		user.Language,
		user.IsVerified,
		user.Mobile,
		now,
		now,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	user.ID = id
	user.CreatedAt = now
	user.UpdatedAt = now
	return nil
}

func (s *Store) GetUserByMobile(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND mobile = ? AND deleted_at IS NULL")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, mobile); err != nil {
		return nil, err
	}
	s.hydrateUserFields(ctx, tenantID, &user)
	return &user, nil
}

func (s *Store) GetUserByEmailOrMobile(ctx context.Context, tenantID, query string) (*ebs_fields.User, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND deleted_at IS NULL AND (email = ? OR mobile = ?)")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, q, q); err != nil {
		return nil, err
	}
	s.hydrateUserFields(ctx, tenantID, &user)
	return &user, nil
}

func (s *Store) GetUserByCard(ctx context.Context, tenantID, pan string) (*ebs_fields.User, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	args := []any{tenantID, tenantID}
	args = append(args, s.panLookupArgs(pan)...)
	stmt := s.DB.Rebind(`SELECT users.* FROM users
		LEFT JOIN cards ON cards.user_id = users.id
		WHERE users.tenant_id = ? AND cards.tenant_id = ? AND ` + s.panLookupClause("cards.pan") + ` AND cards.deleted_at IS NULL
		LIMIT 1`)
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, args...); err != nil {
		return nil, err
	}
	s.hydrateUserFields(ctx, tenantID, &user)
	return &user, nil
}

func (s *Store) FindUserByUsername(ctx context.Context, tenantID, username string) (*ebs_fields.User, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND username = ? AND deleted_at IS NULL")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, strings.ToLower(username)); err != nil {
		return nil, err
	}
	s.hydrateUserFields(ctx, tenantID, &user)
	return &user, nil
}

func (s *Store) GetUserByUsernameEmailOrMobile(ctx context.Context, tenantID, query string) (*ebs_fields.User, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND deleted_at IS NULL AND (username = ? OR email = ? OR mobile = ?)")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, q, q, q); err != nil {
		return nil, err
	}
	s.hydrateUserFields(ctx, tenantID, &user)
	return &user, nil
}

func (s *Store) UpdateUser(ctx context.Context, tenantID string, user *ebs_fields.User) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	s.encryptUserFields(user)
	user.UpdatedAt = time.Now().UTC()
	stmt := s.DB.Rebind(`UPDATE users SET
		password = ?, fullname = ?, username = ?, gender = ?, birthday = ?, email = ?, is_merchant = ?, public_key = ?, device_id = ?,
		otp = ?, signed_otp = ?, firebase_token = ?, is_password_otp = ?, main_card = ?, main_card_enc = ?, main_expdate = ?, language = ?, is_verified = ?, mobile = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	_, err = db.ExecContext(ctx, stmt,
		user.Password,
		user.Fullname,
		user.Username,
		user.Gender,
		user.Birthday,
		strings.ToLower(user.Email),
		user.IsMerchant,
		user.PublicKey,
		user.DeviceID,
		user.OTP,
		user.SignedOTP,
		user.DeviceToken,
		user.IsPasswordOTP,
		user.MainCard,
		user.MainCardEnc,
		user.ExpDate,
		user.Language,
		user.IsVerified,
		user.Mobile,
		user.UpdatedAt,
		tenantID,
		user.ID,
	)
	return err
}

func (s *Store) UpdateUserColumns(ctx context.Context, tenantID string, userID int64, updates map[string]any) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	if s.crypto != nil {
		if value, ok := updates["main_card"].(string); ok {
			if value == "" {
				updates["main_card_enc"] = ""
			} else if !s.crypto.IsHash(value) {
				enc, err := s.crypto.Encrypt(value)
				if err == nil {
					updates["main_card"] = s.crypto.Hash(value)
					updates["main_card_enc"] = enc
				}
			}
		}
	}
	setParts := []string{}
	args := []any{}
	for key, value := range updates {
		setParts = append(setParts, fmt.Sprintf("%s = ?", key))
		args = append(args, value)
	}
	setParts = append(setParts, "updated_at = ?")
	args = append(args, time.Now().UTC())
	args = append(args, tenantID, userID)
	stmt := s.DB.Rebind(fmt.Sprintf("UPDATE users SET %s WHERE tenant_id = ? AND id = ?", strings.Join(setParts, ", ")))
	_, err = db.ExecContext(ctx, stmt, args...)
	return err
}

func (s *Store) UpsertDeviceToken(ctx context.Context, tenantID string, mobile, deviceID string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE users SET device_id = ?, updated_at = ? WHERE tenant_id = ? AND mobile = ?")
	res, err := db.ExecContext(ctx, stmt, deviceID, time.Now().UTC(), tenantID, mobile)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		user := &ebs_fields.User{Mobile: mobile, Username: mobile, DeviceID: deviceID}
		return s.CreateUser(ctx, tenantID, user)
	}
	return nil
}

func (s *Store) GetUserWithCards(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, error) {
	user, err := s.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return nil, err
	}
	cards, err := s.ListCardsByUserID(ctx, tenantID, user.ID)
	if err != nil {
		return nil, err
	}
	user.Cards = cards
	return user, nil
}

func (s *Store) ListCardsByUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Card, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM cards WHERE tenant_id = ? AND user_id = ? AND deleted_at IS NULL ORDER BY is_main DESC")
	cards := []ebs_fields.Card{}
	if err := db.SelectContext(ctx, &cards, stmt, tenantID, userID); err != nil {
		return nil, err
	}
	for i := range cards {
		s.hydrateCardFields(ctx, tenantID, &cards[i])
	}
	return cards, nil
}

func (s *Store) AddCards(ctx context.Context, tenantID string, userID int64, cards []ebs_fields.Card) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, card := range cards {
		s.encryptCardFields(&card)
		stmt := s.DB.Rebind(`INSERT INTO cards(
			tenant_id, user_id, pan, pan_enc, expiry, name, ipin, ipin_enc, is_main, is_valid, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if _, err := db.ExecContext(ctx, stmt,
			tenantID,
			userID,
			card.Pan,
			card.PanEnc,
			card.Expiry,
			card.Name,
			card.IPIN,
			card.IPINEnc,
			card.IsMain,
			card.IsValid,
			now,
			now,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) UpdateCard(ctx context.Context, tenantID string, userID int64, card ebs_fields.Card) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	s.encryptCardFields(&card)
	panClause := s.panLookupClause("pan")
	panArgs := s.panLookupArgs(card.CardIdx)
	stmt := s.DB.Rebind(`UPDATE cards SET
		pan = ?, pan_enc = ?, expiry = ?, name = ?, ipin = ?, ipin_enc = ?, is_main = ?, is_valid = ?, updated_at = ?
		WHERE tenant_id = ? AND user_id = ? AND ` + panClause + ` AND deleted_at IS NULL`)
	args := []any{
		card.Pan,
		card.PanEnc,
		card.Expiry,
		card.Name,
		card.IPIN,
		card.IPINEnc,
		card.IsMain,
		card.IsValid,
		time.Now().UTC(),
		tenantID,
		userID,
	}
	args = append(args, panArgs...)
	_, err = db.ExecContext(ctx, stmt, args...)
	return err
}

func (s *Store) DeleteCard(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	panClause := s.panLookupClause("pan")
	stmt := s.DB.Rebind("UPDATE cards SET deleted_at = ? WHERE tenant_id = ? AND user_id = ? AND " + panClause + " AND deleted_at IS NULL")
	args := []any{time.Now().UTC(), tenantID, userID}
	args = append(args, s.panLookupArgs(cardIdx)...)
	_, err = db.ExecContext(ctx, stmt, args...)
	return err
}

func (s *Store) SetMainCard(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	if _, err := s.ensureDB(); err != nil {
		return err
	}
	tx, err := s.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	resetStmt := s.DB.Rebind("UPDATE cards SET is_main = FALSE, updated_at = ? WHERE tenant_id = ? AND user_id = ? AND deleted_at IS NULL")
	if _, err := tx.ExecContext(ctx, resetStmt, time.Now().UTC(), tenantID, userID); err != nil {
		_ = tx.Rollback()
		return err
	}
	panClause := s.panLookupClause("pan")
	setStmt := s.DB.Rebind("UPDATE cards SET is_main = TRUE, updated_at = ? WHERE tenant_id = ? AND user_id = ? AND " + panClause + " AND deleted_at IS NULL")
	args := []any{time.Now().UTC(), tenantID, userID}
	args = append(args, s.panLookupArgs(cardIdx)...)
	if _, err := tx.ExecContext(ctx, setStmt, args...); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) GetPanByMobile(ctx context.Context, tenantID, mobile string) (string, error) {
	user, err := s.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return "", err
	}
	cards, err := s.ListCardsByUserID(ctx, tenantID, user.ID)
	if err != nil {
		return "", err
	}
	if len(cards) == 0 {
		return "", errors.New("no cards")
	}
	return cards[0].Pan, nil
}

func (s *Store) ListBeneficiaries(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Beneficiary, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM beneficiaries WHERE tenant_id = ? AND user_id = ?")
	var list []ebs_fields.Beneficiary
	if err := db.SelectContext(ctx, &list, stmt, tenantID, userID); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) UpsertBeneficiary(ctx context.Context, tenantID string, userID int64, b ebs_fields.Beneficiary) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO beneficiaries(tenant_id, user_id, data, bill_type, name, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`)
	_, err = db.ExecContext(ctx, stmt, tenantID, userID, b.Data, b.BillType, b.Name, now, now)
	return err
}

func (s *Store) DeleteBeneficiary(ctx context.Context, tenantID string, userID int64, data string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("DELETE FROM beneficiaries WHERE tenant_id = ? AND user_id = ? AND data = ?")
	_, err = db.ExecContext(ctx, stmt, tenantID, userID, data)
	return err
}

func (s *Store) UpsertCacheCard(ctx context.Context, tenantID string, card ebs_fields.CacheCards) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	s.encryptCacheCardFields(&card)
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO cache_cards(tenant_id, pan, pan_enc, expiry, name, is_valid, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, pan) DO UPDATE SET is_valid = excluded.is_valid, pan_enc = excluded.pan_enc, expiry = excluded.expiry, name = excluded.name, updated_at = excluded.updated_at`)
	_, err = db.ExecContext(ctx, stmt, tenantID, card.Pan, card.PanEnc, card.Expiry, card.Name, card.IsValid, now, now)
	return err
}

func (s *Store) GetCacheCard(ctx context.Context, tenantID, pan string) (*ebs_fields.CacheCards, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM cache_cards WHERE tenant_id = ? AND " + s.panLookupClause("pan"))
	var card ebs_fields.CacheCards
	args := []any{tenantID}
	args = append(args, s.panLookupArgs(pan)...)
	if err := db.GetContext(ctx, &card, stmt, args...); err != nil {
		return nil, err
	}
	s.hydrateCacheCardFields(ctx, tenantID, &card)
	return &card, nil
}

func (s *Store) CardExists(ctx context.Context, tenantID, pan string) (bool, error) {
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	stmt := s.DB.Rebind("SELECT 1 FROM cards WHERE tenant_id = ? AND " + s.panLookupClause("pan") + " AND deleted_at IS NULL LIMIT 1")
	var one int
	args := []any{tenantID}
	args = append(args, s.panLookupArgs(pan)...)
	if err := db.GetContext(ctx, &one, stmt, args...); err != nil {
		if ErrNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) UpsertCacheBiller(ctx context.Context, tenantID, mobile, billerID string) error {
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

func (s *Store) RecordLoginAttempt(ctx context.Context, tenantID, mobile string, increment bool) (int, error) {
	db, err := s.ensureDB()
	if err != nil {
		return 0, err
	}
	stmt := s.DB.Rebind("SELECT login_count, window_started_at FROM login_metrics WHERE tenant_id = ? AND mobile = ?")
	var count int
	var window time.Time
	if err := db.GetContext(ctx, &count, stmt, tenantID, mobile); err != nil {
		if strings.Contains(err.Error(), "no rows") {
			now := time.Now().UTC()
			insertStmt := s.DB.Rebind("INSERT INTO login_metrics(tenant_id, mobile, login_count, window_started_at, suspicious_count) VALUES(?, ?, ?, ?, 0)")
			_, err = db.ExecContext(ctx, insertStmt, tenantID, mobile, 0, now)
			if err != nil {
				return 0, err
			}
			return 0, nil
		}
		return 0, err
	}
	_ = window
	if increment {
		count++
		updateStmt := s.DB.Rebind("UPDATE login_metrics SET login_count = ?, window_started_at = ?, updated_at = ? WHERE tenant_id = ? AND mobile = ?")
		_, _ = db.ExecContext(ctx, updateStmt, count, time.Now().UTC(), time.Now().UTC(), tenantID, mobile)
	}
	return count, nil
}

func (s *Store) IncrementSuspicious(ctx context.Context, tenantID, mobile string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE login_metrics SET suspicious_count = suspicious_count + 1 WHERE tenant_id = ? AND mobile = ?")
	_, err = db.ExecContext(ctx, stmt, tenantID, mobile)
	return err
}

func (s *Store) CreateToken(ctx context.Context, tenantID string, token *ebs_fields.Token) error {
	db, err := s.ensureDB()
	if err != nil {
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
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO tokens(tenant_id, user_id, amount, cart_id, uuid, note, to_card, to_card_enc, is_paid, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	res, err := db.ExecContext(ctx, stmt, tenantID, token.UserID, token.Amount, token.CartID, token.UUID, token.Note, toCardValue, toCardEnc, token.IsPaid, now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	token.ID = id
	return nil
}

func (s *Store) GetTokenByUUID(ctx context.Context, tenantID, uuid string) (*ebs_fields.Token, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM tokens WHERE tenant_id = ? AND uuid = ?")
	var token ebs_fields.Token
	if err := db.GetContext(ctx, &token, stmt, tenantID, uuid); err != nil {
		return nil, err
	}
	s.hydrateTokenFields(ctx, tenantID, &token)
	return &token, nil
}

func (s *Store) MarkTokenPaid(ctx context.Context, tenantID, uuid string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE tokens SET is_paid = TRUE, updated_at = ? WHERE tenant_id = ? AND uuid = ?")
	_, err = db.ExecContext(ctx, stmt, time.Now().UTC(), tenantID, uuid)
	return err
}

func (s *Store) CreateTransaction(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	res.MaskPAN()
	payload, _ := json.Marshal(res)
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO transactions(
		tenant_id, token_id, uuid, response_code, response_message, response_status, tran_date_time, tran_amount, tran_fee,
		pan, sender_pan, receiver_pan, terminal_id, system_trace_audit_number, approval_code, service_id, merchant_id,
		bill_type, bill_to, bill_info2, payload, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = db.ExecContext(ctx, stmt,
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
		string(payload),
		now,
		now,
	)
	return err
}

func (s *Store) GetTransactionsByMaskedPan(ctx context.Context, tenantID string, maskedPan string) ([]ebs_fields.EBSResponse, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT payload FROM transactions WHERE tenant_id = ? AND (pan = ? OR sender_pan = ? OR receiver_pan = ?)")
	rows, err := db.QueryxContext(ctx, stmt, tenantID, maskedPan, maskedPan, maskedPan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res []ebs_fields.EBSResponse
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var item ebs_fields.EBSResponse
		if payload != "" {
			_ = json.Unmarshal([]byte(payload), &item)
		}
		res = append(res, item)
	}
	return res, rows.Err()
}

func (s *Store) GetTransactionByUUID(ctx context.Context, tenantID, uuid string) (*ebs_fields.EBSResponse, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT payload FROM transactions WHERE tenant_id = ? AND uuid = ? ORDER BY id DESC LIMIT 1")
	var payload string
	if err := db.GetContext(ctx, &payload, stmt, tenantID, uuid); err != nil {
		return nil, err
	}
	var res ebs_fields.EBSResponse
	if payload != "" {
		_ = json.Unmarshal([]byte(payload), &res)
	}
	return &res, nil
}

func (s *Store) CreatePushData(ctx context.Context, tenantID string, data *ebs_fields.PushDataRecord) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	paymentReq, _ := json.Marshal(data.PaymentRequest)
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
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM push_data WHERE tenant_id = ? AND user_mobile = ? AND deleted_at IS NULL ORDER BY date DESC")
	rows, err := db.QueryxContext(ctx, stmt, tenantID, userMobile)
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
			_ = json.Unmarshal([]byte(paymentReq), &record.PaymentRequest)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) MarkNotificationsRead(ctx context.Context, tenantID, phone string) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE push_data SET is_read = TRUE, updated_at = ? WHERE tenant_id = ? AND phone = ?")
	_, err = db.ExecContext(ctx, stmt, time.Now().UTC(), tenantID, phone)
	return err
}

func (s *Store) GetMeterName(ctx context.Context, tenantID, nec string) (string, error) {
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

func (s *Store) UpdateKYC(ctx context.Context, tenantID string, kyc *ebs_fields.KYC, passport *ebs_fields.Passport) error {
	if _, err := s.ensureDB(); err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := s.DB.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	kycStmt := s.DB.Rebind(`INSERT INTO kyc(tenant_id, user_mobile, mobile, selfie, passport_img, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, mobile) DO UPDATE SET user_mobile = excluded.user_mobile, selfie = excluded.selfie, passport_img = excluded.passport_img, updated_at = excluded.updated_at`)
	if _, err := tx.ExecContext(ctx, kycStmt, tenantID, kyc.UserMobile, kyc.Mobile, kyc.Selfie, kyc.PassportImg, now, now); err != nil {
		_ = tx.Rollback()
		return err
	}
	if passport != nil {
		passStmt := s.DB.Rebind(`INSERT INTO passports(tenant_id, mobile, birth_date, issue_date, expiration_date, national_number, passport_number, gender, nationality, holder_name, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(tenant_id, mobile) DO UPDATE SET birth_date = excluded.birth_date, issue_date = excluded.issue_date, expiration_date = excluded.expiration_date,
			national_number = excluded.national_number, passport_number = excluded.passport_number, gender = excluded.gender, nationality = excluded.nationality, holder_name = excluded.holder_name, updated_at = excluded.updated_at`)
		if _, err := tx.ExecContext(ctx, passStmt, tenantID, passport.Mobile, passport.BirthDate, passport.IssueDate, passport.ExpirationDate, passport.NationalNumber, passport.PassportNumber, passport.Gender, passport.Nationality, passport.HolderName, now, now); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetUserWithKYC(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, *ebs_fields.KYC, *ebs_fields.Passport, error) {
	user, err := s.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return nil, nil, nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, nil, nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM kyc WHERE tenant_id = ? AND mobile = ?")
	var kyc ebs_fields.KYC
	if err := db.GetContext(ctx, &kyc, stmt, tenantID, mobile); err != nil {
		return user, nil, nil, nil
	}
	passStmt := s.DB.Rebind("SELECT * FROM passports WHERE tenant_id = ? AND mobile = ?")
	var passport ebs_fields.Passport
	if err := db.GetContext(ctx, &passport, passStmt, tenantID, mobile); err != nil {
		return user, &kyc, nil, nil
	}
	return user, &kyc, &passport, nil
}

func (s *Store) LinkAuthAccount(ctx context.Context, tenantID string, account *ebs_fields.AuthAccount) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO auth_accounts(tenant_id, user_id, provider, provider_user_id, email, email_verified, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, provider, provider_user_id) DO UPDATE SET email = excluded.email, email_verified = excluded.email_verified, updated_at = excluded.updated_at`)
	_, err = db.ExecContext(ctx, stmt, tenantID, account.UserID, account.Provider, account.ProviderUserID, strings.ToLower(account.Email), account.EmailVerified, now, now)
	return err
}

func (s *Store) FindAuthAccount(ctx context.Context, tenantID, provider, providerUserID string) (*ebs_fields.AuthAccount, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM auth_accounts WHERE tenant_id = ? AND provider = ? AND provider_user_id = ?")
	var account ebs_fields.AuthAccount
	if err := db.GetContext(ctx, &account, stmt, tenantID, provider, providerUserID); err != nil {
		return nil, err
	}
	return &account, nil
}

func (s *Store) FindUserByEmail(ctx context.Context, tenantID, email string) (*ebs_fields.User, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND email = ? AND deleted_at IS NULL")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, strings.ToLower(email)); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) UpdateUserMobile(ctx context.Context, tenantID string, userID int64, mobile, fullname string) error {
	updates := map[string]any{"mobile": mobile, "username": mobile}
	if fullname != "" {
		updates["fullname"] = fullname
	}
	return s.UpdateUserColumns(ctx, tenantID, userID, updates)
}

func (s *Store) UpdateUserProfile(ctx context.Context, tenantID string, userID int64, profile ebs_fields.UserProfile) error {
	updates := map[string]any{}
	if profile.Fullname != "" {
		updates["fullname"] = profile.Fullname
	}
	if profile.Username != "" {
		updates["username"] = profile.Username
	}
	if profile.Email != "" {
		updates["email"] = profile.Email
	}
	if profile.Birthday != "" {
		updates["birthday"] = profile.Birthday
	}
	if profile.Gender != "" {
		updates["gender"] = profile.Gender
	}
	return s.UpdateUserColumns(ctx, tenantID, userID, updates)
}

func (s *Store) UpdateUserLanguage(ctx context.Context, tenantID string, userID int64, language string) error {
	return s.UpdateUserColumns(ctx, tenantID, userID, map[string]any{"language": language})
}

func (s *Store) SetUserVerified(ctx context.Context, tenantID string, userID int64, verified bool) error {
	return s.UpdateUserColumns(ctx, tenantID, userID, map[string]any{"is_verified": verified})
}

func (s *Store) UpdateUserPassword(ctx context.Context, tenantID string, userID int64, hash string) error {
	return s.UpdateUserColumns(ctx, tenantID, userID, map[string]any{"password": hash})
}

func (s *Store) EnsureUserExists(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, error) {
	user, err := s.GetUserByMobile(ctx, tenantID, mobile)
	if err == nil {
		return user, nil
	}
	return nil, err
}

func (s *Store) FindUserByID(ctx context.Context, tenantID string, id int64) (*ebs_fields.User, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, id); err != nil {
		return nil, err
	}
	s.hydrateUserFields(ctx, tenantID, &user)
	return &user, nil
}

func (s *Store) GetCardsOrFail(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, error) {
	user, err := s.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return nil, err
	}
	cards, err := s.ListCardsByUserID(ctx, tenantID, user.ID)
	if err != nil {
		return nil, err
	}
	if len(cards) == 0 {
		return nil, errors.New("no cards found")
	}
	user.Cards = cards
	return user, nil
}

func (s *Store) GetDeviceIDsByPan(ctx context.Context, tenantID, pan string) ([]string, error) {
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind(`SELECT DISTINCT users.device_id
		FROM users LEFT JOIN cards ON cards.user_id = users.id
		WHERE users.device_id != '' AND ` + s.panLookupClause("cards.pan") + ` AND cards.deleted_at IS NULL AND users.tenant_id = ? AND cards.tenant_id = ?`)
	var devices []string
	args := s.panLookupArgs(pan)
	args = append(args, tenantID, tenantID)
	if err := db.SelectContext(ctx, &devices, stmt, args...); err != nil {
		return nil, err
	}
	return devices, nil
}

func (s *Store) GetTokenWithTransaction(ctx context.Context, tenantID, uuid string) (*ebs_fields.Token, error) {
	token, err := s.GetTokenByUUID(ctx, tenantID, uuid)
	if err != nil {
		return nil, err
	}
	return token, nil
}

func (s *Store) GetAllTokensByUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Token, error) {
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
		s.hydrateTokenFields(ctx, tenantID, &tokens[i])
	}
	return tokens, nil
}

func (s *Store) GetAllTokensByUserIDAndCartID(ctx context.Context, tenantID string, userID int64, cartID string) ([]ebs_fields.Token, error) {
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
		s.hydrateTokenFields(ctx, tenantID, &tokens[i])
	}
	return tokens, nil
}

func (s *Store) GetTokenByUUIDWithResult(ctx context.Context, tenantID, uuid string) (*ebs_fields.Token, error) {
	return s.GetTokenByUUID(ctx, tenantID, uuid)
}

func (s *Store) UpdateTokenCard(ctx context.Context, tenantID string, uuid, toCard string) error {
	db, err := s.ensureDB()
	if err != nil {
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
	stmt := s.DB.Rebind("UPDATE tokens SET to_card = ?, to_card_enc = ?, updated_at = ? WHERE tenant_id = ? AND uuid = ?")
	_, err = db.ExecContext(ctx, stmt, toCardValue, toCardEnc, time.Now().UTC(), tenantID, uuid)
	return err
}

func (s *Store) SaveEBSUUID(ctx context.Context, tenantID string, originalUUID string, res ebs_fields.EBSResponse) error {
	return s.CreateTransaction(ctx, tenantID, res)
}

func (s *Store) UpdatePaymentRequest(ctx context.Context, tenantID string, uuid string, data ebs_fields.QrData) error {
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(data)
	stmt := s.DB.Rebind("UPDATE push_data SET payment_request = ?, updated_at = ? WHERE tenant_id = ? AND uuid = ?")
	_, err = db.ExecContext(ctx, stmt, string(payload), time.Now().UTC(), tenantID, uuid)
	return err
}

// ErrNotFound returns true if the provided error is a not found error.
func ErrNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "no rows") || strings.Contains(err.Error(), "not found")
}
