package consumer

import (
	"context"
	"errors"

	"github.com/adonese/noebs/store"
)

var (
	ErrMissingTransactionActor     = errors.New("missing transaction actor")
	ErrMissingTransactionOwnership = errors.New("missing transaction ownership mode")
)

type transactionParticipantsContextKey struct{}

type transactionParticipants struct {
	allowsNoConsumer bool
	actorUserID      int64
	recipientUserID  []int64
}

// WithNoConsumerTransactionParticipants explicitly marks rail/bootstrap work
// that has no authenticated consumer owner.
func WithNoConsumerTransactionParticipants(ctx context.Context) context.Context {
	return context.WithValue(ctx, transactionParticipantsContextKey{}, transactionParticipants{allowsNoConsumer: true})
}

// WithTransactionActor binds the authenticated gateway identity to financial
// work performed through ctx. Public bootstrap handlers must not call it.
func WithTransactionActor(ctx context.Context, userID int64) (context.Context, error) {
	if userID <= 0 {
		return nil, store.ErrInvalidUserID
	}
	return context.WithValue(ctx, transactionParticipantsContextKey{}, transactionParticipants{actorUserID: userID}), nil
}

func withTransactionRecipient(ctx context.Context, userID int64) (context.Context, error) {
	if userID <= 0 {
		return nil, store.ErrInvalidUserID
	}
	participants, ok := ctx.Value(transactionParticipantsContextKey{}).(transactionParticipants)
	if !ok || participants.allowsNoConsumer || participants.actorUserID <= 0 {
		return nil, ErrMissingTransactionActor
	}
	for _, existing := range participants.recipientUserID {
		if existing == userID {
			return ctx, nil
		}
	}
	participants.recipientUserID = append(append([]int64(nil), participants.recipientUserID...), userID)
	return context.WithValue(ctx, transactionParticipantsContextKey{}, participants), nil
}

func transactionParticipantsForRecord(ctx context.Context) ([]store.TransactionParticipant, error) {
	participants, ok := ctx.Value(transactionParticipantsContextKey{}).(transactionParticipants)
	if !ok {
		return nil, ErrMissingTransactionOwnership
	}
	if participants.allowsNoConsumer {
		return nil, nil
	}
	if participants.actorUserID <= 0 {
		return nil, ErrMissingTransactionActor
	}
	recordParticipants := make([]store.TransactionParticipant, 1, 1+len(participants.recipientUserID))
	recordParticipants[0] = store.TransactionParticipant{UserID: participants.actorUserID, Role: store.TransactionParticipantActor}
	for _, userID := range participants.recipientUserID {
		recordParticipants = append(recordParticipants, store.TransactionParticipant{UserID: userID, Role: store.TransactionParticipantRecipient})
	}
	return recordParticipants, nil
}
