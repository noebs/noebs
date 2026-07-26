package handler

import (
	"errors"
	"testing"

	"github.com/adonese/noebs/consumer"
	"github.com/adonese/noebs/store"
)

func TestNormalizeStorePushDataCommandOwnsTransportNormalization(t *testing.T) {
	const transactionUUID = "ed9de23b-734f-4db4-91f0-b6299a7b80a2"
	cmd := consumer.StorePushDataCommand{Data: consumer.PushData{
		UUID:            " notification-id ",
		To:              " device ",
		Phone:           " 0912 ",
		DeviceID:        " token ",
		UserMobile:      " 0913 ",
		TransactionUUID: " " + transactionUUID + " ",
	}}

	if err := normalizeStorePushDataCommand(&cmd); err != nil {
		t.Fatalf("normalize command: %v", err)
	}
	if cmd.Data.UUID != "notification-id" ||
		cmd.Data.To != "device" ||
		cmd.Data.Phone != "0912" ||
		cmd.Data.DeviceID != "token" ||
		cmd.Data.UserMobile != "0913" {
		t.Fatalf("normalized command = %+v", cmd.Data)
	}
	if cmd.Data.TransactionUUID != transactionUUID || cmd.Data.EBSUUID != transactionUUID {
		t.Fatalf("transaction identities = %+v", cmd.Data)
	}
}

func TestNormalizeStorePushDataCommandRejectsInvalidTransactionUUID(t *testing.T) {
	cmd := consumer.StorePushDataCommand{Data: consumer.PushData{TransactionUUID: "not-a-uuid"}}
	if err := normalizeStorePushDataCommand(&cmd); !errors.Is(err, store.ErrInvalidTransactionUUID) {
		t.Fatalf("error = %v, want %v", err, store.ErrInvalidTransactionUUID)
	}
}
