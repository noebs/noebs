package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

type DepositIntent struct {
	ID              int64          `db:"id"`
	TenantID        string         `db:"tenant_id"`
	IntentReference string         `db:"intent_reference"`
	ProviderCode    string         `db:"provider_code"`
	WalletID        uuid.UUID      `db:"wallet_id"`
	OwnerType       string         `db:"owner_type"`
	OwnerID         string         `db:"owner_id"`
	Amount          int64          `db:"amount"`
	Currency        string         `db:"currency"`
	IdempotencyKey  string         `db:"idempotency_key"`
	WorkflowID      string         `db:"workflow_id"`
	RunID           sql.NullString `db:"run_id"`
	Metadata        RawJSON        `db:"metadata"`
	Region          string         `db:"region"`
	RawRequest      RawJSON        `db:"raw_request"`
	CreatedAt       time.Time      `db:"created_at"`
}

func (s *Store) GetDepositIntentByReference(ctx context.Context, tenantID, reference string) (*DepositIntent, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if err := validateBoundedIdentifier(reference, 128, ErrMissingClientReference, ErrInvalidDepositIntent); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var intent DepositIntent
	stmt := db.Rebind(`SELECT * FROM deposit_intents WHERE tenant_id = ? AND intent_reference = ?`)
	if err := db.GetContext(ctx, &intent, stmt, tenantID, reference); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDepositIntentNotFound
		}
		return nil, err
	}
	return &intent, nil
}

func (s *Store) GetDepositIntentByIdempotency(ctx context.Context, tenantID, providerCode, idempotencyKey string) (*DepositIntent, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if providerCode == "" {
		return nil, ErrMissingProviderCode
	}
	if err := validateBoundedIdentifier(idempotencyKey, 256, ErrMissingIdempotencyKey, ErrInvalidIdempotencyKey); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	var intent DepositIntent
	stmt := db.Rebind(`SELECT * FROM deposit_intents
		WHERE tenant_id = ? AND provider_code = ? AND idempotency_key = ?`)
	if err := db.GetContext(ctx, &intent, stmt, tenantID, providerCode, idempotencyKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDepositIntentNotFound
		}
		return nil, err
	}
	return &intent, nil
}

func (s *Store) ReserveDepositIntent(ctx context.Context, requested DepositIntent) (*DepositIntent, error) {
	tenantID, err := validateDepositIntent(requested)
	if err != nil {
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
	defer func() { _ = tx.Rollback() }()

	stmt := tx.Rebind(`INSERT INTO deposit_intents(
			tenant_id, intent_reference, provider_code, wallet_id, owner_type, owner_id,
			amount, currency, idempotency_key, workflow_id, metadata, region, raw_request
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
		RETURNING *`)
	var stored DepositIntent
	err = tx.GetContext(ctx, &stored, stmt,
		tenantID,
		requested.IntentReference,
		requested.ProviderCode,
		requested.WalletID,
		requested.OwnerType,
		requested.OwnerID,
		requested.Amount,
		requested.Currency,
		requested.IdempotencyKey,
		requested.WorkflowID,
		requested.Metadata,
		requested.Region,
		requested.RawRequest,
	)
	if errors.Is(err, sql.ErrNoRows) {
		getIntent := tx.Rebind(`SELECT * FROM deposit_intents
			WHERE tenant_id = ? AND provider_code = ? AND idempotency_key = ?`)
		if err := tx.GetContext(ctx, &stored, getIntent,
			tenantID,
			requested.ProviderCode,
			requested.IdempotencyKey,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrDuplicateDepositIntent
			}
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if err := ValidateDepositIntentReplay(&stored, requested); err != nil {
		return nil, err
	}

	insertPSP := tx.Rebind(`INSERT INTO psp_transactions(
			tenant_id, psp_provider, idempotency_key, client_reference, direction,
			amount, currency, status, workflow_id, raw_request, deposit_intent_id
		) VALUES(?, ?, ?, ?, 'inbound', ?, ?, 'initiated', ?, ?, ?)
		ON CONFLICT (tenant_id, client_reference) DO NOTHING`)
	if _, err := tx.ExecContext(ctx, insertPSP,
		stored.TenantID,
		stored.ProviderCode,
		stored.IdempotencyKey,
		stored.IntentReference,
		stored.Amount,
		stored.Currency,
		stored.WorkflowID,
		stored.RawRequest,
		stored.ID,
	); err != nil {
		return nil, err
	}

	var transaction PSPTransaction
	getPSP := tx.Rebind(`SELECT * FROM psp_transactions
		WHERE tenant_id = ? AND deposit_intent_id = ?`)
	if err := tx.GetContext(ctx, &transaction, getPSP, stored.TenantID, stored.ID); err != nil {
		return nil, err
	}
	if err := ValidateDepositIntentTransaction(&stored, &transaction); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) RecordDepositIntentRun(ctx context.Context, tenantID, reference, workflowID, runID string) (*DepositIntent, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if err := validateBoundedIdentifier(reference, 128, ErrMissingClientReference, ErrInvalidDepositIntent); err != nil {
		return nil, err
	}
	if err := validateBoundedIdentifier(workflowID, 255, ErrMissingWorkflowID, ErrInvalidWorkflowID); err != nil {
		return nil, err
	}
	if err := validateBoundedIdentifier(runID, 255, ErrMissingWorkflowRunID, ErrInvalidWorkflowRunID); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := db.Rebind(`UPDATE deposit_intents
		SET run_id = COALESCE(run_id, ?)
		WHERE tenant_id = ? AND intent_reference = ? AND workflow_id = ?
			AND (run_id IS NULL OR run_id = ?)
		RETURNING *`)
	var intent DepositIntent
	if err := db.GetContext(ctx, &intent, stmt, runID, tenantID, reference, workflowID, runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDuplicateDepositIntent
		}
		return nil, err
	}
	return &intent, nil
}

func ValidateDepositIntentReplay(existing *DepositIntent, requested DepositIntent) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.ProviderCode != requested.ProviderCode ||
		existing.WalletID != requested.WalletID ||
		existing.OwnerType != requested.OwnerType ||
		existing.OwnerID != requested.OwnerID ||
		existing.Amount != requested.Amount ||
		existing.Currency != requested.Currency ||
		existing.IdempotencyKey != requested.IdempotencyKey ||
		existing.Region != requested.Region ||
		!rawJSONMatches(existing.Metadata, requested.Metadata) ||
		!rawJSONMatches(existing.RawRequest, requested.RawRequest) {
		return ErrDuplicateDepositIntent
	}
	return nil
}

