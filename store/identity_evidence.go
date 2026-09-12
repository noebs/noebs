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

	"github.com/adonese/noebs/internal/identitystate"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

var (
	ErrInvalidIdentityEvidence  = errors.New("invalid identity evidence")
	ErrIdentityConflict         = errors.New("identity draft changed; refresh its status")
	ErrIdentityIncomplete       = errors.New("document and selfie evidence are required")
	ErrIdentityReviewIncomplete = errors.New("review the submitted fields and each evidence image before deciding")
)

const IdentityConsentVersion = "identity-review-v1"
const LegacyIdentityConsentVersion = "identity-evidence-v1"
const MaxIdentityImageBytes = 2 * 1024 * 1024

// IdentityOwner always comes from the authenticated NoEBS profile, never JSON.
type IdentityOwner struct {
	TenantID string
	UserID   int64
}

type IdentityEvidenceMetadata struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

type IdentitySession struct {
	ID                uuid.UUID                  `json:"session_id"`
	DocumentType      string                     `json:"document_type"`
	Synthetic         bool                       `json:"synthetic"`
	Status            string                     `json:"status"`
	Verification      identitystate.State        `json:"verification"`
	Revision          int64                      `json:"revision"`
	Evidence          []IdentityEvidenceMetadata `json:"evidence"`
	CreatedAt         time.Time                  `json:"created_at"`
	UpdatedAt         time.Time                  `json:"updated_at"`
	Submission        json.RawMessage            `json:"submission,omitempty"`
	PreviousSessionID *uuid.UUID                 `json:"previous_session_id,omitempty"`
	Review            *IdentityReview            `json:"review,omitempty"`
	submissionHash    string
}

type CreateIdentitySessionParams struct {
	Owner             IdentityOwner
	SessionID         uuid.UUID
	DocumentType      string
	Synthetic         bool
	PreviousSessionID *uuid.UUID
}

type PutIdentityEvidenceParams struct {
	Owner     IdentityOwner
	SessionID uuid.UUID
	Revision  int64
	Kind      string
	JPEG      []byte
}

type IdentitySubmission struct {
	Revision       int64  `json:"revision"`
	ConsentVersion string `json:"consent_version"`
	FieldsReviewed bool   `json:"fields_reviewed"`
	HolderName     string `json:"holder_name"`
	DocumentNumber string `json:"document_number,omitempty"`
}

func validateIdentityOwner(owner IdentityOwner, id uuid.UUID) error {
	if err := validateProjectionUserID(owner.TenantID, owner.UserID); err != nil {
		return err
	}
	if id == uuid.Nil {
		return ErrInvalidIdentityEvidence
	}
	return nil
}

func ValidIdentityDocumentType(value string) bool {
	return value == "passport" || value == "national_id"
}

func ValidIdentityEvidenceKind(value string) bool {
	return value == "document_front" || value == "document_back" || value == "selfie"
}

func ValidateIdentitySubmission(value IdentitySubmission) error {
	if value.Revision < 1 || (value.ConsentVersion != IdentityConsentVersion && value.ConsentVersion != LegacyIdentityConsentVersion) || !value.FieldsReviewed ||
		value.HolderName == "" || value.HolderName != strings.TrimSpace(value.HolderName) || len(value.HolderName) > 256 ||
		value.DocumentNumber != strings.TrimSpace(value.DocumentNumber) || len(value.DocumentNumber) > 128 {
		return ErrInvalidIdentityEvidence
	}
	return nil
}

