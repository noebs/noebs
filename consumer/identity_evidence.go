package consumer

import (
	"context"
	"github.com/adonese/noebs/internal/verification"

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

func (s *Service) LatestIdentitySession(ctx context.Context, owner store.IdentityOwner) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.Store.LatestIdentitySession(ctx, owner)
}

func (s *Service) WithdrawIdentitySession(ctx context.Context, owner store.IdentityOwner, id uuid.UUID, revision int64) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.IdentityWorkflow.Execute(ctx, verification.Command{Action: "withdraw", Owner: owner, SessionID: id, Revision: revision})
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
	return s.IdentityWorkflow.Execute(ctx, verification.Command{Action: "submit", Owner: owner, SessionID: id, Submission: submission})
}

func (s *Service) DiscardIdentitySession(ctx context.Context, owner store.IdentityOwner, id uuid.UUID, revision int64) (store.IdentitySession, error) {
	if s == nil || s.Store == nil {
		return store.IdentitySession{}, ErrMissingStore
	}
	return s.IdentityWorkflow.Execute(ctx, verification.Command{Action: "discard", Owner: owner, SessionID: id, Revision: revision})
}

func (s *Service) ListIdentityReviewQueue(ctx context.Context, reviewer store.IdentityReviewer, limit, offset int) ([]store.IdentityReviewQueueItem, error) {
	return s.Store.ListIdentityReviewQueue(ctx, reviewer, limit, offset)
}
func (s *Service) ReadIdentityReviewCase(ctx context.Context, reviewer store.IdentityReviewer, owner store.IdentityOwner, id uuid.UUID) (store.IdentityReviewCase, error) {
	return s.Store.ReadIdentityReviewCase(ctx, reviewer, owner, id)
}
func (s *Service) ReadIdentityReviewEvidence(ctx context.Context, reviewer store.IdentityReviewer, owner store.IdentityOwner, id uuid.UUID, revision int64, kind string) ([]byte, error) {
	return s.Store.ReadIdentityReviewEvidence(ctx, reviewer, owner, id, revision, kind)
}
func (s *Service) DecideIdentityReview(ctx context.Context, params store.IdentityReviewDecisionParams) (store.IdentitySession, error) {
	return s.IdentityWorkflow.Execute(ctx, verification.Command{Action: "decide", Owner: params.Owner, SessionID: params.SessionID, Decision: params})
}
