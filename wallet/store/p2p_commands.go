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

type P2PCommand struct {
	TenantID       string         `db:"tenant_id"`
	IdempotencyKey string         `db:"idempotency_key"`
	WorkflowID     string         `db:"workflow_id"`
	FromWalletID   uuid.UUID      `db:"from_wallet_id"`
	ToWalletID     uuid.UUID      `db:"to_wallet_id"`
	FromOwnerType  string         `db:"from_owner_type"`
	FromOwnerID    string         `db:"from_owner_id"`
	ToOwnerType    string         `db:"to_owner_type"`
	ToOwnerID      string         `db:"to_owner_id"`
	Command        RawJSON        `db:"command"`
	RunID          sql.NullString `db:"run_id"`
	CreatedAt      time.Time      `db:"created_at"`
}

type P2PCommandReservation struct {
	TenantID       string
	IdempotencyKey string
	WorkflowID     string
	FromWalletID   uuid.UUID
	ToWalletID     uuid.UUID
	FromOwnerType  string
	FromOwnerID    string
	ToOwnerType    string
	ToOwnerID      string
	Command        RawJSON
}

type P2PCommandPayload struct {
	Currency      string `json:"currency"`
	FromWalletID  string `json:"from_wallet_id"`
	ToWalletID    string `json:"to_wallet_id"`
	Amount        int64  `json:"amount"`
	Description   string `json:"description,omitempty"`
	ReferenceID   string `json:"reference_id"`
	FromOwnerType string `json:"from_owner_type"`
	FromOwnerID   string `json:"from_owner_id"`
	ToOwnerType   string `json:"to_owner_type"`
	ToOwnerID     string `json:"to_owner_id"`
}

func DecodeP2PCommand(command *P2PCommand, tenantID, idempotencyKey, workflowID string) (P2PCommandPayload, error) {
	if command == nil ||
		command.TenantID != tenantID ||
		command.IdempotencyKey != idempotencyKey ||
		command.WorkflowID != workflowID {
		return P2PCommandPayload{}, ErrInvalidP2PCommand
	}
	var payload P2PCommandPayload
	if err := json.Unmarshal(command.Command, &payload); err != nil {
		return P2PCommandPayload{}, ErrInvalidP2PCommand
	}
	fromWalletID, fromErr := uuid.Parse(payload.FromWalletID)
	toWalletID, toErr := uuid.Parse(payload.ToWalletID)
	if fromErr != nil || toErr != nil ||
		fromWalletID != command.FromWalletID || toWalletID != command.ToWalletID ||
		payload.FromOwnerType != command.FromOwnerType || payload.FromOwnerID != command.FromOwnerID ||
		payload.ToOwnerType != command.ToOwnerType || payload.ToOwnerID != command.ToOwnerID {
		return P2PCommandPayload{}, ErrInvalidP2PCommand
	}
	return payload, nil
}

func (s *Store) GetP2PCommand(ctx context.Context, tenantID, idempotencyKey string) (*P2PCommand, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if err := validateBoundedIdentifier(idempotencyKey, 256, ErrMissingIdempotencyKey, ErrInvalidIdempotencyKey); err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	stmt := db.Rebind(`SELECT tenant_id, idempotency_key, workflow_id,
			from_wallet_id, to_wallet_id, from_owner_type, from_owner_id, to_owner_type, to_owner_id,
			command, run_id, created_at
		FROM p2p_commands
		WHERE tenant_id = ? AND idempotency_key = ?`)
	var command P2PCommand
	if err := db.GetContext(ctx, &command, stmt, tenantID, idempotencyKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrP2PCommandNotFound
		}
		return nil, err
	}
	return &command, nil
}

func (s *Store) ReserveP2PCommand(ctx context.Context, command P2PCommandReservation) (*P2PCommand, error) {
	tenantID, err := validateP2PCommand(command)
	if err != nil {
		return nil, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}

	stmt := db.Rebind(`INSERT INTO p2p_commands(
			tenant_id, idempotency_key, workflow_id,
			from_wallet_id, to_wallet_id, from_owner_type, from_owner_id, to_owner_type, to_owner_id,
			command
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT DO NOTHING
		RETURNING tenant_id, idempotency_key, workflow_id,
			from_wallet_id, to_wallet_id, from_owner_type, from_owner_id, to_owner_type, to_owner_id,
			command, run_id, created_at`)
	var stored P2PCommand
	err = db.GetContext(ctx, &stored, stmt,
		tenantID,
		command.IdempotencyKey,
		command.WorkflowID,
		command.FromWalletID,
		command.ToWalletID,
		command.FromOwnerType,
		command.FromOwnerID,
		command.ToOwnerType,
		command.ToOwnerID,
		command.Command,
	)
	if errors.Is(err, sql.ErrNoRows) {
		existing, loadErr := s.GetP2PCommand(ctx, tenantID, command.IdempotencyKey)
		if loadErr != nil {
			if errors.Is(loadErr, ErrP2PCommandNotFound) {
				return nil, ErrDuplicateP2PCommand
			}
			return nil, loadErr
		}
		if err := ValidateP2PCommandReplay(existing, command); err != nil {
			return nil, err
		}
		return existing, nil
	}
	if err != nil {
		return nil, err
	}
	return &stored, nil
}