func ValidateDepositIntentTransaction(intent *DepositIntent, transaction *PSPTransaction) error {
	if intent == nil || transaction == nil ||
		!transaction.DepositIntentID.Valid || transaction.DepositIntentID.Int64 != intent.ID ||
		transaction.TenantID != intent.TenantID ||
		transaction.PSPProvider != intent.ProviderCode ||
		transaction.IdempotencyKey != intent.IdempotencyKey ||
		transaction.ClientReference != intent.IntentReference ||
		transaction.Direction != "inbound" ||
		transaction.Amount != intent.Amount ||
		transaction.Currency != intent.Currency ||
		!transaction.WorkflowID.Valid || transaction.WorkflowID.String != intent.WorkflowID {
		return ErrInvalidDepositIntent
	}
	return nil
}

func validateDepositIntent(intent DepositIntent) (string, error) {
	tenantID, err := ValidateTenantID(intent.TenantID)
	if err != nil {
		return "", err
	}
	if err := validateBoundedIdentifier(intent.IntentReference, 128, ErrMissingClientReference, ErrInvalidDepositIntent); err != nil {
		return "", err
	}
	if intent.ProviderCode == "" {
		return "", ErrMissingProviderCode
	}
	if intent.WalletID == uuid.Nil {
		return "", ErrMissingWalletID
	}
	if intent.OwnerType == "" {
		return "", ErrMissingOwnerType
	}
	if !OwnerTypeValid(intent.OwnerType) {
		return "", ErrInvalidOwnerType
	}
	if intent.OwnerID == "" {
		return "", ErrMissingOwnerID
	}
	if intent.Amount <= 0 {
		return "", ErrInvalidAmount
	}
	if intent.Currency == "" {
		return "", ErrMissingCurrency
	}
	if err := validateBoundedIdentifier(intent.IdempotencyKey, 256, ErrMissingIdempotencyKey, ErrInvalidIdempotencyKey); err != nil {
		return "", err
	}
	if err := validateBoundedIdentifier(intent.WorkflowID, 255, ErrMissingWorkflowID, ErrInvalidWorkflowID); err != nil {
		return "", err
	}
	if strings.TrimSpace(intent.Region) != intent.Region || len(intent.Region) > 128 || !utf8.ValidString(intent.Region) {
		return "", ErrInvalidDepositIntent
	}
	if !validDepositMetadata(intent.Metadata) || !validDepositRequest(intent.RawRequest) {
		return "", ErrInvalidDepositIntent
	}
	return tenantID, nil
}

func validDepositMetadata(raw RawJSON) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	_, ok := value.(map[string]any)
	return ok
}

func validDepositRequest(raw RawJSON) bool {
	if len(raw) == 0 || !json.Valid(raw) {
		return false
	}
	var value map[string]json.RawMessage
	return json.Unmarshal(raw, &value) == nil && value != nil
}
