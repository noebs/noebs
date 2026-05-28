package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

type TransactionEventCreate struct {
	Topic     string
	EventKey  string
	EventType string
	Payload   []byte
}

type TransactionEvent struct {
	ID              int64           `db:"id"`
	TenantID        string          `db:"tenant_id"`
	Topic           string          `db:"topic"`
	EventKey        string          `db:"event_key"`
	EventType       string          `db:"event_type"`
	Payload         json.RawMessage `db:"payload"`
	PublishAttempts int             `db:"publish_attempts"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

func (s *Store) CreateTransactionWithEvent(ctx context.Context, tenantID string, res ebs_fields.EBSResponse, event TransactionEventCreate) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.UUID) == "" {
		return ErrMissingUUID
	}
	if err := validateTransactionEventCreate(event); err != nil {
		return err
	}
	res.MaskPAN()
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC()
	transactionID, err := s.insertTransaction(ctx, tx, tenantID, res, now)
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`INSERT INTO transaction_events(
		transaction_id, tenant_id, topic, event_key, event_type, payload, publish_attempts, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, 0, ?, ?)`)
	if _, err := tx.ExecContext(ctx, stmt,
		transactionID,
		tenantID,
		strings.TrimSpace(event.Topic),
		strings.TrimSpace(event.EventKey),
		strings.TrimSpace(event.EventType),
		string(event.Payload),
		now,
		now,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func validateTransactionEventCreate(event TransactionEventCreate) error {
	if strings.TrimSpace(event.Topic) == "" {
		return ErrMissingEventTopic
	}
	if strings.TrimSpace(event.EventKey) == "" {
		return ErrMissingEventKey
	}
	if strings.TrimSpace(event.EventType) == "" {
		return ErrMissingEventType
	}
	if len(event.Payload) == 0 || strings.TrimSpace(string(event.Payload)) == "" {
		return ErrMissingEventPayload
	}
	return nil
}

func (s *Store) ClaimPendingTransactionEvents(ctx context.Context, limit int) ([]TransactionEvent, error) {
	if limit <= 0 {
		return nil, ErrMissingData
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	tx, err := db.BeginTxx(ctx, &sql.TxOptions{})
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	stmt := s.DB.Rebind(`SELECT id, tenant_id, topic, event_key, event_type, payload, publish_attempts, created_at, updated_at
		FROM transaction_events
		WHERE published_at IS NULL
		ORDER BY id ASC
		LIMIT ?
		FOR UPDATE SKIP LOCKED`)
	var events []TransactionEvent
	if err := tx.SelectContext(ctx, &events, stmt, limit); err != nil {
		return nil, err
	}
	for _, event := range events {
		if event.ID <= 0 {
			return nil, ErrMissingEventID
		}
		updateStmt := s.DB.Rebind(`UPDATE transaction_events
			SET publish_attempts = publish_attempts + 1, updated_at = ?
			WHERE id = ?`)
		if _, err := tx.ExecContext(ctx, updateStmt, time.Now().UTC(), event.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	committed = true
	return events, nil
}

func (s *Store) MarkTransactionEventPublished(ctx context.Context, eventID int64) error {
	if eventID <= 0 {
		return ErrMissingEventID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := s.DB.Rebind(`UPDATE transaction_events
		SET published_at = ?, last_error = NULL, updated_at = ?
		WHERE id = ?`)
	now := time.Now().UTC()
	result, err := db.ExecContext(ctx, stmt, now, now, eventID)
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

func (s *Store) MarkTransactionEventPublishFailed(ctx context.Context, eventID int64, publishErr error) error {
	if eventID <= 0 {
		return ErrMissingEventID
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	message := ""
	if publishErr != nil {
		message = publishErr.Error()
	}
	stmt := s.DB.Rebind(`UPDATE transaction_events
		SET last_error = ?, updated_at = ?
		WHERE id = ?`)
	result, err := db.ExecContext(ctx, stmt, message, time.Now().UTC(), eventID)
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
