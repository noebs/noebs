package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/adonese/noebs/internal/statusevent"
)

var ErrStatusNotificationConflict = errors.New("status notification conflicts with its existing receipt")

func (s *Store) StoreStatusNotification(ctx context.Context, event statusevent.Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	if _, err := ValidateTenantID(event.TenantID); err != nil {
		return err
	}
	// System transactions have no customer recipient.
	if event.UserID == nil {
		return nil
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := db.ExecContext(ctx, `INSERT INTO status_notifications(tenant_id,user_id,event_id,aggregate_type,aggregate_id,version,payload,occurred_at)
	VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, event.TenantID, *event.UserID, event.ID, event.AggregateType, event.AggregateID, event.Version, string(payload), event.OccurredAt)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil || inserted == 1 {
		return err
	}
	var identical bool
	err = db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM status_notifications
	 WHERE tenant_id=$1 AND user_id=$2 AND event_id=$3 AND payload=$4::jsonb)`, event.TenantID, *event.UserID, event.ID, string(payload)).Scan(&identical)
	if err != nil {
		return err
	}
	if !identical {
		return ErrStatusNotificationConflict
	}
	return nil
}

func (s *Store) ListStatusNotifications(ctx context.Context, tenantID string, userID int64, limit int) ([]statusevent.Event, error) {
	if err := validateProjectionUserID(tenantID, userID); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 100 {
		return nil, ErrMissingData
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT payload FROM status_notifications WHERE tenant_id=$1 AND user_id=$2 ORDER BY occurred_at DESC,event_id LIMIT $3`, tenantID, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []statusevent.Event{}
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		event, err := statusevent.Parse(payload)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
