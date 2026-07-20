package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

const (
	CardStatusActive  = "active"
	CardStatusRetired = "retired"
	CardStatusBlocked = "blocked"

	EnrollmentIntentPending    = "pending"
	EnrollmentIntentProcessing = "processing"
	EnrollmentIntentCompleted  = "completed"
	EnrollmentIntentFailed     = "failed"
	EnrollmentIntentExpired    = "expired"

	CardEnrollmentOperation = "card_enrollment"
	cardPANKeyVersion       = 1
)

type CardEnrollmentIntent struct {
	EnrollmentID    string                  `db:"enrollment_id" json:"enrollment_id"`
	RailUUID        string                  `db:"rail_uuid" json:"rail_uuid"`
	Status          string                  `db:"status" json:"-"`
	ExpiresAt       time.Time               `db:"expires_at" json:"expires_at"`
	RequestClaim    string                  `db:"request_claim" json:"-"`
	RailSubmitted   sql.NullTime            `db:"rail_submitted_at" json:"-"`
	CompletedCardID sql.NullString          `db:"completed_card_id" json:"-"`
	CompletedCard   *ebs_fields.CardSummary `db:"-" json:"-"`
}

type CardEnrollmentAttempt struct {
	PAN           string
	Expiry        string
	Name          string
	OperationKind string
}

type VerifiedCardEnrollment struct {
	PAN                string
	Expiry             string
	Name               string
	VerificationMethod string
}

func NormalizeCardID(value string) (string, error) {
	return normalizeRequiredCanonicalUUID(value, ErrMissingCardID, ErrInvalidCardID)
}

func NormalizeEnrollmentID(value string) (string, error) {
	return normalizeRequiredCanonicalUUID(value, ErrInvalidEnrollmentIntent, ErrInvalidEnrollmentIntent)
}

func NormalizeRailUUID(value string) (string, error) {
	return normalizeRequiredCanonicalUUID(value, ErrMissingRailUUID, ErrInvalidRailUUID)
}

func normalizeRequiredCanonicalUUID(value string, missingErr, invalidErr error) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", missingErr
	}
	if value != trimmed {
		return "", invalidErr
	}
	return normalizeCanonicalUUID(value, invalidErr)
}

func normalizeCanonicalUUID(value string, invalidErr error) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil || parsed.String() != value {
		return "", invalidErr
	}
	return value, nil
}

func (s *Store) ListActiveCardSummaries(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.CardSummary, error) {
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

	stmt := db.Rebind(`SELECT card_id::text AS card_id,
		COALESCE(name, '') AS name,
		masked_pan,
		COALESCE(expiry, '') AS expiry,
		is_main,
		status
		FROM cards
		WHERE tenant_id = ? AND user_id = ?
		  AND status = 'active' AND verified_at IS NOT NULL
		ORDER BY is_main DESC, created_at ASC, card_id ASC`)
	cards := make([]ebs_fields.CardSummary, 0)
	if err := db.SelectContext(ctx, &cards, stmt, tenantID, userID); err != nil {
		return nil, err
	}
	return cards, nil
}

