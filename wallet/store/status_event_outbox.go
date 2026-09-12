package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	basestore "github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

var (
	ErrInvalidStatusEventLease = errors.New("invalid status event lease")
	ErrStatusEventLeaseLost    = errors.New("status event lease lost")
)

// StatusEventOutbox publishes immutable transition events. A lease covers one
// oldest unpublished event per transaction, preserving order across workers.
type StatusEventOutbox struct {
	store  *Store
	topic  string
	lease  time.Duration
	mu     sync.Mutex
	claims map[int64]string
}

func NewStatusEventOutbox(store *Store, topic string, lease time.Duration) (*StatusEventOutbox, error) {
	if _, err := store.ensureDB(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(topic) == "" {
		return nil, basestore.ErrMissingEventTopic
	}
	if topic != strings.TrimSpace(topic) {
		return nil, basestore.ErrMissingEventTopic
	}
	if lease < time.Second {
		return nil, ErrInvalidStatusEventLease
	}
	return &StatusEventOutbox{store: store, topic: topic, lease: lease, claims: make(map[int64]string)}, nil
}

func (s *StatusEventOutbox) ClaimPendingTransactionEvents(ctx context.Context, limit int) ([]basestore.TransactionEvent, error) {
	if limit <= 0 {
		return nil, ErrInvalidLimit
	}
	token := uuid.NewString()
	stmt := s.store.DB.Rebind(`WITH ready AS (
		SELECT e.id FROM transaction_status_events e
		WHERE e.published_at IS NULL AND (e.claimed_until IS NULL OR e.claimed_until <= clock_timestamp())
		AND NOT EXISTS (SELECT 1 FROM transaction_status_events prior
			WHERE prior.tenant_id = e.tenant_id AND prior.aggregate_id = e.aggregate_id
			AND prior.version < e.version AND prior.published_at IS NULL)
		ORDER BY e.id LIMIT ? FOR UPDATE SKIP LOCKED
	), claimed AS (
		UPDATE transaction_status_events e SET claim_token = ?, claimed_until = clock_timestamp() + (? * interval '1 millisecond'),
		publish_attempts = publish_attempts + 1 FROM ready WHERE e.id = ready.id RETURNING e.*
	)
	SELECT id,tenant_id,?::text AS topic,tenant_id || ':' || aggregate_id AS event_key,
	 'transaction.status.changed.v1' AS event_type,
	 jsonb_build_object('event_id',event_id,'type','transaction.status.changed.v1','tenant_id',tenant_id,
	 'aggregate_type','transaction','aggregate_id',aggregate_id,'version',version,'status',status,
	 'substatus',substatus,'occurred_at',created_at,'user_id',user_id) AS payload,
	 publish_attempts,created_at,created_at AS updated_at FROM claimed ORDER BY id`)
	var events []basestore.TransactionEvent
	if err := s.store.DB.SelectContext(ctx, &events, stmt, limit, token, s.lease.Milliseconds(), s.topic); err != nil {
		return nil, err
	}
	s.mu.Lock()
	for _, event := range events {
		s.claims[event.ID] = token
	}
	s.mu.Unlock()
	return events, nil
}

func (s *StatusEventOutbox) MarkTransactionEventPublished(ctx context.Context, eventID int64) error {
	return s.finish(ctx, eventID, nil)
}

func (s *StatusEventOutbox) MarkTransactionEventPublishFailed(ctx context.Context, eventID int64, publishErr error) error {
	if publishErr == nil {
		return ErrStatusEventLeaseLost
	}
	return s.finish(ctx, eventID, publishErr)
}

func (s *StatusEventOutbox) finish(ctx context.Context, eventID int64, publishErr error) error {
	if eventID <= 0 {
		return basestore.ErrMissingEventID
	}
	s.mu.Lock()
	token, ok := s.claims[eventID]
	s.mu.Unlock()
	if !ok {
		return ErrStatusEventLeaseLost
	}
	query := `UPDATE transaction_status_events SET published_at = clock_timestamp(), last_error = NULL, claim_token = NULL, claimed_until = NULL`
	args := []any{}
	if publishErr != nil {
		query = `UPDATE transaction_status_events SET last_error = ?, claim_token = NULL, claimed_until = NULL`
		args = append(args, publishErr.Error())
	}
	query += ` WHERE id = ? AND claim_token = ? AND published_at IS NULL`
	args = append(args, eventID, token)
	result, err := s.store.DB.ExecContext(ctx, s.store.DB.Rebind(query), args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStatusEventLeaseLost
	}
	s.mu.Lock()
	delete(s.claims, eventID)
	s.mu.Unlock()
	return nil
}