func (s *Store) RecordP2PCommandRun(
	ctx context.Context,
	tenantID, idempotencyKey, workflowID, runID string,
) (*P2PCommand, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if err := validateBoundedIdentifier(idempotencyKey, 256, ErrMissingIdempotencyKey, ErrInvalidIdempotencyKey); err != nil {
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

	stmt := db.Rebind(`UPDATE p2p_commands
		SET run_id = COALESCE(run_id, ?)
		WHERE tenant_id = ?
			AND idempotency_key = ?
			AND workflow_id = ?
			AND (run_id IS NULL OR run_id = ?)
		RETURNING tenant_id, idempotency_key, workflow_id,
			from_wallet_id, to_wallet_id, from_owner_type, from_owner_id, to_owner_type, to_owner_id,
			command, run_id, created_at`)
	var stored P2PCommand
	if err := db.GetContext(ctx, &stored, stmt, runID, tenantID, idempotencyKey, workflowID, runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrDuplicateP2PCommand
		}
		return nil, err
	}
	return &stored, nil
}

func ValidateP2PCommandReplay(existing *P2PCommand, requested P2PCommandReservation) error {
	if existing == nil ||
		existing.TenantID != requested.TenantID ||
		existing.IdempotencyKey != requested.IdempotencyKey ||
		existing.WorkflowID != requested.WorkflowID ||
		existing.FromWalletID != requested.FromWalletID ||
		existing.ToWalletID != requested.ToWalletID ||
		existing.FromOwnerType != requested.FromOwnerType ||
		existing.FromOwnerID != requested.FromOwnerID ||
		existing.ToOwnerType != requested.ToOwnerType ||
		existing.ToOwnerID != requested.ToOwnerID ||
		!rawJSONMatches(existing.Command, requested.Command) {
		return ErrDuplicateP2PCommand
	}
	return nil
}

func validateP2PCommand(command P2PCommandReservation) (string, error) {
	tenantID, err := ValidateTenantID(command.TenantID)
	if err != nil {
		return "", err
	}
	if err := validateBoundedIdentifier(command.IdempotencyKey, 256, ErrMissingIdempotencyKey, ErrInvalidIdempotencyKey); err != nil {
		return "", err
	}
	if err := validateBoundedIdentifier(command.WorkflowID, 255, ErrMissingWorkflowID, ErrInvalidWorkflowID); err != nil {
		return "", err
	}
	if command.FromWalletID == uuid.Nil || command.ToWalletID == uuid.Nil {
		return "", ErrMissingWalletID
	}
	if command.FromWalletID == command.ToWalletID {
		return "", ErrInvalidWalletPair
	}
	if command.FromOwnerType == "" || command.ToOwnerType == "" {
		return "", ErrMissingOwnerType
	}
	if !OwnerTypeValid(command.FromOwnerType) || !OwnerTypeValid(command.ToOwnerType) {
		return "", ErrInvalidOwnerType
	}
	if command.FromOwnerID == "" || command.ToOwnerID == "" {
		return "", ErrMissingOwnerID
	}
	if strings.TrimSpace(command.FromOwnerID) != command.FromOwnerID || strings.TrimSpace(command.ToOwnerID) != command.ToOwnerID {
		return "", ErrInvalidP2PCommand
	}
	switch {
	case len(command.Command) == 0:
		return "", ErrMissingP2PCommand
	case !json.Valid(command.Command):
		return "", ErrInvalidP2PCommand
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(command.Command, &document); err != nil || document == nil {
		return "", ErrInvalidP2PCommand
	}
	return tenantID, nil
}

func validateBoundedIdentifier(value string, maxLength int, missing, invalid error) error {
	switch {
	case value == "":
		return missing
	case len(value) > maxLength,
		strings.TrimSpace(value) != value,
		strings.ContainsRune(value, '\x00'),
		!utf8.ValidString(value):
		return invalid
	default:
		return nil
	}
}