func (s *Store) UpdateActiveCardName(ctx context.Context, tenantID string, userID int64, cardID, name string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	cardID, err = NormalizeCardID(cardID)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if len(name) > 100 {
		return ErrMissingData
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE cards
		SET name = ?, updated_at = ?
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid AND status = 'active'`)
	if err := execContextRequireRowsAffected(ctx, db, stmt, name, time.Now().UTC(), tenantID, userID, cardID); err != nil {
		return nonEnumeratingCardError(err)
	}
	return nil
}

func (s *Store) RetireActiveCard(ctx context.Context, tenantID string, userID int64, cardID string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	cardID, err = NormalizeCardID(cardID)
	if err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockCardOwner(ctx, tx, tenantID, userID); err != nil {
		return err
	}
	findStmt := tx.Rebind(`SELECT is_main FROM cards
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid AND status = 'active'
		FOR UPDATE`)
	var wasMain bool
	if err := tx.GetContext(ctx, &wasMain, findStmt, tenantID, userID, cardID); err != nil {
		return nonEnumeratingCardError(err)
	}
	now := time.Now().UTC()
	stmt := tx.Rebind(`UPDATE cards
		SET status = 'retired', is_main = FALSE, retired_at = ?, updated_at = ?
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid AND status = 'active'`)
	if err := execContextRequireRowsAffected(ctx, tx, stmt, now, now, tenantID, userID, cardID); err != nil {
		return nonEnumeratingCardError(err)
	}
	if wasMain {
		promoteStmt := tx.Rebind(`UPDATE cards SET is_main = TRUE, updated_at = ?
			WHERE id = (
				SELECT id FROM cards
				WHERE tenant_id = ? AND user_id = ?
				  AND status = 'active' AND verified_at IS NOT NULL
				ORDER BY created_at ASC, card_id ASC
				LIMIT 1
			)`)
		if _, err := tx.ExecContext(ctx, promoteStmt, now, tenantID, userID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) SetActiveMainCard(ctx context.Context, tenantID string, userID int64, cardID string) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	cardID, err = NormalizeCardID(cardID)
	if err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := lockCardOwner(ctx, tx, tenantID, userID); err != nil {
		return err
	}
	var exists bool
	findStmt := tx.Rebind(`SELECT EXISTS(
		SELECT 1 FROM cards
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid
		  AND status = 'active' AND verified_at IS NOT NULL
	)`)
	if err := tx.GetContext(ctx, &exists, findStmt, tenantID, userID, cardID); err != nil {
		return err
	}
	if !exists {
		return ErrCardNotFound
	}
	now := time.Now().UTC()
	resetStmt := tx.Rebind(`UPDATE cards SET is_main = FALSE, updated_at = ?
		WHERE tenant_id = ? AND user_id = ? AND status = 'active' AND is_main = TRUE`)
	if _, err := tx.ExecContext(ctx, resetStmt, now, tenantID, userID); err != nil {
		return err
	}
	setStmt := tx.Rebind(`UPDATE cards SET is_main = TRUE, updated_at = ?
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid
		  AND status = 'active' AND verified_at IS NOT NULL`)
	if err := execContextRequireRowsAffected(ctx, tx, setStmt, now, tenantID, userID, cardID); err != nil {
		return nonEnumeratingCardError(err)
	}
	return tx.Commit()
}

func (s *Store) CreateCardEnrollmentIntent(ctx context.Context, tenantID string, userID int64, now time.Time, ttl time.Duration) (CardEnrollmentIntent, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	if userID <= 0 {
		return CardEnrollmentIntent{}, ErrInvalidUserID
	}
	if now.IsZero() || ttl <= 0 {
		return CardEnrollmentIntent{}, ErrInvalidEnrollmentIntent
	}
	db, err := s.ensureDB()
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	enrollmentUUID, err := uuid.NewRandom()
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	railUUID, err := uuid.NewRandom()
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	now = now.UTC()
	intent := CardEnrollmentIntent{
		EnrollmentID: enrollmentUUID.String(),
		RailUUID:     railUUID.String(),
		Status:       EnrollmentIntentPending,
		ExpiresAt:    now.Add(ttl),
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	expireStmt := tx.Rebind(`UPDATE card_enrollment_intents
		SET status = 'expired', updated_at = ?
		WHERE tenant_id = ? AND user_id = ?
		  AND status IN ('pending', 'processing') AND expires_at <= ?`)
	if _, err := tx.ExecContext(ctx, expireStmt, now, tenantID, userID, now); err != nil {
		return CardEnrollmentIntent{}, err
	}
	insertStmt := tx.Rebind(`INSERT INTO card_enrollment_intents(
		enrollment_id, tenant_id, user_id, rail_uuid, status, expires_at, created_at, updated_at
	) VALUES(?::uuid, ?, ?, ?::uuid, ?, ?, ?, ?)`)
	if _, err := tx.ExecContext(ctx, insertStmt,
		intent.EnrollmentID, tenantID, userID, intent.RailUUID,
		intent.Status, intent.ExpiresAt, now, now,
	); err != nil {
		if isPostgresConstraint(err, "idx_card_vault_one_open_enrollment_intent") {
			return CardEnrollmentIntent{}, ErrEnrollmentIntentOpen
		}
		return CardEnrollmentIntent{}, err
	}
	if err := tx.Commit(); err != nil {
		return CardEnrollmentIntent{}, err
	}
	return intent, nil
}

func (s *Store) BeginCardEnrollmentIntent(ctx context.Context, tenantID string, userID int64, enrollmentID string, attempt CardEnrollmentAttempt, now time.Time) (CardEnrollmentIntent, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	if userID <= 0 {
		return CardEnrollmentIntent{}, ErrInvalidUserID
	}
	enrollmentID, err = NormalizeEnrollmentID(enrollmentID)
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	if now.IsZero() {
		return CardEnrollmentIntent{}, ErrInvalidEnrollmentIntent
	}
	prepared, err := s.prepareEnrollmentAttempt(attempt)
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	intent, err := selectEnrollmentIntentForUpdate(ctx, tx, tenantID, userID, enrollmentID)
	if err != nil {
		return CardEnrollmentIntent{}, err
	}
	now = now.UTC()
	if intent.RequestClaim != "" && intent.RequestClaim != prepared.Claim {
		return CardEnrollmentIntent{}, ErrEnrollmentClaimMismatch
	}
	if intent.Status == EnrollmentIntentCompleted {
		if !intent.CompletedCardID.Valid {
			return CardEnrollmentIntent{}, ErrInvalidEnrollmentIntent
		}
		card, err := selectCompletedCardSummary(ctx, tx, tenantID, userID, intent.CompletedCardID.String)
		if err != nil {
			return CardEnrollmentIntent{}, err
		}
		intent.CompletedCard = &card
		if err := tx.Commit(); err != nil {
			return CardEnrollmentIntent{}, err
		}
		return intent, nil
	}
	if !intent.ExpiresAt.After(now) && (intent.Status == EnrollmentIntentPending || intent.Status == EnrollmentIntentProcessing) {
		stmt := tx.Rebind(`UPDATE card_enrollment_intents SET status = 'expired', updated_at = ?
			WHERE tenant_id = ? AND enrollment_id = ?::uuid`)
		if _, err := tx.ExecContext(ctx, stmt, now, tenantID, enrollmentID); err != nil {
			return CardEnrollmentIntent{}, err
		}
		if err := tx.Commit(); err != nil {
			return CardEnrollmentIntent{}, err
		}
		return CardEnrollmentIntent{}, ErrEnrollmentIntentExpired
	}
	switch intent.Status {
	case EnrollmentIntentPending:
		stmt := tx.Rebind(`UPDATE card_enrollment_intents
			SET status = 'processing', operation_kind = ?, request_claim = ?,
				request_fingerprint = ?, request_expiry = ?, request_name = ?, updated_at = ?
			WHERE tenant_id = ? AND enrollment_id = ?::uuid AND status = 'pending'`)
		if err := execContextRequireRowsAffected(ctx, tx, stmt,
			prepared.OperationKind, prepared.Claim, prepared.PANFingerprint,
			prepared.Expiry, prepared.Name, now, tenantID, enrollmentID,
		); err != nil {
			return CardEnrollmentIntent{}, err
		}
		intent.Status = EnrollmentIntentProcessing
		intent.RequestClaim = prepared.Claim
	case EnrollmentIntentProcessing:
		// A retry reuses the stored rail UUID. ClaimRailSubmission below ensures
		// that only the first caller may issue the non-mutating rail verification.
	case EnrollmentIntentExpired:
		return CardEnrollmentIntent{}, ErrEnrollmentIntentExpired
	default:
		return CardEnrollmentIntent{}, ErrEnrollmentIntentConsumed
	}
	if err := tx.Commit(); err != nil {
		return CardEnrollmentIntent{}, err
	}
	return intent, nil
}

// ClaimCardEnrollmentRailSubmission atomically grants at most one caller the
// right to submit the enrollment verification rail request. A false result
// means the stable rail UUID must be reconciled; callers must not issue it again.
func (s *Store) ClaimCardEnrollmentRailSubmission(ctx context.Context, tenantID string, userID int64, enrollmentID string, now time.Time) (bool, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return false, err
	}
	if userID <= 0 {
		return false, ErrInvalidUserID
	}
	enrollmentID, err = NormalizeEnrollmentID(enrollmentID)
	if err != nil {
		return false, err
	}
	if now.IsZero() {
		return false, ErrInvalidEnrollmentIntent
	}
	db, err := s.ensureDB()
	if err != nil {
		return false, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	intent, err := selectEnrollmentIntentForUpdate(ctx, tx, tenantID, userID, enrollmentID)
	if err != nil {
		return false, err
	}
	now = now.UTC()
	if intent.Status == EnrollmentIntentCompleted {
		return false, tx.Commit()
	}
	if !intent.ExpiresAt.After(now) {
		return false, ErrEnrollmentIntentExpired
	}
	if intent.Status != EnrollmentIntentProcessing || intent.RequestClaim == "" {
		return false, ErrEnrollmentIntentConsumed
	}
	if intent.RailSubmitted.Valid {
		return false, tx.Commit()
	}
	stmt := tx.Rebind(`UPDATE card_enrollment_intents SET rail_submitted_at = ?, updated_at = ?
		WHERE tenant_id = ? AND enrollment_id = ?::uuid AND user_id = ?
		  AND status = 'processing' AND rail_submitted_at IS NULL`)
	if err := execContextRequireRowsAffected(ctx, tx, stmt, now, now, tenantID, enrollmentID, userID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) CompleteCardEnrollmentIntent(ctx context.Context, tenantID string, userID int64, enrollmentID string, enrollment VerifiedCardEnrollment, now time.Time) (ebs_fields.CardSummary, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	if userID <= 0 {
		return ebs_fields.CardSummary{}, ErrInvalidUserID
	}
	enrollmentID, err = NormalizeEnrollmentID(enrollmentID)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	if now.IsZero() {
		return ebs_fields.CardSummary{}, ErrInvalidEnrollmentIntent
	}
	prepared, err := s.prepareVerifiedCard(enrollment)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	defer func() { _ = tx.Rollback() }()
	intent, err := selectEnrollmentIntentForUpdate(ctx, tx, tenantID, userID, enrollmentID)
	if err != nil {
		return ebs_fields.CardSummary{}, err
	}
	now = now.UTC()
	if intent.RequestClaim != prepared.RequestClaim {
		return ebs_fields.CardSummary{}, ErrEnrollmentClaimMismatch
	}
	if intent.Status == EnrollmentIntentCompleted {
		if !intent.CompletedCardID.Valid {
			return ebs_fields.CardSummary{}, ErrInvalidEnrollmentIntent
		}
		card, err := selectCompletedCardSummary(ctx, tx, tenantID, userID, intent.CompletedCardID.String)
		if err != nil {
			return ebs_fields.CardSummary{}, err
		}
		if err := tx.Commit(); err != nil {
			return ebs_fields.CardSummary{}, err
		}
		return card, nil
	}
	if !intent.ExpiresAt.After(now) {
		return ebs_fields.CardSummary{}, ErrEnrollmentIntentExpired
	}
	if intent.Status != EnrollmentIntentProcessing {
		return ebs_fields.CardSummary{}, ErrEnrollmentIntentConsumed
	}
	if !intent.RailSubmitted.Valid {
		return ebs_fields.CardSummary{}, ErrInvalidEnrollmentIntent
	}
	if err := lockCardOwner(ctx, tx, tenantID, userID); err != nil {
		return ebs_fields.CardSummary{}, err
	}
	var hasMain bool
	mainStmt := tx.Rebind(`SELECT EXISTS(
		SELECT 1 FROM cards
		WHERE tenant_id = ? AND user_id = ? AND status = 'active' AND is_main = TRUE
	)`)
	if err := tx.GetContext(ctx, &hasMain, mainStmt, tenantID, userID); err != nil {
		return ebs_fields.CardSummary{}, err
	}
	prepared.IsMain = !hasMain
	insertStmt := tx.Rebind(`INSERT INTO cards(
		tenant_id, user_id, card_id, pan_fingerprint, pan_ciphertext,
		pan_key_version, masked_pan, expiry, name, status, is_main,
		verification_method, verified_at, retired_at, created_at, updated_at
	) VALUES(?, ?, ?::uuid, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, NULL, ?, ?)`)
	if _, err := tx.ExecContext(ctx, insertStmt,
		tenantID, userID, prepared.Summary.CardID, prepared.PANFingerprint,
		prepared.PANCiphertext, cardPANKeyVersion, prepared.Summary.MaskedPAN,
		prepared.Summary.Expiry, prepared.Summary.Name, prepared.IsMain,
		prepared.VerificationMethod, now, now, now,
	); err != nil {
		if isPostgresConstraint(err, "idx_card_vault_cards_active_fingerprint") {
			return ebs_fields.CardSummary{}, ErrCardEnrollmentConflict
		}
		return ebs_fields.CardSummary{}, err
	}
	completeStmt := tx.Rebind(`UPDATE card_enrollment_intents
		SET status = 'completed', completed_card_id = ?::uuid, updated_at = ?
		WHERE tenant_id = ? AND enrollment_id = ?::uuid AND user_id = ? AND status = 'processing'`)
	if err := execContextRequireRowsAffected(ctx, tx, completeStmt,
		prepared.Summary.CardID, now, tenantID, enrollmentID, userID,
	); err != nil {
		return ebs_fields.CardSummary{}, err
	}
	if err := tx.Commit(); err != nil {
		return ebs_fields.CardSummary{}, err
	}
	prepared.Summary.IsMain = prepared.IsMain
	return prepared.Summary, nil
}

func (s *Store) FailCardEnrollmentIntent(ctx context.Context, tenantID string, userID int64, enrollmentID, failureCode string, now time.Time) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if userID <= 0 {
		return ErrInvalidUserID
	}
	enrollmentID, err = NormalizeEnrollmentID(enrollmentID)
	if err != nil {
		return err
	}
	failureCode = strings.TrimSpace(failureCode)
	if failureCode == "" || len(failureCode) > 64 {
		return ErrInvalidEnrollmentIntent
	}
	if now.IsZero() {
		return ErrInvalidEnrollmentIntent
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`UPDATE card_enrollment_intents
		SET status = 'failed', failure_code = ?, updated_at = ?
		WHERE tenant_id = ? AND enrollment_id = ?::uuid AND user_id = ?
		  AND status IN ('pending', 'processing')`)
	if err := execContextRequireRowsAffected(ctx, db, stmt,
		failureCode, now.UTC(), tenantID, enrollmentID, userID,
	); err != nil {
		return enrollmentIntentError(err)
	}
	return nil
}

type preparedVerifiedCard struct {
	Summary            ebs_fields.CardSummary
	PANFingerprint     string
	PANCiphertext      string
	VerificationMethod string
	RequestClaim       string
	IsMain             bool
}

type preparedEnrollmentAttempt struct {
	PAN            string
	Expiry         string
	Name           string
	OperationKind  string
	PANFingerprint string
	Claim          string
}

func (s *Store) prepareVerifiedCard(enrollment VerifiedCardEnrollment) (preparedVerifiedCard, error) {
	preparedAttempt, err := s.prepareEnrollmentAttempt(CardEnrollmentAttempt{
		PAN:           enrollment.PAN,
		Expiry:        enrollment.Expiry,
		Name:          enrollment.Name,
		OperationKind: CardEnrollmentOperation,
	})
	if err != nil {
		return preparedVerifiedCard{}, err
	}
	method := strings.TrimSpace(enrollment.VerificationMethod)
	if method == "" || len(method) > 64 {
		return preparedVerifiedCard{}, ErrInvalidEnrollmentIntent
	}
	cardID, err := uuid.NewRandom()
	if err != nil {
		return preparedVerifiedCard{}, err
	}
	ciphertext, err := s.crypto.Encrypt(preparedAttempt.PAN)
	if err != nil {
		return preparedVerifiedCard{}, err
	}
	return preparedVerifiedCard{
		Summary: ebs_fields.CardSummary{
			CardID:    cardID.String(),
			Name:      preparedAttempt.Name,
			MaskedPAN: "****" + preparedAttempt.PAN[len(preparedAttempt.PAN)-4:],
			Expiry:    preparedAttempt.Expiry,
			Status:    CardStatusActive,
		},
		PANFingerprint:     preparedAttempt.PANFingerprint,
		PANCiphertext:      ciphertext,
		VerificationMethod: method,
		RequestClaim:       preparedAttempt.Claim,
	}, nil
}

func (s *Store) prepareEnrollmentAttempt(attempt CardEnrollmentAttempt) (preparedEnrollmentAttempt, error) {
	if s == nil || s.crypto == nil {
		return preparedEnrollmentAttempt{}, ErrMissingDataKey
	}
	pan, err := normalizeVerifiedPAN(attempt.PAN)
	if err != nil {
		return preparedEnrollmentAttempt{}, err
	}
	expiry := strings.TrimSpace(attempt.Expiry)
	if len(expiry) != 4 || !asciiDigits(expiry) {
		return preparedEnrollmentAttempt{}, ErrInvalidCardExpiry
	}
	name := strings.TrimSpace(attempt.Name)
	if len(name) > 100 {
		return preparedEnrollmentAttempt{}, ErrMissingData
	}
	operationKind := strings.TrimSpace(attempt.OperationKind)
	if operationKind != CardEnrollmentOperation {
		return preparedEnrollmentAttempt{}, ErrInvalidEnrollmentIntent
	}
	fingerprint := "v1:" + s.crypto.Hash(pan)
	claimPayload := struct {
		Version        int    `json:"version"`
		OperationKind  string `json:"operation_kind"`
		PANFingerprint string `json:"pan_fingerprint"`
		Expiry         string `json:"expiry"`
		Name           string `json:"name"`
	}{
		Version:        1,
		OperationKind:  operationKind,
		PANFingerprint: fingerprint,
		Expiry:         expiry,
		Name:           name,
	}
	encoded, err := json.Marshal(claimPayload)
	if err != nil {
		return preparedEnrollmentAttempt{}, err
	}
	digest := sha256.Sum256(encoded)
	return preparedEnrollmentAttempt{
		PAN:            pan,
		Expiry:         expiry,
		Name:           name,
		OperationKind:  operationKind,
		PANFingerprint: fingerprint,
		Claim:          "v1:" + hex.EncodeToString(digest[:]),
	}, nil
}

func normalizeVerifiedPAN(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) < 12 || len(value) > 19 || !asciiDigits(value) {
		return "", ErrMissingPAN
	}
	return value, nil
}

func asciiDigits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func selectEnrollmentIntentForUpdate(ctx context.Context, tx *sqlx.Tx, tenantID string, userID int64, enrollmentID string) (CardEnrollmentIntent, error) {
	stmt := tx.Rebind(`SELECT enrollment_id::text AS enrollment_id,
		rail_uuid::text AS rail_uuid, status, expires_at,
		COALESCE(request_claim, '') AS request_claim,
		rail_submitted_at,
		completed_card_id::text AS completed_card_id
		FROM card_enrollment_intents
		WHERE tenant_id = ? AND user_id = ? AND enrollment_id = ?::uuid
		FOR UPDATE`)
	var intent CardEnrollmentIntent
	if err := tx.GetContext(ctx, &intent, stmt, tenantID, userID, enrollmentID); err != nil {
		return CardEnrollmentIntent{}, enrollmentIntentError(err)
	}
	return intent, nil
}

func selectCompletedCardSummary(ctx context.Context, tx *sqlx.Tx, tenantID string, userID int64, cardID string) (ebs_fields.CardSummary, error) {
	stmt := tx.Rebind(`SELECT card_id::text AS card_id,
		COALESCE(name, '') AS name,
		masked_pan,
		COALESCE(expiry, '') AS expiry,
		is_main,
		status
		FROM cards
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid
		  AND verified_at IS NOT NULL`)
	var card ebs_fields.CardSummary
	if err := tx.GetContext(ctx, &card, stmt, tenantID, userID, cardID); err != nil {
		return ebs_fields.CardSummary{}, nonEnumeratingCardError(err)
	}
	return card, nil
}

func lockCardOwner(ctx context.Context, tx *sqlx.Tx, tenantID string, userID int64) error {
	key := fmt.Sprintf("%s:%d", tenantID, userID)
	stmt := tx.Rebind(`SELECT pg_advisory_xact_lock(hashtextextended(?, 0))`)
	_, err := tx.ExecContext(ctx, stmt, key)
	return err
}

func nonEnumeratingCardError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCardNotFound
	}
	return err
}

func enrollmentIntentError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrEnrollmentIntentNotFound
	}
	return err
}

func isPostgresConstraint(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == constraint
}
