package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

const FundedPurposeBalanceInquiry = "balance_inquiry"

// FundedOperationBodyClaim returns the canonical semantic claim for the one
// funded operation currently enabled by the opaque-card contract.
func FundedOperationBodyClaim(cardID, purpose string) (string, error) {
	cardID, err := NormalizeCardID(cardID)
	if err != nil {
		return "", err
	}
	if purpose != FundedPurposeBalanceInquiry {
		return "", ErrInvalidFundedPurpose
	}
	canonical, err := json.Marshal(struct {
		CardID    string `json:"card_id"`
		Operation string `json:"operation"`
		Version   int    `json:"version"`
	}{
		CardID:    cardID,
		Operation: purpose,
		Version:   1,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)
	return "v1:" + hex.EncodeToString(digest[:]), nil
}

// FundedOperationClaim is the complete durable identity of one caller-chosen
// rail operation. BodyClaim is computed from the operation's safe semantic
// fields and never includes PAN, expiry, or an IPIN block.
type FundedOperationClaim struct {
	UserID           int64
	CardID           string
	RailUUID         string
	Purpose          string
	BodyClaim        string
	RailTranDateTime string
}

// FundedOperationGrant contains request-scoped rail material only for the one
// caller that durably creates the operation claim. Exact retries receive the
// persisted rail time with Granted false and no card secrets.
type FundedOperationGrant struct {
	Granted          bool
	RailTranDateTime string
	PAN              string
	Expiry           string
}

type persistedFundedOperationClaim struct {
	UserID           int64  `db:"user_id"`
	CardID           string `db:"card_id"`
	Purpose          string `db:"purpose"`
	BodyClaim        string `db:"body_claim"`
	RailTranDateTime string `db:"rail_tran_date_time"`
}

// ClaimFundedCardOperation atomically binds an owned active card to one rail
// UUID and grants its request-scoped secrets to at most one caller.
func (s *Store) ClaimFundedCardOperation(ctx context.Context, tenantID string, claim FundedOperationClaim, now time.Time) (FundedOperationGrant, error) {
	prepared, err := prepareFundedOperationClaim(tenantID, claim, now)
	if err != nil {
		return FundedOperationGrant{}, err
	}
	if s == nil || s.crypto == nil {
		return FundedOperationGrant{}, ErrMissingDataKey
	}
	db, err := s.ensureDB()
	if err != nil {
		return FundedOperationGrant{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return FundedOperationGrant{}, err
	}
	defer func() { _ = tx.Rollback() }()

	existing, found, err := selectFundedOperationClaim(ctx, tx, prepared.TenantID, prepared.RailUUID)
	if err != nil {
		return FundedOperationGrant{}, err
	}
	if found {
		if err := matchFundedOperationReplay(ctx, tx, prepared, existing); err != nil {
			return FundedOperationGrant{}, err
		}
		if err := tx.Commit(); err != nil {
			return FundedOperationGrant{}, err
		}
		return FundedOperationGrant{RailTranDateTime: existing.RailTranDateTime}, nil
	}

	ciphertext, expiry, err := selectActiveOwnedCardSecrets(ctx, tx, prepared.TenantID, prepared.UserID, prepared.CardID)
	if err != nil {
		return FundedOperationGrant{}, err
	}
	inserted, err := insertFundedOperationClaim(ctx, tx, prepared, now.UTC())
	if err != nil {
		return FundedOperationGrant{}, err
	}
	if !inserted {
		existing, found, err = selectFundedOperationClaim(ctx, tx, prepared.TenantID, prepared.RailUUID)
		if err != nil {
			return FundedOperationGrant{}, err
		}
		if !found {
			return FundedOperationGrant{}, ErrFundedClaimMismatch
		}
		if err := matchFundedOperationReplay(ctx, tx, prepared, existing); err != nil {
			return FundedOperationGrant{}, err
		}
		if err := tx.Commit(); err != nil {
			return FundedOperationGrant{}, err
		}
		return FundedOperationGrant{RailTranDateTime: existing.RailTranDateTime}, nil
	}

	pan, err := s.crypto.Decrypt(ciphertext)
	if err != nil {
		return FundedOperationGrant{}, err
	}
	pan, err = normalizeVerifiedPAN(pan)
	if err != nil {
		return FundedOperationGrant{}, err
	}
	if len(expiry) != 4 || !asciiDigits(expiry) {
		return FundedOperationGrant{}, ErrInvalidCardExpiry
	}
	if err := tx.Commit(); err != nil {
		return FundedOperationGrant{}, err
	}
	return FundedOperationGrant{
		Granted:          true,
		RailTranDateTime: prepared.RailTranDateTime,
		PAN:              pan,
		Expiry:           expiry,
	}, nil
}

type preparedFundedOperationClaim struct {
	TenantID string
	FundedOperationClaim
}

func prepareFundedOperationClaim(tenantID string, claim FundedOperationClaim, now time.Time) (preparedFundedOperationClaim, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return preparedFundedOperationClaim{}, err
	}
	if claim.UserID <= 0 {
		return preparedFundedOperationClaim{}, ErrInvalidUserID
	}
	claim.CardID, err = NormalizeCardID(claim.CardID)
	if err != nil {
		return preparedFundedOperationClaim{}, err
	}
	claim.RailUUID, err = NormalizeRailUUID(claim.RailUUID)
	if err != nil {
		return preparedFundedOperationClaim{}, err
	}
	if claim.Purpose != FundedPurposeBalanceInquiry {
		return preparedFundedOperationClaim{}, ErrInvalidFundedPurpose
	}
	if !validFundedBodyClaim(claim.BodyClaim) {
		return preparedFundedOperationClaim{}, ErrInvalidFundedBodyClaim
	}
	expectedClaim, err := FundedOperationBodyClaim(claim.CardID, claim.Purpose)
	if err != nil {
		return preparedFundedOperationClaim{}, err
	}
	if subtle.ConstantTimeCompare([]byte(claim.BodyClaim), []byte(expectedClaim)) != 1 {
		return preparedFundedOperationClaim{}, ErrFundedClaimMismatch
	}
	if len(claim.RailTranDateTime) != 12 || !asciiDigits(claim.RailTranDateTime) {
		return preparedFundedOperationClaim{}, ErrInvalidRailTranDateTime
	}
	if now.IsZero() {
		return preparedFundedOperationClaim{}, ErrInvalidRailTranDateTime
	}
	return preparedFundedOperationClaim{TenantID: tenantID, FundedOperationClaim: claim}, nil
}

func validFundedBodyClaim(value string) bool {
	if len(value) != 67 || !strings.HasPrefix(value, "v1:") {
		return false
	}
	digest, err := hex.DecodeString(value[3:])
	return err == nil && len(digest) == 32 && hex.EncodeToString(digest) == value[3:]
}

func selectFundedOperationClaim(ctx context.Context, tx *sqlx.Tx, tenantID, railUUID string) (persistedFundedOperationClaim, bool, error) {
	stmt := tx.Rebind(`SELECT user_id, card_id::text AS card_id, purpose, body_claim, rail_tran_date_time
		FROM card_funded_operation_claims
		WHERE tenant_id = ? AND rail_uuid = ?::uuid
		FOR UPDATE`)
	var claim persistedFundedOperationClaim
	if err := tx.GetContext(ctx, &claim, stmt, tenantID, railUUID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return persistedFundedOperationClaim{}, false, nil
		}
		return persistedFundedOperationClaim{}, false, err
	}
	return claim, true, nil
}

