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

type dbExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type sqlExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

const loginAttemptWindow = 15 * time.Minute

var allowedUserUpdateColumns = map[string]struct{}{
	"password":        {},
	"fullname":        {},
	"username":        {},
	"gender":          {},
	"birthday":        {},
	"email":           {},
	"public_key":      {},
	"device_token":    {},
	"is_password_otp": {},
	"main_card":       {},
	"language":        {},
	"is_verified":     {},
	"mobile":          {},
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

func (s *Store) CreateAPIKey(ctx context.Context, tenantID, email, apiKey string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return ErrMissingEmail
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ErrMissingAPIKey
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("INSERT INTO api_keys(tenant_id, email, api_key, created_at) VALUES(?, ?, ?, ?) ON CONFLICT(tenant_id, email) DO UPDATE SET api_key = excluded.api_key")
	_, err = db.ExecContext(ctx, stmt, tenantID, email, apiKey, time.Now().UTC())
	return err
}

func (s *Store) ValidateAPIKey(ctx context.Context, tenantID, email, apiKey string) (bool, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return false, ErrMissingEmail
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return false, ErrMissingAPIKey
	}
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	stmt := s.DB.Rebind("SELECT api_key FROM api_keys WHERE tenant_id = ? AND email = ?")
	var stored string
	if err := db.GetContext(ctx, &stored, stmt, tenantID, email); err != nil {
		return false, err
	}
	return stored == apiKey, nil
}

func (s *Store) ValidateAPIKeyValue(ctx context.Context, tenantID, apiKey string) (bool, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return false, ErrMissingAPIKey
	}
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
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrMissingUser
	}
	if err := validateUserCreateIdentity(user); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	if err := s.requireDataKeyForSensitiveValue(user.MainCard); err != nil {
		return err
	}
	if err := s.encryptUserFields(user); err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.insertUser(ctx, db, tenantID, user, now); err != nil {
		return err
	}
	user.CreatedAt = now
	user.UpdatedAt = now
	return nil
}

