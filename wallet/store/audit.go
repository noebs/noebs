package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type AuditEvent struct {
	TenantID   string
	EventType  string
	ActorType  string
	ActorID    string
	TargetType sql.NullString
	TargetID   sql.NullString
	Action     string
	OldValue   json.RawMessage
	NewValue   json.RawMessage
	Metadata   json.RawMessage
	IPAddress  sql.NullString
	UserAgent  sql.NullString
	WorkflowID sql.NullString
	RequestID  sql.NullString
	TraceID    sql.NullString
	CreatedAt  time.Time `db:"created_at"`
}

type AuditLogFilter struct {
	TenantID   string
	EventType  string
	ActorType  string
	ActorID    string
	TargetType string
	TargetID   string
	Action     string
	Start      time.Time
	End        time.Time
	Limit      int
	Offset     int
}

func (s *Store) InsertAuditEvent(ctx context.Context, event AuditEvent) error {
	tenantID, err := ValidateTenantID(event.TenantID)
	if err != nil {
		return err
	}
	if event.EventType == "" {
		return ErrMissingEventType
	}
	if event.ActorType == "" {
		return ErrMissingActorType
	}
	if event.ActorID == "" {
		return ErrMissingActorID
	}
	if event.Action == "" {
		return ErrMissingAction
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	stmt := db.Rebind(`INSERT INTO wallet_audit_log(
		tenant_id, event_type, actor_type, actor_id, target_type, target_id, action,
		old_value, new_value, metadata, ip_address, user_agent, workflow_id, request_id, trace_id
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	_, err = db.ExecContext(ctx, stmt,
		tenantID,
		event.EventType,
		event.ActorType,
		event.ActorID,
		event.TargetType,
		event.TargetID,
		event.Action,
		event.OldValue,
		event.NewValue,
		event.Metadata,
		event.IPAddress,
		event.UserAgent,
		event.WorkflowID,
		event.RequestID,
		event.TraceID,
	)
	return err
}

func (s *Store) ListAuditEvents(ctx context.Context, filter AuditLogFilter) ([]AuditEvent, error) {
	tenantID, err := ValidateTenantID(filter.TenantID)
	if err != nil {
		return nil, err
	}
	if filter.Limit <= 0 {
		return nil, ErrInvalidLimit
	}
	if filter.Offset < 0 {
		return nil, ErrInvalidOffset
	}
	if filter.Start.IsZero() != filter.End.IsZero() {
		if filter.Start.IsZero() {
			return nil, ErrMissingStartTime
		}
		return nil, ErrMissingEndTime
	}
	if !filter.Start.IsZero() && filter.Start.After(filter.End) {
		return nil, ErrInvalidTimeRange
	}
	db, err := s.ensureDB()
	if err != nil {
		return nil, err
	}
	query := `SELECT tenant_id, event_type, actor_type, actor_id, target_type, target_id, action,
		old_value, new_value, metadata, ip_address, user_agent, workflow_id, request_id, trace_id, created_at
		FROM wallet_audit_log WHERE tenant_id = ?`
	args := []any{tenantID}
	if filter.EventType != "" {
		query += " AND event_type = ?"
		args = append(args, filter.EventType)
	}
	if filter.ActorType != "" {
		query += " AND actor_type = ?"
		args = append(args, filter.ActorType)
	}
	if filter.ActorID != "" {
		query += " AND actor_id = ?"
		args = append(args, filter.ActorID)
	}
	if filter.TargetType != "" {
		query += " AND target_type = ?"
		args = append(args, filter.TargetType)
	}
	if filter.TargetID != "" {
		query += " AND target_id = ?"
		args = append(args, filter.TargetID)
	}
	if filter.Action != "" {
		query += " AND action = ?"
		args = append(args, filter.Action)
	}
	if !filter.Start.IsZero() && !filter.End.IsZero() {
		query += " AND created_at >= ? AND created_at <= ?"
		args = append(args, filter.Start, filter.End)
	}
	query += " ORDER BY created_at DESC LIMIT ? OFFSET ?"
	args = append(args, filter.Limit, filter.Offset)
	stmt := db.Rebind(query)
	var rows []AuditEvent
	if err := db.SelectContext(ctx, &rows, stmt, args...); err != nil {
		return nil, err
	}
	return rows, nil
}
