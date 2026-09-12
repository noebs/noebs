package store

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type IdentityVerification struct {
	TenantID        string     `db:"tenant_id" json:"tenant_id"`
	UserID          int64      `db:"user_id" json:"user_id"`
	Status          string     `db:"status" json:"status"`
	Substatus       string     `db:"substatus" json:"substatus"`
	Revision        int64      `db:"revision" json:"revision"`
	SourceSessionID *uuid.UUID `db:"source_session_id" json:"source_session_id,omitempty"`
	UpdatedAt       time.Time  `db:"updated_at" json:"updated_at"`
}

func (s *Store) GetIdentityVerification(ctx context.Context, owner IdentityOwner) (IdentityVerification, error) {
	if err := validateProjectionUserID(owner.TenantID, owner.UserID); err != nil {
		return IdentityVerification{}, err
	}
	db, err := s.ensureDB()
	if err != nil {
		return IdentityVerification{}, err
	}
	return readIdentityVerification(ctx, db, owner)
}

func readIdentityVerification(ctx context.Context, db sqlx.QueryerContext, owner IdentityOwner) (IdentityVerification, error) {
	var result IdentityVerification
	err := sqlx.GetContext(ctx, db, &result, `SELECT tenant_id,user_id,status,substatus,revision,source_session_id,updated_at
	 FROM identity_verifications WHERE tenant_id=$1 AND user_id=$2`, owner.TenantID, owner.UserID)
	return result, err
}
