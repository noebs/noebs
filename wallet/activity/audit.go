package activity

import (
	"context"

	walletstore "github.com/adonese/noebs/wallet/store"
)

type AuditActivities struct {
	Store *walletstore.Store
}

func NewAuditActivities(store *walletstore.Store) *AuditActivities {
	return &AuditActivities{Store: store}
}

func (a *AuditActivities) RecordAuditEvent(ctx context.Context, event walletstore.AuditEvent) error {
	if a == nil || a.Store == nil {
		return ErrMissingStore
	}
	return a.Store.InsertAuditEvent(ctx, event)
}
