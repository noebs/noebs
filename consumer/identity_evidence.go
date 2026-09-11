package consumer

import (
	"context"

	"github.com/adonese/noebs/store"
	"github.com/google/uuid"
)

// Evidence intake stays within the identity service. It never changes wallet
// KYC tiers or exports evidence to a payment rail.
func (s *Service) CreateIdentitySession(ctx context.Context, params store.CreateIdentitySessionParams) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.Store.CreateIdentitySession(ctx, params)
}

func (s *Service) GetIdentitySession(ctx context.Context, owner store.IdentityOwner, id uuid.UUID) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.Store.GetIdentitySession(ctx, owner, id)
}

func (s *Service) PutIdentityEvidence(ctx context.Context, params store.PutIdentityEvidenceParams) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.Store.PutIdentityEvidence(ctx, params)
}

func (s *Service) SubmitIdentitySession(ctx context.Context, owner store.IdentityOwner, id uuid.UUID, submission store.IdentitySubmission) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.Store.SubmitIdentitySession(ctx, owner, id, submission)
}

func (s *Service) DiscardIdentitySession(ctx context.Context, owner store.IdentityOwner, id uuid.UUID, revision int64) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.Store.DiscardIdentitySession(ctx, owner, id, revision)
}
