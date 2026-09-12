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

// IdentityReview is the customer-facing manual decision. It does not assert
// automated document authenticity, biometric liveness, or a wallet KYC tier.
type IdentityReview struct {
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason"`
	Method     string    `json:"method"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

type IdentityReviewer struct {
	TenantID string
	Actor    string
}

type IdentityReviewEvent struct {
	ID            uuid.UUID       `json:"event_id"`
	Actor         string          `json:"actor"`
	DatabaseActor string          `json:"database_actor"`
	Action        string          `json:"action"`
	Revision      int64           `json:"revision"`
	Details       json.RawMessage `json:"details"`
	CreatedAt     time.Time       `json:"created_at"`
}

type IdentityReviewCase struct {
	Owner               IdentityOwner         `json:"owner"`
	Session             IdentitySession       `json:"session"`
	AccountVerification IdentityVerification  `json:"account_verification"`
	Events              []IdentityReviewEvent `json:"events"`
}

type IdentityReviewQueueItem struct {
	SessionID    uuid.UUID `json:"session_id"`
	UserID       int64     `json:"user_id"`
	DocumentType string    `json:"document_type"`
	Revision     int64     `json:"revision"`
	SubmittedAt  time.Time `json:"submitted_at"`
}

type IdentityReviewDecisionParams struct {
	Reviewer         IdentityReviewer
	Owner            IdentityOwner
	SessionID        uuid.UUID
	OperationID      uuid.UUID
	Revision         int64
	Decision         string
	Reason           string
	PolicyReference  string
	EvidenceReviewed bool
}

func validateIdentityReviewer(reviewer IdentityReviewer) error {
	if _, err := ValidateTenantID(reviewer.TenantID); err != nil {
		return err
	}
	if reviewer.Actor == "" || reviewer.Actor != strings.TrimSpace(reviewer.Actor) || len(reviewer.Actor) > 256 || strings.ContainsAny(reviewer.Actor, "\r\n\x00") {
		return ErrInvalidIdentityEvidence
	}
	return nil
}

func validateIdentityReviewOwner(reviewer IdentityReviewer, owner IdentityOwner, id uuid.UUID) error {
	if err := validateIdentityReviewer(reviewer); err != nil {
		return err
	}
	if err := validateIdentityOwner(owner, id); err != nil {
		return err
	}
	if reviewer.TenantID != owner.TenantID {
		return sql.ErrNoRows
	}
	return nil
}

func ValidateIdentityReviewDecision(p IdentityReviewDecisionParams) error {
	if err := validateIdentityReviewOwner(p.Reviewer, p.Owner, p.SessionID); err != nil {
		return err
	}
	if p.OperationID == uuid.Nil || p.Revision < 1 || !p.EvidenceReviewed ||
		(p.Decision != "approved" && p.Decision != "needs_information" && p.Decision != "rejected") ||
		p.Reason == "" || p.Reason != strings.TrimSpace(p.Reason) || len(p.Reason) > 2000 ||
		p.PolicyReference == "" || p.PolicyReference != strings.TrimSpace(p.PolicyReference) || len(p.PolicyReference) > 256 {
		return ErrInvalidIdentityEvidence
	}
	return nil
}

func (s *Store) LatestIdentitySession(ctx context.Context, owner IdentityOwner) (IdentitySession, error) {
	if err := validateProjectionUserID(owner.TenantID, owner.UserID); err != nil {
		return IdentitySession{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentitySession{}, err
	}
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: false})
	if err != nil {
		return IdentitySession{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var id uuid.UUID
	err = tx.QueryRowContext(ctx, `SELECT id FROM identity_sessions WHERE tenant_id=$1 AND user_id=$2 AND NOT synthetic AND status <> 'discarded' ORDER BY created_at DESC,id DESC LIMIT 1`, owner.TenantID, owner.UserID).Scan(&id)
	if err != nil {
		return IdentitySession{}, err
	}
	result, err := identitySnapshot(ctx, tx, owner, id, false)
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

func (s *Store) WithdrawIdentitySession(ctx context.Context, owner IdentityOwner, id uuid.UUID, revision int64) (IdentitySession, error) {
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
	if result.Status == "withdrawn" && (revision == result.Revision || revision == result.Revision-1) {
		return result, tx.Commit()
	}
	if result.Status == "discarded" || result.Status == "withdrawn" || result.Revision != revision {
		return IdentitySession{}, ErrIdentityConflict
	}
	if err := identityTransition(result.Status, "withdrawn"); err != nil {
		return IdentitySession{}, err
	}
	// Keep metadata hashes in the event; delete live image bytes and submitted
	// claims atomically. This cannot promise erasure from database backups.
	details := map[string]any{"session_id": id, "previous_status": result.Status, "evidence": result.Evidence}
	if err := insertIdentityReviewEvent(ctx, tx, uuid.New(), owner, "customer", "withdrawal", revision, details); err != nil {
		return IdentitySession{}, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM identity_evidence WHERE tenant_id=$1 AND user_id=$2 AND session_id=$3`, owner.TenantID, owner.UserID, id); err != nil {
		return IdentitySession{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE identity_sessions SET status='withdrawn',submission=NULL,submission_sha256=NULL,review=NULL,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, owner.TenantID, owner.UserID, id); err != nil {
		return IdentitySession{}, err
	}
	result, err = identitySnapshot(ctx, tx, owner, id, true)
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

// ListIdentityReviewQueue intentionally contains no submitted claims or image
// bytes. The trusted operator must request and audit a particular case separately.
func (s *Store) ListIdentityReviewQueue(ctx context.Context, reviewer IdentityReviewer, limit, offset int) ([]IdentityReviewQueueItem, error) {
	if err := validateIdentityReviewer(reviewer); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 100000 {
		return nil, ErrInvalidIdentityEvidence
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `SELECT id,user_id,document_type,revision,updated_at FROM identity_sessions WHERE tenant_id=$1 AND status='submitted' AND NOT synthetic ORDER BY updated_at,id LIMIT $2 OFFSET $3`, reviewer.TenantID, limit, offset)
	if err != nil {
		return nil, err
	}
	items := []IdentityReviewQueueItem{}
	for rows.Next() {
		var item IdentityReviewQueueItem
		if err = rows.Scan(&item.SessionID, &item.UserID, &item.DocumentType, &item.Revision, &item.SubmittedAt); err != nil {
			_ = rows.Close()
			return nil, err
		}
		items = append(items, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	details, err := json.Marshal(map[string]any{"limit": limit, "offset": offset, "count": len(items)})
	if err != nil {
		return nil, err
	}
	hash := identityRequestHash(details)
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_review_events(id,tenant_id,actor,action,request_sha256,details) VALUES($1,$2,$3,'queue_read',$4,$5)`, uuid.New(), reviewer.TenantID, reviewer.Actor, hash, string(details))
	if err != nil {
		return nil, err
	}
	return items, tx.Commit()
}

func (s *Store) ReadIdentityReviewCase(ctx context.Context, reviewer IdentityReviewer, owner IdentityOwner, id uuid.UUID) (IdentityReviewCase, error) {
	if err := validateIdentityReviewOwner(reviewer, owner, id); err != nil {
		return IdentityReviewCase{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentityReviewCase{}, err
	}
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return IdentityReviewCase{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := identitySnapshot(ctx, tx, owner, id, false)
	if err != nil {
		return IdentityReviewCase{}, err
	}
	if err = insertIdentityReviewEvent(ctx, tx, uuid.New(), owner, reviewer.Actor, "case_read", result.Revision, map[string]any{"session_id": id, "status": result.Status}); err != nil {
		return IdentityReviewCase{}, err
	}
	events, err := identityReviewEvents(ctx, tx, owner, id)
	if err != nil {
		return IdentityReviewCase{}, err
	}
	account, err := readIdentityVerification(ctx, tx, owner)
	if err != nil {
		return IdentityReviewCase{}, err
	}
	return IdentityReviewCase{Owner: owner, Session: result, AccountVerification: account, Events: events}, tx.Commit()
}

// Evidence bytes are available only through the trusted operator command. Every
// successful read commits its access receipt before returning the bytes.
func (s *Store) ReadIdentityReviewEvidence(ctx context.Context, reviewer IdentityReviewer, owner IdentityOwner, id uuid.UUID, revision int64, kind string) ([]byte, error) {
	if err := validateIdentityReviewOwner(reviewer, owner, id); err != nil {
		return nil, err
	}
	if revision < 1 || !ValidIdentityEvidenceKind(kind) {
		return nil, ErrInvalidIdentityEvidence
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := identitySnapshot(ctx, tx, owner, id, false)
	if err != nil {
		return nil, err
	}
	if result.Revision != revision || !identitySubmittedStatus(result.Status) {
		return nil, ErrIdentityConflict
	}
	var payload []byte
	var hash string
	err = tx.QueryRowContext(ctx, `SELECT image_bytes,sha256 FROM identity_evidence WHERE tenant_id=$1 AND user_id=$2 AND session_id=$3 AND kind=$4`, owner.TenantID, owner.UserID, id, kind).Scan(&payload, &hash)
	if err != nil {
		return nil, err
	}
	if err = insertIdentityReviewEvent(ctx, tx, uuid.New(), owner, reviewer.Actor, "evidence_read", revision, map[string]any{"session_id": id, "kind": kind, "sha256": hash}); err != nil {
		return nil, err
	}
	return payload, tx.Commit()
}

func (s *Store) DecideIdentityReview(ctx context.Context, p IdentityReviewDecisionParams) (IdentitySession, error) {
	if err := ValidateIdentityReviewDecision(p); err != nil {
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
	result, err := identitySnapshot(ctx, tx, p.Owner, p.SessionID, true)
	if err != nil {
		return IdentitySession{}, err
	}
	details := map[string]any{"session_id": p.SessionID, "decision": p.Decision, "reason": p.Reason, "policy_reference": p.PolicyReference, "evidence_reviewed": p.EvidenceReviewed, "method": "manual", "evidence": result.Evidence}
	payload, err := json.Marshal(details)
	if err != nil {
		return IdentitySession{}, err
	}
	// A stable operation key identifies exact human terms and the reviewed case
	// revision. Changed retries cannot replace the first recorded decision.
	digest := identityReviewRequestHash(p.Owner, p.Reviewer.Actor, "decision", p.Revision, payload)
	var oldDigest, oldActor string
	err = tx.QueryRowContext(ctx, `SELECT request_sha256,actor FROM identity_review_events WHERE id=$1 AND tenant_id=$2 AND user_id=$3 AND session_id=$4 AND action='decision'`, p.OperationID, p.Owner.TenantID, p.Owner.UserID, p.SessionID).Scan(&oldDigest, &oldActor)
	if err == nil {
		if digest != oldDigest || oldActor != p.Reviewer.Actor {
			return IdentitySession{}, ErrIdentityConflict
		}
		return result, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return IdentitySession{}, err
	}
	if result.Status != "submitted" || result.Revision != p.Revision {
		return IdentitySession{}, ErrIdentityConflict
	}
	if err := identityTransition(result.Status, p.Decision); err != nil {
		return IdentitySession{}, err
	}
	if result.Synthetic && p.Decision == "approved" {
		return IdentitySession{}, ErrInvalidIdentityEvidence
	}
	if err = requireIdentityReviewAccess(ctx, tx, p, result); err != nil {
		return IdentitySession{}, err
	}
	review := IdentityReview{Decision: p.Decision, Reason: p.Reason, Method: "manual", ReviewedAt: time.Now().UTC()}
	reviewJSON, err := json.Marshal(review)
	if err != nil {
		return IdentitySession{}, err
	}
	if err = insertIdentityReviewEvent(ctx, tx, p.OperationID, p.Owner, p.Reviewer.Actor, "decision", p.Revision, details); err != nil {
		return IdentitySession{}, err
	}
	_, err = tx.ExecContext(ctx, `UPDATE identity_sessions SET status=$4,review=$5,revision=revision+1,updated_at=clock_timestamp() WHERE tenant_id=$1 AND user_id=$2 AND id=$3`, p.Owner.TenantID, p.Owner.UserID, p.SessionID, p.Decision, string(reviewJSON))
	if err != nil {
		return IdentitySession{}, err
	}
	result, err = identitySnapshot(ctx, tx, p.Owner, p.SessionID, true)
	if err != nil {
		return IdentitySession{}, err
	}
	return result, tx.Commit()
}

func requireIdentityReviewAccess(ctx context.Context, tx *sqlx.Tx, p IdentityReviewDecisionParams, session IdentitySession) error {
	var caseRead bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_review_events WHERE tenant_id=$1 AND user_id=$2 AND session_id=$3 AND actor=$4 AND revision=$5 AND action='case_read')`, p.Owner.TenantID, p.Owner.UserID, p.SessionID, p.Reviewer.Actor, p.Revision).Scan(&caseRead); err != nil {
		return err
	}
	if !caseRead {
		return ErrIdentityReviewIncomplete
	}
	for _, evidence := range session.Evidence {
		var accessed bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM identity_review_events WHERE tenant_id=$1 AND user_id=$2 AND session_id=$3 AND actor=$4 AND revision=$5 AND action='evidence_read' AND details->>'kind'=$6 AND details->>'sha256'=$7)`, p.Owner.TenantID, p.Owner.UserID, p.SessionID, p.Reviewer.Actor, p.Revision, evidence.Kind, evidence.SHA256).Scan(&accessed); err != nil {
			return err
		}
		if !accessed {
			return ErrIdentityReviewIncomplete
		}
	}
	return nil
}

func identityRequestHash(payload []byte) string {
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:])
}
func identityReviewRequestHash(owner IdentityOwner, actor, action string, revision int64, payload []byte) string {
	if action == "decision" {
		// Evidence is an audit attachment, not a caller-supplied decision term.
		// Its later deletion must not change the identity of an exact retry.
		var terms map[string]json.RawMessage
		if json.Unmarshal(payload, &terms) == nil {
			delete(terms, "evidence")
			payload, _ = json.Marshal(terms)
		}
	}

	request, _ := json.Marshal(struct {
		Owner         IdentityOwner
		Actor, Action string
		Revision      int64
		Details       json.RawMessage
	}{owner, actor, action, revision, payload})
	return identityRequestHash(request)
}

func insertIdentityReviewEvent(ctx context.Context, tx *sqlx.Tx, id uuid.UUID, owner IdentityOwner, actor, action string, revision int64, details map[string]any) error {
	payload, err := json.Marshal(details)
	if err != nil {
		return err
	}
	session, ok := details["session_id"]
	if !ok {
		// Withdrawal includes the session explicitly at its caller, like every other
		// event. Never infer the owner or choose a case from unrelated metadata.
		return ErrInvalidIdentityEvidence
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO identity_review_events(id,tenant_id,user_id,session_id,actor,action,revision,request_sha256,details) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, owner.TenantID, owner.UserID, session, actor, action, revision, identityReviewRequestHash(owner, actor, action, revision, payload), string(payload))
	return err
}

func identityReviewEvents(ctx context.Context, tx *sqlx.Tx, owner IdentityOwner, id uuid.UUID) ([]IdentityReviewEvent, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,actor,database_actor,action,revision,details,created_at FROM identity_review_events WHERE tenant_id=$1 AND user_id=$2 AND session_id=$3 ORDER BY created_at,id`, owner.TenantID, owner.UserID, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []IdentityReviewEvent{}
	for rows.Next() {
		var event IdentityReviewEvent
		if err = rows.Scan(&event.ID, &event.Actor, &event.DatabaseActor, &event.Action, &event.Revision, &event.Details, &event.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