func (s *Store) CreateIdentitySession(ctx context.Context, p CreateIdentitySessionParams) (IdentitySession, error) {
	if err := validateIdentityOwner(p.Owner, p.SessionID); err != nil {
		return IdentitySession{}, err
	}
	if !ValidIdentityDocumentType(p.DocumentType) || (p.PreviousSessionID != nil && (*p.PreviousSessionID == uuid.Nil || *p.PreviousSessionID == p.SessionID)) {
		return IdentitySession{}, ErrInvalidIdentityEvidence
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentitySession{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return IdentitySession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	// A correction is a new evidence set. The reviewed case remains immutable,
	// and its ownership must be proven even if a client knows its UUID.
	if p.PreviousSessionID != nil {
		previous, err := identitySnapshot(ctx, tx, p.Owner, *p.PreviousSessionID, true)
		if err != nil {
			return IdentitySession{}, err
		}
		if previous.Status != "needs_information" && previous.Status != "rejected" {
			return IdentitySession{}, ErrIdentityConflict
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_sessions
		(tenant_id,user_id,id,document_type,synthetic,previous_session_id,status,revision,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,'draft',1,clock_timestamp(),clock_timestamp())
		ON CONFLICT(tenant_id,user_id,id) DO NOTHING`, p.Owner.TenantID, p.Owner.UserID, p.SessionID, p.DocumentType, p.Synthetic, p.PreviousSessionID)
	if err != nil {
		return IdentitySession{}, err
	}
	result, err := identitySnapshot(ctx, tx, p.Owner, p.SessionID, true)
	if err == nil && (result.DocumentType != p.DocumentType || !sameIdentityPrevious(result.PreviousSessionID, p.PreviousSessionID)) {
		err = ErrIdentityConflict
	}
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

func (s *Store) GetIdentitySession(ctx context.Context, owner IdentityOwner, id uuid.UUID) (IdentitySession, error) {
	if err := validateIdentityOwner(owner, id); err != nil {
		return IdentitySession{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentitySession{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return IdentitySession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := identitySnapshot(ctx, tx, owner, id, false)
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

// Locking the session serializes uploads, submission and discard. A stale
// capture cannot replace evidence selected by a newer draft revision.
func (s *Store) PutIdentityEvidence(ctx context.Context, p PutIdentityEvidenceParams) (IdentitySession, error) {
	if err := validateIdentityOwner(p.Owner, p.SessionID); err != nil {
		return IdentitySession{}, err
	}
	if !ValidIdentityEvidenceKind(p.Kind) || p.Revision < 1 || len(p.JPEG) == 0 || len(p.JPEG) > MaxIdentityImageBytes {
		return IdentitySession{}, ErrInvalidIdentityEvidence
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentitySession{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return IdentitySession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := identitySnapshot(ctx, tx, p.Owner, p.SessionID, true)
	if err != nil {
		return IdentitySession{}, err
	}
	if result.Status != "draft" || (p.Kind == "document_back" && result.DocumentType != "national_id") {
		return IdentitySession{}, ErrIdentityConflict
	}
	digest := sha256.Sum256(p.JPEG)
	hash := hex.EncodeToString(digest[:])
	for _, evidence := range result.Evidence {
		if evidence.Kind == p.Kind && evidence.SHA256 == hash && (p.Revision == result.Revision || p.Revision == result.Revision-1) {
			return result, tx.Commit() // Response-loss retry, with the same bytes.
		}
	}
	if p.Revision != result.Revision {
		return IdentitySession{}, ErrIdentityConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_evidence
		(tenant_id,user_id,session_id,kind,content_type,image_bytes,sha256,created_at)
		VALUES($1,$2,$3,$4,'image/jpeg',$5,$6,clock_timestamp())
		ON CONFLICT(tenant_id,user_id,session_id,kind) DO UPDATE SET
		image_bytes=excluded.image_bytes,sha256=excluded.sha256,created_at=excluded.created_at`,
		p.Owner.TenantID, p.Owner.UserID, p.SessionID, p.Kind, p.JPEG, hash)
	if err != nil {
		return IdentitySession{}, err
	}
	if err = bumpIdentityRevision(ctx, tx, p.Owner, p.SessionID); err != nil {
		return IdentitySession{}, err
	}
	result, err = identitySnapshot(ctx, tx, p.Owner, p.SessionID, true)
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

func (s *Store) SubmitIdentitySession(ctx context.Context, owner IdentityOwner, id uuid.UUID, submission IdentitySubmission) (IdentitySession, error) {
	if err := validateIdentityOwner(owner, id); err != nil {
		return IdentitySession{}, err
	}
	if err := ValidateIdentitySubmission(submission); err != nil {
		return IdentitySession{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentitySession{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return IdentitySession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := identitySnapshot(ctx, tx, owner, id, true)
	if err != nil {
		return IdentitySession{}, err
	}
	payload, err := json.Marshal(submission)
	if err != nil {
		return IdentitySession{}, err
	}
	digest := sha256.Sum256(payload)
	hash := hex.EncodeToString(digest[:])
	if identitySubmittedStatus(result.Status) && result.submissionHash == hash {
		return result, tx.Commit()
	}
	if !result.Synthetic && submission.ConsentVersion != IdentityConsentVersion {
		return IdentitySession{}, ErrInvalidIdentityEvidence
	}
	if result.Status != "draft" || submission.Revision != result.Revision {
		return IdentitySession{}, ErrIdentityConflict
	}
	if err := identityTransition(result.Status, "submitted"); err != nil {
		return IdentitySession{}, err
	}
	kinds := map[string]bool{}
	for _, evidence := range result.Evidence {
		kinds[evidence.Kind] = true
	}
	if !kinds["document_front"] || !kinds["selfie"] {
		return IdentitySession{}, ErrIdentityIncomplete
	}
	_, err = tx.ExecContext(ctx, `UPDATE identity_sessions SET status='submitted',submission=$4,
		submission_sha256=$5,revision=revision+1,updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, owner.TenantID, owner.UserID, id, string(payload), hash)
	if err != nil {
		return IdentitySession{}, err
	}
	result, err = identitySnapshot(ctx, tx, owner, id, true)
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

func (s *Store) DiscardIdentitySession(ctx context.Context, owner IdentityOwner, id uuid.UUID, revision int64) (IdentitySession, error) {
	if err := validateIdentityOwner(owner, id); err != nil {
		return IdentitySession{}, err
	}
	if revision < 1 {
		return IdentitySession{}, ErrInvalidIdentityEvidence
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentitySession{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return IdentitySession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := identitySnapshot(ctx, tx, owner, id, true)
	if err != nil {
		return IdentitySession{}, err
	}
	if result.Status == "discarded" && revision == result.Revision-1 {
		return result, tx.Commit()
	}
	if result.Status != "draft" || revision != result.Revision {
		return IdentitySession{}, ErrIdentityConflict
	}
	if err := identityTransition(result.Status, "discarded"); err != nil {
		return IdentitySession{}, err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM identity_evidence WHERE tenant_id=$1 AND user_id=$2 AND session_id=$3`, owner.TenantID, owner.UserID, id)
	if err != nil {
		return IdentitySession{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE identity_sessions SET status='discarded',revision=revision+1,
		updated_at=clock_timestamp() WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, owner.TenantID, owner.UserID, id)
	if err != nil {
		return IdentitySession{}, err
	}
	result, err = identitySnapshot(ctx, tx, owner, id, true)
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

func bumpIdentityRevision(ctx context.Context, tx *sqlx.Tx, owner IdentityOwner, id uuid.UUID) error {
	_, err := tx.ExecContext(ctx, `UPDATE identity_sessions SET revision=revision+1,updated_at=clock_timestamp()
		WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, owner.TenantID, owner.UserID, id)
	return err
}

func identitySnapshot(ctx context.Context, tx *sqlx.Tx, owner IdentityOwner, id uuid.UUID, write bool) (IdentitySession, error) {
	lock := " FOR SHARE"
	if write {
		lock = " FOR UPDATE"
	}
	result := IdentitySession{Evidence: []IdentityEvidenceMetadata{}}
	var submission sql.NullString
	var previous uuid.NullUUID
	var review sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT id,document_type,synthetic,status,revision,created_at,updated_at,
		submission,COALESCE(submission_sha256,''),previous_session_id,review FROM identity_sessions
		WHERE tenant_id=$1 AND user_id=$2 AND id=$3`+lock, owner.TenantID, owner.UserID, id).Scan(
		&result.ID, &result.DocumentType, &result.Synthetic, &result.Status, &result.Revision,
		&result.CreatedAt, &result.UpdatedAt, &submission, &result.submissionHash, &previous, &review)
	if err != nil {
		return IdentitySession{}, err
	}
	result.Verification, err = identitystate.FromSession(result.Status)
	if err != nil {
		return IdentitySession{}, err
	}
	if submission.Valid {
		result.Submission = json.RawMessage(submission.String)
	}
	if previous.Valid {
		result.PreviousSessionID = &previous.UUID
	}
	if review.Valid {
		if err := json.Unmarshal([]byte(review.String), &result.Review); err != nil {
			return IdentitySession{}, err
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT kind,sha256,octet_length(image_bytes) FROM identity_evidence
		WHERE tenant_id=$1 AND user_id=$2 AND session_id=$3 ORDER BY kind`, owner.TenantID, owner.UserID, id)
	if err != nil {
		return IdentitySession{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var metadata IdentityEvidenceMetadata
		if err := rows.Scan(&metadata.Kind, &metadata.SHA256, &metadata.Bytes); err != nil {
			return IdentitySession{}, err
		}
		result.Evidence = append(result.Evidence, metadata)
	}
	return result, rows.Err()
}

func sameIdentityPrevious(a, b *uuid.UUID) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func identitySubmittedStatus(status string) bool {
	return status == "submitted" || status == "approved" || status == "needs_information" || status == "rejected"
}