func matchFundedOperationReplay(ctx context.Context, tx *sqlx.Tx, requested preparedFundedOperationClaim, existing persistedFundedOperationClaim) error {
	if existing.UserID != requested.UserID {
		return ErrCardNotFound
	}
	if existing.CardID != requested.CardID {
		owned, err := activeOwnedCardExists(ctx, tx, requested.TenantID, requested.UserID, requested.CardID)
		if err != nil {
			return err
		}
		if !owned {
			return ErrCardNotFound
		}
		return ErrFundedClaimMismatch
	}
	if existing.Purpose != requested.Purpose || existing.BodyClaim != requested.BodyClaim {
		return ErrFundedClaimMismatch
	}
	return nil
}

func activeOwnedCardExists(ctx context.Context, tx *sqlx.Tx, tenantID string, userID int64, cardID string) (bool, error) {
	stmt := tx.Rebind(`SELECT EXISTS(
		SELECT 1 FROM cards
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid
		  AND status = 'active' AND verified_at IS NOT NULL
	)`)
	var exists bool
	if err := tx.GetContext(ctx, &exists, stmt, tenantID, userID, cardID); err != nil {
		return false, err
	}
	return exists, nil
}

func selectActiveOwnedCardSecrets(ctx context.Context, tx *sqlx.Tx, tenantID string, userID int64, cardID string) (string, string, error) {
	stmt := tx.Rebind(`SELECT pan_ciphertext, expiry FROM cards
		WHERE tenant_id = ? AND user_id = ? AND card_id = ?::uuid
		  AND status = 'active' AND verified_at IS NOT NULL
		FOR SHARE`)
	var ciphertext, expiry string
	if err := tx.QueryRowContext(ctx, stmt, tenantID, userID, cardID).Scan(&ciphertext, &expiry); err != nil {
		return "", "", nonEnumeratingCardError(err)
	}
	return ciphertext, expiry, nil
}

func insertFundedOperationClaim(ctx context.Context, tx *sqlx.Tx, claim preparedFundedOperationClaim, claimedAt time.Time) (bool, error) {
	stmt := tx.Rebind(`INSERT INTO card_funded_operation_claims(
		tenant_id, rail_uuid, user_id, card_id, purpose, body_claim, rail_tran_date_time, claimed_at
	) VALUES(?, ?::uuid, ?, ?::uuid, ?, ?, ?, ?)
	ON CONFLICT (tenant_id, rail_uuid) DO NOTHING
	RETURNING TRUE`)
	var inserted bool
	if err := tx.QueryRowContext(ctx, stmt,
		claim.TenantID, claim.RailUUID, claim.UserID, claim.CardID,
		claim.Purpose, claim.BodyClaim, claim.RailTranDateTime, claimedAt,
	).Scan(&inserted); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return inserted, nil
}