func (s *Store) insertUser(ctx context.Context, exec dbExecutor, tenantID string, user *ebs_fields.User, now time.Time) error {
	stmt := `INSERT INTO users(
		tenant_id, password, fullname, username, gender, birthday, email, is_merchant, public_key, device_id, otp, signed_otp,
		device_token, is_password_otp, main_card, main_card_enc, main_expdate, language, is_verified, mobile, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	args := []any{
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
		"",
		user.Language,
		user.IsVerified,
		user.Mobile,
		now,
		now,
	}
	if s.DB.Driver == DriverPostgres {
		stmt = stmt + " RETURNING id"
		stmt = s.DB.Rebind(stmt)
		if err := exec.QueryRowContext(ctx, stmt, args...).Scan(&user.ID); err != nil {
			return err
		}
	} else {
		stmt = s.DB.Rebind(stmt)
		res, err := exec.ExecContext(ctx, stmt, args...)
		if err != nil {
			return err
		}
		id, _ := res.LastInsertId()
		user.ID = id
	}
	return nil
}

func validateUserCreateIdentity(user *ebs_fields.User) error {
	if strings.TrimSpace(user.Mobile) == "" {
		return ErrMissingMobile
	}
	return nil
}

func (s *Store) GetUserByMobile(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, error) {
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
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND mobile = ? AND deleted_at IS NULL")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, mobile); err != nil {
		return nil, err
	}
	if err := s.hydrateUserFields(ctx, tenantID, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByEmailOrMobile(ctx context.Context, tenantID, query string) (*ebs_fields.User, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrMissingUserIdentifier
	}
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
	if err := s.hydrateUserFields(ctx, tenantID, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByCard(ctx context.Context, tenantID, pan string) (*ebs_fields.User, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	pan = strings.TrimSpace(pan)
	if pan == "" {
		return nil, ErrMissingPAN
	}
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
	if err := s.hydrateUserFields(ctx, tenantID, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) FindUserByUsername(ctx context.Context, tenantID, username string) (*ebs_fields.User, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	username = strings.TrimSpace(username)
	if username == "" {
		return nil, ErrMissingUsername
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND username = ? AND deleted_at IS NULL")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, strings.ToLower(username)); err != nil {
		return nil, err
	}
	if err := s.hydrateUserFields(ctx, tenantID, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetUserByUsernameEmailOrMobile(ctx context.Context, tenantID, query string) (*ebs_fields.User, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, ErrMissingUserIdentifier
	}
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
	if err := s.hydrateUserFields(ctx, tenantID, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) UpdateUser(ctx context.Context, tenantID string, user *ebs_fields.User) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrMissingUser
	}
	if user.ID <= 0 {
		return ErrInvalidUserID
	}
	if s == nil {
		return fmt.Errorf("nil db")
	}
	if err := s.requireDataKeyForSensitiveValue(user.MainCard); err != nil {
		return err
	}
	if err := s.encryptUserFields(user); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	user.UpdatedAt = time.Now().UTC()
	stmt := s.DB.Rebind(`UPDATE users SET
		password = ?, fullname = ?, username = ?, gender = ?, birthday = ?, email = ?, is_merchant = ?, public_key = ?, device_id = ?,
		otp = ?, signed_otp = ?, device_token = ?, is_password_otp = ?, main_card = ?, main_card_enc = ?, main_expdate = ?, language = ?, is_verified = ?, mobile = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`)
	return execContextRequireRowsAffected(ctx, db, stmt,
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
		"",
		user.Language,
		user.IsVerified,
		user.Mobile,
		user.UpdatedAt,
		tenantID,
		user.ID,
	)
}

func (s *Store) UpdateUserColumns(ctx context.Context, tenantID string, userID int64, updates map[string]any) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	if len(updates) == 0 {
		return ErrMissingData
	}
	if err := validateUserUpdateColumns(updates); err != nil {
		return err
	}
	if value, ok := updates["main_card"].(string); ok {
		if err := s.requireDataKeyForSensitiveValue(value); err != nil {
			return err
		}
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
				} else {
					return err
				}
			}
		}
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
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
	return execContextRequireRowsAffected(ctx, db, stmt, args...)
}

func validateUserUpdateColumns(updates map[string]any) error {
	for key := range updates {
		if _, ok := allowedUserUpdateColumns[key]; !ok {
			return fmt.Errorf("%w: %s", ErrInvalidUserColumn, key)
		}
	}
	return nil
}

func (s *Store) UpsertDeviceToken(ctx context.Context, tenantID string, mobile, deviceToken string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	deviceToken = strings.TrimSpace(deviceToken)
	if deviceToken == "" {
		return ErrMissingToken
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("UPDATE users SET device_token = ?, updated_at = ? WHERE tenant_id = ? AND mobile = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, deviceToken, time.Now().UTC(), tenantID, mobile)
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
	stmt := s.DB.Rebind("SELECT * FROM cards WHERE tenant_id = ? AND user_id = ? AND deleted_at IS NULL ORDER BY is_main DESC")
	cards := []ebs_fields.Card{}
	if err := db.SelectContext(ctx, &cards, stmt, tenantID, userID); err != nil {
		return nil, err
	}
	for i := range cards {
		if err := s.hydrateCardFields(ctx, tenantID, &cards[i]); err != nil {
			return nil, err
		}
	}
	return cards, nil
}

func (s *Store) ListCardsByMobile(ctx context.Context, tenantID, mobile string) ([]ebs_fields.Card, error) {
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
	stmt := s.DB.Rebind("SELECT * FROM cards WHERE tenant_id = ? AND mobile = ? AND deleted_at IS NULL ORDER BY is_main DESC")
	cards := []ebs_fields.Card{}
	if err := db.SelectContext(ctx, &cards, stmt, tenantID, mobile); err != nil {
		return nil, err
	}
	for i := range cards {
		if err := s.hydrateCardFields(ctx, tenantID, &cards[i]); err != nil {
			return nil, err
		}
	}
	return cards, nil
}

func (s *Store) AddCards(ctx context.Context, tenantID string, userID int64, cards []ebs_fields.Card) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	now := time.Now().UTC()
	prepared := make([]ebs_fields.Card, 0, len(cards))
	for i := range cards {
		cards[i].Mobile = strings.TrimSpace(cards[i].Mobile)
		if cards[i].Mobile == "" {
			return ErrMissingMobile
		}
		cards[i].Pan = strings.TrimSpace(cards[i].Pan)
		if cards[i].Pan == "" {
			return ErrMissingPAN
		}
		if s == nil {
			return fmt.Errorf("nil db")
		}
		if err := s.requireDataKeyForSensitiveValue(cards[i].Pan, cards[i].IPIN); err != nil {
			return err
		}
		card := cards[i]
		if err := s.encryptCardFields(&card); err != nil {
			return err
		}
		prepared = append(prepared, card)
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	for _, card := range prepared {
		stmt := s.DB.Rebind(`INSERT INTO cards(
			tenant_id, user_id, mobile, pan, pan_enc, expiry, name, ipin, ipin_enc, is_main, is_valid, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if _, err := tx.ExecContext(ctx, stmt,
			tenantID,
			userID,
			card.Mobile,
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
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func (s *Store) UpdateCard(ctx context.Context, tenantID string, userID int64, card ebs_fields.Card) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	card.CardIdx = strings.TrimSpace(card.CardIdx)
	if card.CardIdx == "" {
		return ErrMissingPAN
	}
	card.Pan = strings.TrimSpace(card.Pan)
	if card.Pan == "" {
		return ErrMissingPAN
	}
	if s == nil {
		return fmt.Errorf("nil db")
	}
	if err := s.requireDataKeyForSensitiveValue(card.Pan, card.IPIN); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	if err := s.encryptCardFields(&card); err != nil {
		return err
	}
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
	return execContextRequireRowsAffected(ctx, db, stmt, args...)
}

func (s *Store) DeleteCard(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	cardIdx = strings.TrimSpace(cardIdx)
	if cardIdx == "" {
		return ErrMissingPAN
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	panClause := s.panLookupClause("pan")
	stmt := s.DB.Rebind("UPDATE cards SET deleted_at = ? WHERE tenant_id = ? AND user_id = ? AND " + panClause + " AND deleted_at IS NULL")
	args := []any{time.Now().UTC(), tenantID, userID}
	args = append(args, s.panLookupArgs(cardIdx)...)
	return execContextRequireRowsAffected(ctx, db, stmt, args...)
}

func (s *Store) SetMainCard(ctx context.Context, tenantID string, userID int64, cardIdx string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	cardIdx = strings.TrimSpace(cardIdx)
	if cardIdx == "" {
		return ErrMissingPAN
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	resetStmt := db.Rebind("UPDATE cards SET is_main = FALSE, updated_at = ? WHERE tenant_id = ? AND user_id = ? AND deleted_at IS NULL")
	if _, err := tx.ExecContext(ctx, resetStmt, time.Now().UTC(), tenantID, userID); err != nil {
		_ = tx.Rollback()
		return err
	}
	panClause := s.panLookupClause("pan")
	setStmt := db.Rebind("UPDATE cards SET is_main = TRUE, updated_at = ? WHERE tenant_id = ? AND user_id = ? AND " + panClause + " AND deleted_at IS NULL")
	args := []any{time.Now().UTC(), tenantID, userID}
	args = append(args, s.panLookupArgs(cardIdx)...)
	result, err := tx.ExecContext(ctx, setStmt, args...)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if affected == 0 {
		_ = tx.Rollback()
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) GetPanByMobile(ctx context.Context, tenantID, mobile string) (string, error) {
	cards, err := s.ListCardsByMobile(ctx, tenantID, mobile)
	if err != nil {
		return "", err
	}
	if len(cards) == 0 {
		return "", errors.New("no cards")
	}
	return cards[0].Pan, nil
}

func (s *Store) ListBeneficiaries(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.Beneficiary, error) {
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
	stmt := s.DB.Rebind("SELECT * FROM beneficiaries WHERE tenant_id = ? AND user_id = ?")
	var list []ebs_fields.Beneficiary
	if err := db.SelectContext(ctx, &list, stmt, tenantID, userID); err != nil {
		return nil, err
	}
	return list, nil
}

func (s *Store) UpsertBeneficiary(ctx context.Context, tenantID string, userID int64, b ebs_fields.Beneficiary) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	b.Data = strings.TrimSpace(b.Data)
	if b.Data == "" {
		return ErrMissingData
	}
	b.BillType = strings.TrimSpace(b.BillType)
	if b.BillType == "" {
		return ErrMissingBillType
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO beneficiaries(tenant_id, user_id, data, bill_type, name, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, user_id, data) DO UPDATE SET
			bill_type = excluded.bill_type,
			name = excluded.name,
			updated_at = excluded.updated_at`)
	_, err = db.ExecContext(ctx, stmt, tenantID, userID, b.Data, b.BillType, b.Name, now, now)
	return err
}

func (s *Store) DeleteBeneficiary(ctx context.Context, tenantID string, userID int64, data string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	data = strings.TrimSpace(data)
	if data == "" {
		return ErrMissingData
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind("DELETE FROM beneficiaries WHERE tenant_id = ? AND user_id = ? AND data = ?")
	return execContextRequireRowsAffected(ctx, db, stmt, tenantID, userID, data)
}

func (s *Store) UpsertCacheCard(ctx context.Context, tenantID string, card ebs_fields.CacheCards) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	card.Pan = strings.TrimSpace(card.Pan)
	if card.Pan == "" {
		return ErrMissingPAN
	}
	if s == nil {
		return fmt.Errorf("nil db")
	}
	if err := s.requireDataKeyForSensitiveValue(card.Pan); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	if err := s.encryptCacheCardFields(&card); err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO cache_cards(tenant_id, pan, pan_enc, expiry, name, is_valid, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, pan) DO UPDATE SET is_valid = excluded.is_valid, pan_enc = excluded.pan_enc, expiry = excluded.expiry, name = excluded.name, updated_at = excluded.updated_at`)
	_, err = db.ExecContext(ctx, stmt, tenantID, card.Pan, card.PanEnc, card.Expiry, card.Name, card.IsValid, now, now)
	return err
}

func (s *Store) GetCacheCard(ctx context.Context, tenantID, pan string) (*ebs_fields.CacheCards, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	pan = strings.TrimSpace(pan)
	if pan == "" {
		return nil, ErrMissingPAN
	}
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
	if err := s.hydrateCacheCardFields(ctx, tenantID, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

func (s *Store) CardExists(ctx context.Context, tenantID, pan string) (bool, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(pan) == "" {
		return false, ErrMissingPAN
	}
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

func (s *Store) RecordLoginAttempt(ctx context.Context, tenantID, mobile string, increment bool) (int, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(mobile) == "" {
		return 0, ErrMissingMobile
	}
	db, err := s.ensureDB()
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	cutoff := now.Add(-loginAttemptWindow)
	incrementBy := 0
	if increment {
		incrementBy = 1
	}
	stmt := s.DB.Rebind(`INSERT INTO login_metrics(
		tenant_id, mobile, login_count, window_started_at, suspicious_count, updated_at
	) VALUES(?, ?, ?, ?, 0, ?)
	ON CONFLICT(tenant_id, mobile) DO UPDATE SET
		login_count = CASE
			WHEN login_metrics.window_started_at <= ? OR login_metrics.window_started_at > ?
			THEN EXCLUDED.login_count
			ELSE login_metrics.login_count + ?
		END,
		window_started_at = CASE
			WHEN login_metrics.window_started_at <= ? OR login_metrics.window_started_at > ?
			THEN EXCLUDED.window_started_at
			ELSE login_metrics.window_started_at
		END,
		updated_at = EXCLUDED.updated_at
	RETURNING login_count`)
	var count int
	if err := db.QueryRowContext(ctx, stmt,
		tenantID, mobile, incrementBy, now, now,
		cutoff, now, incrementBy,
		cutoff, now,
	).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) IncrementSuspicious(ctx context.Context, tenantID, mobile string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(mobile) == "" {
		return ErrMissingMobile
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO login_metrics(
		tenant_id, mobile, login_count, window_started_at, suspicious_count, updated_at
	) VALUES(?, ?, 0, ?, 1, ?)
	ON CONFLICT(tenant_id, mobile) DO UPDATE SET
		suspicious_count = login_metrics.suspicious_count + 1,
		updated_at = EXCLUDED.updated_at`)
	_, err = db.ExecContext(ctx, stmt, tenantID, mobile, now, now)
	return err
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
		records = append(records, record)
	}
	return records, rows.Err()
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

func (s *Store) UpdateKYC(ctx context.Context, tenantID string, kyc *ebs_fields.KYC, passport *ebs_fields.Passport) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	identity, err := validateKYCIdentity(kyc, passport)
	if err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	userStmt := db.Rebind("SELECT 1 FROM users WHERE tenant_id = ? AND mobile = ? AND deleted_at IS NULL")
	var userExists int
	if err := tx.GetContext(ctx, &userExists, userStmt, tenantID, identity.mobile); err != nil {
		_ = tx.Rollback()
		return err
	}
	kycStmt := db.Rebind(`INSERT INTO kyc(tenant_id, user_mobile, mobile, selfie, passport_img, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, mobile) DO UPDATE SET user_mobile = excluded.user_mobile, selfie = excluded.selfie, passport_img = excluded.passport_img, updated_at = excluded.updated_at`)
	if _, err := tx.ExecContext(ctx, kycStmt, tenantID, identity.userMobile, identity.mobile, kyc.Selfie, kyc.PassportImg, now, now); err != nil {
		_ = tx.Rollback()
		return err
	}
	if passport != nil {
		passStmt := db.Rebind(`INSERT INTO passports(tenant_id, mobile, birth_date, issue_date, expiration_date, national_number, passport_number, gender, nationality, holder_name, created_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(tenant_id, mobile) DO UPDATE SET birth_date = excluded.birth_date, issue_date = excluded.issue_date, expiration_date = excluded.expiration_date,
			national_number = excluded.national_number, passport_number = excluded.passport_number, gender = excluded.gender, nationality = excluded.nationality, holder_name = excluded.holder_name, updated_at = excluded.updated_at`)
		if _, err := tx.ExecContext(ctx, passStmt, tenantID, identity.passportMobile, passport.BirthDate, passport.IssueDate, passport.ExpirationDate, passport.NationalNumber, passport.PassportNumber, passport.Gender, passport.Nationality, passport.HolderName, now, now); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

type kycIdentity struct {
	userMobile     string
	mobile         string
	passportMobile string
}

func validateKYCIdentity(kyc *ebs_fields.KYC, passport *ebs_fields.Passport) (kycIdentity, error) {
	if kyc == nil {
		return kycIdentity{}, ErrMissingKYC
	}
	identity := kycIdentity{
		userMobile: strings.TrimSpace(kyc.UserMobile),
		mobile:     strings.TrimSpace(kyc.Mobile),
	}
	if identity.userMobile == "" || identity.mobile == "" {
		return kycIdentity{}, ErrMissingMobile
	}
	if identity.userMobile != identity.mobile {
		return kycIdentity{}, ErrInvalidMobile
	}
	if passport == nil {
		return identity, nil
	}
	identity.passportMobile = strings.TrimSpace(passport.Mobile)
	if identity.passportMobile == "" {
		return kycIdentity{}, ErrMissingMobile
	}
	if identity.passportMobile != identity.mobile {
		return kycIdentity{}, ErrInvalidMobile
	}
	return identity, nil
}

func (s *Store) GetUserWithKYC(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, *ebs_fields.KYC, *ebs_fields.Passport, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, nil, nil, err
	}
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
		if ErrNotFound(err) {
			return user, nil, nil, nil
		}
		return nil, nil, nil, err
	}
	passStmt := s.DB.Rebind("SELECT * FROM passports WHERE tenant_id = ? AND mobile = ?")
	var passport ebs_fields.Passport
	if err := db.GetContext(ctx, &passport, passStmt, tenantID, mobile); err != nil {
		if ErrNotFound(err) {
			return user, &kyc, nil, nil
		}
		return nil, nil, nil, err
	}
	return user, &kyc, &passport, nil
}

func (s *Store) LinkAuthAccount(ctx context.Context, tenantID string, account *ebs_fields.AuthAccount) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if err := validateAuthAccount(account); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	return s.linkAuthAccount(ctx, db, tenantID, account, time.Now().UTC())
}

func (s *Store) CreateUserWithAuthAccount(ctx context.Context, tenantID string, user *ebs_fields.User, account *ebs_fields.AuthAccount) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrMissingUser
	}
	if err := validateUserCreateIdentity(user); err != nil {
		return err
	}
	if err := validateAuthAccountForNewUser(account); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	if err := s.requireDataKeyForSensitiveValue(user.MainCard); err != nil {
		return err
	}
	if err := s.encryptUserFields(user); err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if err := s.insertUser(ctx, tx, tenantID, user, now); err != nil {
		_ = tx.Rollback()
		return err
	}
	account.UserID = user.ID
	if err := s.linkAuthAccount(ctx, tx, tenantID, account, now); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	user.CreatedAt = now
	user.UpdatedAt = now
	return nil
}

func validateAuthAccountForNewUser(account *ebs_fields.AuthAccount) error {
	if account == nil {
		return ErrMissingAccount
	}
	if strings.TrimSpace(account.Provider) == "" {
		return ErrMissingProvider
	}
	if strings.TrimSpace(account.ProviderUserID) == "" {
		return ErrMissingProviderUserID
	}
	return nil
}

func validateAuthAccount(account *ebs_fields.AuthAccount) error {
	if err := validateAuthAccountForNewUser(account); err != nil {
		return err
	}
	if account.UserID <= 0 {
		return ErrInvalidUserID
	}
	return nil
}

func (s *Store) linkAuthAccount(ctx context.Context, exec dbExecutor, tenantID string, account *ebs_fields.AuthAccount, now time.Time) error {
	if err := s.requireAuthAccountUser(ctx, exec, tenantID, account.UserID); err != nil {
		return err
	}
	provider := strings.TrimSpace(account.Provider)
	providerUserID := strings.TrimSpace(account.ProviderUserID)
	email := strings.ToLower(account.Email)
	stmt := s.DB.Rebind(`INSERT INTO auth_accounts(tenant_id, user_id, provider, provider_user_id, email, email_verified, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(tenant_id, provider, provider_user_id) DO NOTHING
		RETURNING id`)
	var id int64
	if err := exec.QueryRowContext(ctx, stmt, tenantID, account.UserID, provider, providerUserID, email, account.EmailVerified, now, now).Scan(&id); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		existing, err := s.getAuthAccountByProvider(ctx, exec, tenantID, provider, providerUserID)
		if err != nil {
			return err
		}
		if !authAccountReplayMatches(existing, tenantID, account.UserID, provider, providerUserID, email, account.EmailVerified) {
			return ErrDuplicateAuthAccount
		}
		return nil
	}
	return nil
}

func (s *Store) FindAuthAccount(ctx context.Context, tenantID, provider, providerUserID string) (*ebs_fields.AuthAccount, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, ErrMissingProvider
	}
	providerUserID = strings.TrimSpace(providerUserID)
	if providerUserID == "" {
		return nil, ErrMissingProviderUserID
	}
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

func (s *Store) requireAuthAccountUser(ctx context.Context, exec dbExecutor, tenantID string, userID int64) error {
	stmt := s.DB.Rebind("SELECT id FROM users WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL")
	var id int64
	return exec.QueryRowContext(ctx, stmt, tenantID, userID).Scan(&id)
}

func (s *Store) getAuthAccountByProvider(ctx context.Context, exec dbExecutor, tenantID, provider, providerUserID string) (*ebs_fields.AuthAccount, error) {
	stmt := s.DB.Rebind(`SELECT id, tenant_id, user_id, provider, provider_user_id, email, email_verified, created_at, updated_at
		FROM auth_accounts WHERE tenant_id = ? AND provider = ? AND provider_user_id = ?`)
	var account ebs_fields.AuthAccount
	if err := exec.QueryRowContext(ctx, stmt, tenantID, provider, providerUserID).Scan(
		&account.ID,
		&account.TenantID,
		&account.UserID,
		&account.Provider,
		&account.ProviderUserID,
		&account.Email,
		&account.EmailVerified,
		&account.CreatedAt,
		&account.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &account, nil
}

func authAccountReplayMatches(account *ebs_fields.AuthAccount, tenantID string, userID int64, provider, providerUserID, email string, emailVerified bool) bool {
	if account == nil {
		return false
	}
	return account.TenantID == tenantID &&
		account.UserID == userID &&
		account.Provider == provider &&
		account.ProviderUserID == providerUserID &&
		account.Email == email &&
		account.EmailVerified == emailVerified
}

func (s *Store) FindUserByEmail(ctx context.Context, tenantID, email string) (*ebs_fields.User, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, ErrMissingEmail
	}
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
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	updates := map[string]any{"mobile": mobile, "username": mobile}
	if fullname != "" {
		updates["fullname"] = fullname
	}
	return s.UpdateUserColumns(ctx, tenantID, userID, updates)
}

func (s *Store) UpdateUserProfile(ctx context.Context, tenantID string, userID int64, profile ebs_fields.UserProfile) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	updates := map[string]any{}
	if profile.Fullname != "" {
		fullname := strings.TrimSpace(profile.Fullname)
		if fullname != "" {
			updates["fullname"] = fullname
		}
	}
	if profile.Username != "" {
		username := strings.TrimSpace(profile.Username)
		if username == "" {
			return ErrMissingUsername
		}
		updates["username"] = username
	}
	if profile.Email != "" {
		email := strings.TrimSpace(profile.Email)
		if email == "" {
			return ErrMissingEmail
		}
		updates["email"] = strings.ToLower(email)
	}
	if profile.Birthday != "" {
		birthday := strings.TrimSpace(profile.Birthday)
		if birthday != "" {
			updates["birthday"] = birthday
		}
	}
	if profile.Gender != "" {
		gender := strings.TrimSpace(profile.Gender)
		if gender != "" {
			updates["gender"] = gender
		}
	}
	if len(updates) == 0 {
		return ErrMissingData
	}
	return s.UpdateUserColumns(ctx, tenantID, userID, updates)
}

func (s *Store) UpdateUserLanguage(ctx context.Context, tenantID string, userID int64, language string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	language = strings.TrimSpace(language)
	if language == "" {
		return ErrMissingLanguage
	}
	return s.UpdateUserColumns(ctx, tenantID, userID, map[string]any{"language": language})
}

func (s *Store) SetUserVerified(ctx context.Context, tenantID string, userID int64, verified bool) error {
	return s.UpdateUserColumns(ctx, tenantID, userID, map[string]any{"is_verified": verified})
}

func (s *Store) UpdateUserPassword(ctx context.Context, tenantID string, userID int64, hash string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	if strings.TrimSpace(hash) == "" {
		return ErrMissingPassword
	}
	return s.UpdateUserColumns(ctx, tenantID, userID, map[string]any{"password": hash})
}

func (s *Store) RotateUserPassword(ctx context.Context, tenantID string, userID int64, hash string, now time.Time) (int64, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return 0, err
	}
	if userID <= 0 {
		return 0, ErrInvalidUserID
	}
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return 0, ErrMissingPassword
	}
	if now.IsZero() {
		return 0, ErrInvalidAuthTime
	}
	db, err := s.ensureDB()
	if err != nil {
		return 0, err
	}
	stmt := s.DB.Rebind(`UPDATE users
		SET password = ?, session_epoch = session_epoch + 1, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND is_verified = TRUE
		RETURNING session_epoch`)
	var sessionEpoch int64
	if err := db.QueryRowContext(ctx, stmt, hash, now.UTC(), tenantID, userID).Scan(&sessionEpoch); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, sql.ErrNoRows
		}
		return 0, err
	}
	return sessionEpoch, nil
}

func (s *Store) EnsureUserExists(ctx context.Context, tenantID, mobile string) (*ebs_fields.User, error) {
	user, err := s.GetUserByMobile(ctx, tenantID, mobile)
	if err == nil {
		return user, nil
	}
	return nil, err
}

func (s *Store) FindUserByID(ctx context.Context, tenantID string, id int64) (*ebs_fields.User, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if id <= 0 {
		return nil, ErrInvalidUserID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind("SELECT * FROM users WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL")
	var user ebs_fields.User
	if err := db.GetContext(ctx, &user, stmt, tenantID, id); err != nil {
		return nil, err
	}
	if err := s.hydrateUserFields(ctx, tenantID, &user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *Store) GetDeviceIDsByPan(ctx context.Context, tenantID, pan string) ([]string, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	pan = strings.TrimSpace(pan)
	if pan == "" {
		return nil, ErrMissingPAN
	}
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
