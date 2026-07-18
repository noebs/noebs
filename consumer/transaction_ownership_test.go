package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestTransactionParticipantContextRequiresActorAndDeduplicatesRecipients(t *testing.T) {
	if _, err := WithTransactionActor(context.Background(), 0); !errors.Is(err, store.ErrInvalidUserID) {
		t.Fatalf("invalid actor error = %v, want %v", err, store.ErrInvalidUserID)
	}
	if _, err := withTransactionRecipient(context.Background(), 84); !errors.Is(err, ErrMissingTransactionActor) {
		t.Fatalf("recipient without actor error = %v, want %v", err, ErrMissingTransactionActor)
	}

	ctx, err := WithTransactionActor(context.Background(), 42)
	if err != nil {
		t.Fatalf("bind actor: %v", err)
	}
	for _, userID := range []int64{84, 42, 84} {
		ctx, err = withTransactionRecipient(ctx, userID)
		if err != nil {
			t.Fatalf("bind recipient %d: %v", userID, err)
		}
	}
	participants, err := transactionParticipantsForRecord(ctx)
	if err != nil {
		t.Fatalf("read participant IDs: %v", err)
	}
	if len(participants) != 3 ||
		participants[0] != (store.TransactionParticipant{UserID: 42, Role: store.TransactionParticipantActor}) ||
		participants[1] != (store.TransactionParticipant{UserID: 84, Role: store.TransactionParticipantRecipient}) ||
		participants[2] != (store.TransactionParticipant{UserID: 42, Role: store.TransactionParticipantRecipient}) {
		t.Fatalf("participants = %v, want actor 42 and recipients 84/42", participants)
	}

	if _, err := transactionParticipantsForRecord(context.Background()); !errors.Is(err, ErrMissingTransactionOwnership) {
		t.Fatalf("unmarked context error = %v, want %v", err, ErrMissingTransactionOwnership)
	}
	publicParticipants, err := transactionParticipantsForRecord(WithNoConsumerTransactionParticipants(context.Background()))
	if err != nil {
		t.Fatalf("read public participant IDs: %v", err)
	}
	if len(publicParticipants) != 0 {
		t.Fatalf("public participants = %v, want none", publicParticipants)
	}
}
