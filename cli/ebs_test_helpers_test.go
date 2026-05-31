package main

import (
	"encoding/json"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

const (
	testTerminalID = "12345678"
	testClientID   = "noebs"
	testPAN        = "1234567891234567"
	testPIN        = "1234"
	testExpiry     = "2209"
	testCurrency   = "SDG"
	testAmount     = 32.43
	testTrace      = 123456
	testTimestamp  = "2026-05-31T00:00:00Z"
)

func populatePurchaseFields(missing bool) ebs_fields.PurchaseFields {
	fields := ebs_fields.PurchaseFields{
		WorkingKeyFields: populateWorkingKeyFields(),
		CardInfoFields:   populateCardInfoFields(),
		AmountFields:     populateAmountFields(),
	}
	if missing {
		fields.TerminalID = ""
	}
	return fields
}

func populateCardTransferFields() ebs_fields.CardTransferFields {
	return ebs_fields.CardTransferFields{
		CommonFields:   populateCommonFields(),
		CardInfoFields: populateCardInfoFields(),
		AmountFields:   populateAmountFields(),
		ToCard:         testPAN,
	}
}

func populateWorkingKeyFields() ebs_fields.WorkingKeyFields {
	return ebs_fields.WorkingKeyFields{
		CommonFields: populateCommonFields(),
	}
}

func populateCommonFields() ebs_fields.CommonFields {
	return ebs_fields.CommonFields{
		TerminalID:             testTerminalID,
		TranDateTime:           testTimestamp,
		SystemTraceAuditNumber: testTrace,
		ClientID:               testClientID,
	}
}

func populateCardInfoFields() ebs_fields.CardInfoFields {
	return ebs_fields.CardInfoFields{
		Pin:     testPIN,
		Pan:     testPAN,
		Expdate: testExpiry,
	}
}

func populateAmountFields() ebs_fields.AmountFields {
	return ebs_fields.AmountFields{
		TranCurrencyCode: testCurrency,
		TranAmount:       float32(testAmount),
	}
}

func getSuccessfulPurchasePayload(t *testing.T) []byte {
	t.Helper()
	payload, err := json.Marshal(populatePurchaseFields(false))
	if err != nil {
		t.Fatalf("marshal successful purchase payload: %v", err)
	}
	return payload
}

func getFailedPurchasePayload(t *testing.T) []byte {
	t.Helper()
	fields := populatePurchaseFields(false)
	fields.TranAmount = -testAmount
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal failed purchase payload: %v", err)
	}
	return payload
}
