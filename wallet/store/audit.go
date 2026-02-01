package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
}

func (s *Store) InsertAuditEvent(ctx context.Context, event AuditEvent) error {
	if event.TenantID == "" {
		return ErrMissingTenantID
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
		event.TenantID,
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
