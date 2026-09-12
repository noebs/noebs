package store

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"sync"

	"github.com/adonese/noebs/internal/identitystate"
)

// IdentityStatusOutbox relays committed changes; it never scans identity cases.
type IdentityStatusOutbox struct {
	Store  *Store
	Topic  string
	mu     sync.Mutex
	claims map[int64]uuid.UUID
}

func (o *IdentityStatusOutbox) ClaimPendingTransactionEvents(ctx context.Context, limit int) ([]TransactionEvent, error) {
	if o.Topic == "" {
		return nil, ErrMissingEventTopic
	}
	if limit < 1 {
		return nil, ErrMissingData
	}
	db, err := o.Store.ensureDB()
	if err != nil {
		return nil, err
	}
	token := uuid.New()
	var result []TransactionEvent
	err = db.SelectContext(ctx, &result, `WITH pending AS (
	 SELECT e.id FROM identity_status_events e WHERE e.published_at IS NULL
	 AND (e.claimed_until IS NULL OR e.claimed_until < clock_timestamp())
	 AND NOT EXISTS(SELECT 1 FROM identity_status_events earlier WHERE earlier.tenant_id=e.tenant_id
	 AND earlier.user_id=e.user_id AND earlier.revision<e.revision AND earlier.published_at IS NULL)
	 ORDER BY e.id LIMIT $1 FOR UPDATE SKIP LOCKED
	) UPDATE identity_status_events e SET claim_token=$3,claimed_until=clock_timestamp()+interval '60 seconds',publish_attempts=publish_attempts+1
	FROM pending WHERE e.id=pending.id RETURNING e.id,e.tenant_id,$2::text AS topic,
	e.tenant_id||':user:'||e.user_id::text AS event_key,'verification.status.changed.v1' AS event_type,
	e.payload,e.publish_attempts,e.created_at,e.created_at AS updated_at`, limit, o.Topic, token)
	if err != nil {
		return nil, err
	}
	o.mu.Lock()
	if o.claims == nil {
		o.claims = make(map[int64]uuid.UUID)
	}
	for _, event := range result {
		o.claims[event.ID] = token
	}
	o.mu.Unlock()
	return result, nil
}

var ErrIdentityEventLeaseLost = errors.New("identity event lease lost")

func (o *IdentityStatusOutbox) MarkTransactionEventPublished(ctx context.Context, id int64) error {
	return o.finish(ctx, id, true)
}
func (o *IdentityStatusOutbox) MarkTransactionEventPublishFailed(ctx context.Context, id int64, publishErr error) error {
	if publishErr == nil {
		return ErrMissingData
	}
	return o.finish(ctx, id, false)
}
func (o *IdentityStatusOutbox) finish(ctx context.Context, id int64, published bool) error {
	if id < 1 {
		return ErrMissingEventID
	}
	o.mu.Lock()
	token, ok := o.claims[id]
	o.mu.Unlock()
	if !ok {
		return ErrIdentityEventLeaseLost
	}
	db, err := o.Store.ensureDB()
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `UPDATE identity_status_events SET
 published_at=CASE WHEN $3 THEN clock_timestamp() ELSE NULL END,
 last_error=CASE WHEN $3 THEN NULL ELSE 'kafka_publish_failed' END,
 claimed_until=NULL,claim_token=NULL WHERE id=$1 AND claim_token=$2 AND published_at IS NULL`, id, token, published)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrIdentityEventLeaseLost
	}
	o.mu.Lock()
	delete(o.claims, id)
	o.mu.Unlock()
	return nil
}

func identityTransition(from, to string) error {
	if err := identitystate.Transition(from, to); err != nil {
		return errors.Join(ErrIdentityConflict, err)
	}
	return nil
}
