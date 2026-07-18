package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/jmoiron/sqlx"
)

type TransactionParticipantRole string

const (
	TransactionParticipantActor     TransactionParticipantRole = "actor"
	TransactionParticipantRecipient TransactionParticipantRole = "recipient"
)

type TransactionParticipant struct {
	UserID int64
	Role   TransactionParticipantRole
}

func validateTransactionParticipants(participants []TransactionParticipant) error {
	seen := make(map[TransactionParticipant]struct{}, len(participants))
	for _, participant := range participants {
		if participant.UserID <= 0 {
			return ErrInvalidUserID
		}
		if participant.Role != TransactionParticipantActor && participant.Role != TransactionParticipantRecipient {
			return ErrInvalidParticipantRole
		}
		if _, exists := seen[participant]; exists {
			return ErrDuplicateParticipant
		}
		seen[participant] = struct{}{}
	}
	return nil
}

func (s *Store) insertTransactionParticipants(ctx context.Context, tx *sqlx.Tx, tenantID string, transactionID int64, participants []TransactionParticipant, now time.Time) error {
	for _, participant := range participants {
		stmt := s.DB.Rebind(`INSERT INTO transaction_participants(transaction_id, tenant_id, user_id, role, created_at)
			SELECT id, tenant_id, ?, ?, ?
			FROM transactions
			WHERE id = ? AND tenant_id = ?`)
		if err := execContextRequireRowsAffected(ctx, tx, stmt, participant.UserID, participant.Role, now, transactionID, tenantID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) validateExistingTransactionParticipants(ctx context.Context, tx *sqlx.Tx, tenantID string, transactionID int64, requested []TransactionParticipant) error {
	stmt := s.DB.Rebind(`SELECT user_id, role
		FROM transaction_participants
		WHERE tenant_id = ? AND transaction_id = ?
		ORDER BY user_id, role`)
	var existing []struct {
		UserID int64                      `db:"user_id"`
		Role   TransactionParticipantRole `db:"role"`
	}
	if err := tx.SelectContext(ctx, &existing, stmt, tenantID, transactionID); err != nil {
		return err
	}

	canonicalRequested := append([]TransactionParticipant(nil), requested...)
	sort.Slice(canonicalRequested, func(i, j int) bool {
		if canonicalRequested[i].UserID == canonicalRequested[j].UserID {
			return canonicalRequested[i].Role < canonicalRequested[j].Role
		}
		return canonicalRequested[i].UserID < canonicalRequested[j].UserID
	})
	if len(existing) != len(canonicalRequested) {
		return ErrDuplicateTransaction
	}
	for i := range existing {
		if existing[i].UserID != canonicalRequested[i].UserID || existing[i].Role != canonicalRequested[i].Role {
			return ErrDuplicateTransaction
		}
	}
	return nil
}

func (s *Store) GetTransactionsByParticipantUserID(ctx context.Context, tenantID string, userID int64) ([]ebs_fields.EBSResponse, error) {
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
	stmt := s.DB.Rebind(`SELECT transactions.payload
		FROM transactions
		WHERE transactions.tenant_id = ?
		  AND EXISTS (
			SELECT 1
			FROM transaction_participants
			WHERE transaction_participants.transaction_id = transactions.id
			  AND transaction_participants.tenant_id = transactions.tenant_id
			  AND transaction_participants.user_id = ?
		  )
		ORDER BY transactions.id DESC`)
	rows, err := db.QueryxContext(ctx, stmt, tenantID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	transactions := make([]ebs_fields.EBSResponse, 0)
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		transaction, err := decodeStoredTransactionPayload(payload, fmt.Sprintf("participant user_id %d", userID))
		if err != nil {
			return nil, err
		}
		transactions = append(transactions, transaction)
	}
	return transactions, rows.Err()
}

func (s *Store) GetTransactionByUUIDForParticipantUserID(ctx context.Context, tenantID string, userID int64, uuid string) (*ebs_fields.EBSResponse, error) {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return nil, err
	}
	if userID <= 0 {
		return nil, ErrInvalidUserID
	}
	uuid = strings.TrimSpace(uuid)
	if uuid == "" {
		return nil, ErrMissingUUID
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	stmt := s.DB.Rebind(`SELECT transactions.payload
		FROM transactions
		WHERE transactions.tenant_id = ?
		  AND transactions.uuid = ?
		  AND EXISTS (
			SELECT 1
			FROM transaction_participants
			WHERE transaction_participants.transaction_id = transactions.id
			  AND transaction_participants.tenant_id = transactions.tenant_id
			  AND transaction_participants.user_id = ?
		  )
		ORDER BY transactions.id DESC
		LIMIT 1`)
	var payload string
	if err := db.GetContext(ctx, &payload, stmt, tenantID, uuid, userID); err != nil {
		return nil, err
	}
	transaction, err := decodeStoredTransactionPayload(payload, fmt.Sprintf("uuid %q for participant user_id %d", uuid, userID))
	if err != nil {
		return nil, err
	}
	return &transaction, nil
}
