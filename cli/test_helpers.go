package main

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

func populatePurchaseFields(missing bool) ebs_fields.PurchaseFields {
	//FIXME
	// accept the required transaction as an interface and return a struct.

	// this should be a generic function for all fields
	// it should also respects each struct types
	// lets test populating purchase fields
	fields := ebs_fields.PurchaseFields{
		WorkingKeyFields: populateWorkingKeyFields(), CardInfoFields: populateCardInfoFields(),
		AmountFields: populateAmountFields(),
	}

	if missing {
		fields.TerminalID = ""
		return fields
	}
	return fields
}

func populateCardTransferFields() ebs_fields.CardTransferFields {
	toCard := "1234567891234567"

	f := ebs_fields.CardTransferFields{
		CommonFields:   populateCommmonFields(),
		CardInfoFields: populateCardInfoFields(),
		AmountFields:   populateAmountFields(),
		ToCard:         toCard,
	}

	return f
}

func populateWorkingKeyFields() ebs_fields.WorkingKeyFields {
	f := ebs_fields.WorkingKeyFields{
		CommonFields: populateCommmonFields(),
	}
	return f
}

func populateCommmonFields() ebs_fields.CommonFields {
	f := ebs_fields.CommonFields{
		TerminalID:             "12345678",
		TranDateTime:           time.Now().UTC().String(),
		SystemTraceAuditNumber: rand.Int(),
		ClientID:               "noebs",
	}
	return f
}

func populateCardInfoFields() ebs_fields.CardInfoFields {
	f := ebs_fields.CardInfoFields{
		Pin:     "1234",
		Pan:     "1234567891234567",
		Expdate: "2209",
	}
	return f
}

func populateAmountFields() ebs_fields.AmountFields {
	currencyCode := "SDG"
	amount := 32.43
	f := ebs_fields.AmountFields{
		TranCurrencyCode: currencyCode,
		TranAmount:       float32(amount),
	}
	return f
}

func getSuccessfulPurchasePayload(service interface{}) []byte {

	// get the purchase struct only, try to generalize it later
	if _, ok := service.(ebs_fields.PurchaseFields); ok {
		f := populatePurchaseFields(false)
		jsonFields, err := json.Marshal(f)

		if err != nil {
			return nil
		}
		return jsonFields
	} else {
		return nil
	}

}

// invalid transaction fields to `which`? For now, let us make it for purchase ONLY.
func getFailedPurchasePayload(t *testing.T, service interface{}) []byte {
	t.Helper()
	p := populatePurchaseFields(false)
	p.TranAmount = -32.43
	fields, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("There is an error: %s", err.Error())
		return nil
	}
	return fields
}
