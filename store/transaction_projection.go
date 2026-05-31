package store

import (
	"context"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

func (s *Store) UpsertTransactionProjection(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.UUID) == "" {
		return ErrMissingUUID
	}
	res.MaskPAN()
	if _, err := marshalTransactionPayload(res); err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, _, err = s.insertTransaction(ctx, db, tenantID, res, now)
	return err
}
