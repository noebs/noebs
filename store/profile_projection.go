package store

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/jackc/pgx/v5/pgconn"
)

const maxOIDCSubjectBytes = 255

// PrincipalIdentity is the immutable authority for one tenant profile.
type PrincipalIdentity struct {
	TenantID string
	Issuer   string
	Subject  string
}

// ProfileProjection is application data attached to an external principal.
// UserID is a domain identifier, not an authentication credential.
type ProfileProjection struct {
	PrincipalIdentity
	UserID      int64
	Fullname    string
	Username    string
	Gender      string
	Birthday    string
	Email       string
	DeviceToken string
	Language    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type CreateProfileProjectionParams struct {
	PrincipalIdentity
	Fullname    string
	Username    string
	Gender      string
	Birthday    string
	Email       string
	DeviceToken string
	Language    string
}

type ProfileProjectionUpdate struct {
	Fullname *string
	Username *string
	Gender   *string
	Birthday *string
	Email    *string
}

func (s *Store) CreateProfileProjection(ctx context.Context, params CreateProfileProjectionParams) (ProfileProjection, error) {
	if err := validatePrincipalIdentity(params.PrincipalIdentity); err != nil {
		return ProfileProjection{}, err
	}
	if err := validateProfileCreate(params); err != nil {
		return ProfileProjection{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return ProfileProjection{}, err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO users(
		tenant_id, issuer, subject, fullname, username, gender, birthday, email,
		device_token, language, created_at, updated_at
	) VALUES(?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		NULLIF(?, ''), NULLIF(?, ''), ?, ?)
	ON CONFLICT (tenant_id, issuer, subject) DO NOTHING
	RETURNING id, created_at, updated_at`)
	projection := profileProjectionFromCreate(params)
	err = db.QueryRowContext(ctx, stmt,
		params.TenantID, params.Issuer, params.Subject, params.Fullname,
		params.Username, params.Gender, params.Birthday, params.Email,
		params.DeviceToken, params.Language, now, now,
	).Scan(&projection.UserID, &projection.CreatedAt, &projection.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProfileProjection{}, ErrProfileAlreadyExists
	}
	if err != nil {
		return ProfileProjection{}, profileMutationError(err)
	}
	return projection, nil
}

func (s *Store) ResolveProfileProjection(ctx context.Context, identity PrincipalIdentity) (ProfileProjection, error) {
	if err := validatePrincipalIdentity(identity); err != nil {
		return ProfileProjection{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return ProfileProjection{}, err
	}
	stmt := s.DB.Rebind(profileProjectionSelect + ` WHERE tenant_id = ? AND issuer = ? AND subject = ?`)
	return scanProfileProjection(db.QueryRowContext(ctx, stmt, identity.TenantID, identity.Issuer, identity.Subject))
}

func (s *Store) FindProfileProjectionByUserID(ctx context.Context, tenantID string, userID int64) (ProfileProjection, error) {
	if err := validateProjectionUserID(tenantID, userID); err != nil {
		return ProfileProjection{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return ProfileProjection{}, err
	}
	stmt := s.DB.Rebind(profileProjectionSelect + ` WHERE tenant_id = ? AND id = ?`)
	return scanProfileProjection(db.QueryRowContext(ctx, stmt, tenantID, userID))
}

func (s *Store) UpdateProfileProjection(ctx context.Context, tenantID string, userID int64, update ProfileProjectionUpdate) error {
	if err := validateProjectionUserID(tenantID, userID); err != nil {
		return err
	}
	set, args, err := profileUpdateColumns(update)
	if err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	args = append(args, time.Now().UTC(), tenantID, userID)
	stmt := s.DB.Rebind(`UPDATE users SET ` + strings.Join(set, ", ") + `, updated_at = ? WHERE tenant_id = ? AND id = ?`)
	return profileMutationError(execContextRequireRowsAffected(ctx, db, stmt, args...))
}

func (s *Store) SetProfileLanguage(ctx context.Context, tenantID string, userID int64, language string) error {
	if err := validateProjectionUserID(tenantID, userID); err != nil {
		return err
	}
	if !validOptionalProfileValue(language) || language == "" {
		return ErrMissingLanguage
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE users SET language = ?, updated_at = ? WHERE tenant_id = ? AND id = ?`)
	return execContextRequireRowsAffected(ctx, db, stmt, language, time.Now().UTC(), tenantID, userID)
}

func (s *Store) SetProfileDeviceToken(ctx context.Context, tenantID string, userID int64, deviceToken string) error {
	if err := validateProjectionUserID(tenantID, userID); err != nil {
		return err
	}
	if !validOptionalProfileValue(deviceToken) || deviceToken == "" {
		return ErrMissingToken
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE users SET device_token = ?, updated_at = ? WHERE tenant_id = ? AND id = ?`)
	return execContextRequireRowsAffected(ctx, db, stmt, deviceToken, time.Now().UTC(), tenantID, userID)
}

func (s *Store) UpdateProfileKYC(ctx context.Context, tenantID string, userID int64, request ebs_fields.KYCPassport) error {
	if err := validateProjectionUserID(tenantID, userID); err != nil {
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
	defer func() { _ = tx.Rollback() }()
	var exists int
	userStmt := s.DB.Rebind(`SELECT 1 FROM users WHERE tenant_id = ? AND id = ? FOR UPDATE`)
	if err := tx.QueryRowContext(ctx, userStmt, tenantID, userID).Scan(&exists); err != nil {
		return err
	}
	kycStmt := s.DB.Rebind(`INSERT INTO kyc(tenant_id, user_id, selfie, passport_img, created_at, updated_at)
		VALUES(?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
		ON CONFLICT(tenant_id, user_id) DO UPDATE SET
			selfie = excluded.selfie,
			passport_img = excluded.passport_img,
			updated_at = excluded.updated_at`)
	if _, err := tx.ExecContext(ctx, kycStmt, tenantID, userID, request.Selfie, request.PassportImg, now, now); err != nil {
		return err
	}
	if profilePassportPresent(request.Passport) {
		passport := request.Passport
		passportStmt := s.DB.Rebind(`INSERT INTO passports(
			tenant_id, user_id, birth_date, issue_date, expiration_date, national_number,
			passport_number, gender, nationality, holder_name, created_at, updated_at
		) VALUES(?, ?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)
		ON CONFLICT(tenant_id, user_id) DO UPDATE SET
			birth_date = excluded.birth_date,
			issue_date = excluded.issue_date,
			expiration_date = excluded.expiration_date,
			national_number = excluded.national_number,
			passport_number = excluded.passport_number,
			gender = excluded.gender,
			nationality = excluded.nationality,
			holder_name = excluded.holder_name,
			updated_at = excluded.updated_at`)
		if _, err := tx.ExecContext(ctx, passportStmt,
			tenantID, userID, nullableTime(passport.BirthDate), nullableTime(passport.IssueDate), nullableTime(passport.ExpirationDate),
			passport.NationalNumber, passport.PassportNumber, passport.Gender, passport.Nationality, passport.HolderName, now, now,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetProfileKYC(ctx context.Context, tenantID string, userID int64) (*ebs_fields.KYC, *ebs_fields.Passport, error) {
	if err := validateProjectionUserID(tenantID, userID); err != nil {
		return nil, nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, nil, err
	}
	kycStmt := s.DB.Rebind(`SELECT user_id, tenant_id, COALESCE(selfie, ''), COALESCE(passport_img, ''), created_at, updated_at
		FROM kyc WHERE tenant_id = ? AND user_id = ?`)
	kyc := &ebs_fields.KYC{}
	if err := db.QueryRowContext(ctx, kycStmt, tenantID, userID).Scan(
		&kyc.UserID, &kyc.TenantID, &kyc.Selfie, &kyc.PassportImg, &kyc.CreatedAt, &kyc.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	passportStmt := s.DB.Rebind(`SELECT user_id, tenant_id, birth_date, issue_date, expiration_date,
		COALESCE(national_number, ''), COALESCE(passport_number, ''), COALESCE(gender, ''),
		COALESCE(nationality, ''), COALESCE(holder_name, ''), created_at, updated_at
		FROM passports WHERE tenant_id = ? AND user_id = ?`)
	passport := &ebs_fields.Passport{}
	var birthDate, issueDate, expirationDate sql.NullTime
	if err := db.QueryRowContext(ctx, passportStmt, tenantID, userID).Scan(
		&passport.UserID, &passport.TenantID, &birthDate, &issueDate, &expirationDate,
		&passport.NationalNumber, &passport.PassportNumber, &passport.Gender,
		&passport.Nationality, &passport.HolderName, &passport.CreatedAt, &passport.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return kyc, nil, nil
		}
		return nil, nil, err
	}
	passport.BirthDate = birthDate.Time
	passport.IssueDate = issueDate.Time
	passport.ExpirationDate = expirationDate.Time
	return kyc, passport, nil
}

const profileProjectionSelect = `SELECT
	id, tenant_id, issuer, subject, fullname,
	COALESCE(username, ''), COALESCE(gender, ''), COALESCE(birthday, ''),
	COALESCE(email, ''), COALESCE(device_token, ''), COALESCE(language, ''),
	created_at, updated_at
	FROM users`

type rowScanner interface {
	Scan(...any) error
}

func scanProfileProjection(row rowScanner) (ProfileProjection, error) {
	var profile ProfileProjection
	err := row.Scan(
		&profile.UserID, &profile.TenantID, &profile.Issuer, &profile.Subject,
		&profile.Fullname, &profile.Username, &profile.Gender, &profile.Birthday,
		&profile.Email, &profile.DeviceToken, &profile.Language,
		&profile.CreatedAt, &profile.UpdatedAt,
	)
	return profile, err
}

func profileMutationError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrProfileContactConflict
	}
	return err
}

func profileProjectionFromCreate(params CreateProfileProjectionParams) ProfileProjection {
	return ProfileProjection{
		PrincipalIdentity: params.PrincipalIdentity,
		Fullname:          params.Fullname,
		Username:          params.Username,
		Gender:            params.Gender,
		Birthday:          params.Birthday,
		Email:             params.Email,
		DeviceToken:       params.DeviceToken,
		Language:          params.Language,
	}
}

func validatePrincipalIdentity(identity PrincipalIdentity) error {
	if _, err := validateExactTenantID(identity.TenantID); err != nil {
		return err
	}
	if identity.Issuer == "" {
		return ErrMissingIssuer
	}
	issuer, err := url.Parse(identity.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" ||
		identity.Issuer != strings.TrimSpace(identity.Issuer) {
		return ErrInvalidIssuer
	}
	if identity.Subject == "" {
		return ErrMissingSubject
	}
	if len(identity.Subject) > maxOIDCSubjectBytes || !utf8.ValidString(identity.Subject) || identity.Subject != strings.TrimSpace(identity.Subject) ||
		strings.IndexFunc(identity.Subject, unicode.IsControl) >= 0 {
		return ErrInvalidSubject
	}
	return nil
}

func validateExactTenantID(tenantID string) (string, error) {
	validated, err := ValidateTenantID(tenantID)
	if err != nil {
		return "", err
	}
	if validated != tenantID {
		return "", ErrInvalidTenantID
	}
	return validated, nil
}

func validateProfileCreate(params CreateProfileProjectionParams) error {
	if err := validateRequiredProfileName(params.Fullname); err != nil {
		return err
	}
	for _, value := range []string{params.Username, params.Gender, params.Birthday, params.Email, params.DeviceToken, params.Language} {
		if !validOptionalProfileValue(value) {
			return ErrMissingData
		}
	}
	return nil
}

func validateRequiredProfileName(value string) error {
	if value == "" {
		return ErrMissingProfileName
	}
	if value != strings.TrimSpace(value) {
		return ErrInvalidProfileName
	}
	return nil
}

func validOptionalProfileValue(value string) bool {
	return value == "" || value == strings.TrimSpace(value)
}

func validateProjectionUserID(tenantID string, userID int64) error {
	if _, err := validateExactTenantID(tenantID); err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	return nil
}

func profilePassportPresent(passport ebs_fields.Passport) bool {
	return !passport.BirthDate.IsZero() || !passport.IssueDate.IsZero() || !passport.ExpirationDate.IsZero() ||
		passport.NationalNumber != "" || passport.PassportNumber != "" || passport.Gender != "" ||
		passport.Nationality != "" || passport.HolderName != ""
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

func profileUpdateColumns(update ProfileProjectionUpdate) ([]string, []any, error) {
	set := make([]string, 0, 5)
	args := make([]any, 0, 5)
	add := func(column string, value *string, validate func(string) error) error {
		if value == nil {
			return nil
		}
		if err := validate(*value); err != nil {
			return err
		}
		set = append(set, column+" = ?")
		args = append(args, *value)
		return nil
	}
	if err := add("fullname", update.Fullname, validateRequiredProfileName); err != nil {
		return nil, nil, err
	}
	optional := func(value string) error {
		if !validOptionalProfileValue(value) || value == "" {
			return ErrMissingData
		}
		return nil
	}
	for _, field := range []struct {
		column string
		value  *string
	}{
		{"username", update.Username},
		{"gender", update.Gender},
		{"birthday", update.Birthday},
		{"email", update.Email},
	} {
		if err := add(field.column, field.value, optional); err != nil {
			return nil, nil, err
		}
	}
	if len(set) == 0 {
		return nil, nil, ErrMissingData
	}
	return set, args, nil
}
