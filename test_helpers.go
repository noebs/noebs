package main

import (
	"math/rand"
	"noebs/validations"
	"testing"
	"time"
	"encoding/json"
)

func populatePurchaseFields(missing bool) validations.PurchaseFields {
	// this should be a generic function for all fields
	// it should also respects each struct types
	// lets test populating purchase fields
	fields := validations.PurchaseFields{
		populateWorkingKeyFields(), populateCardInfoFields(),
		populateAmountFields(),
	}

	if missing {
		fields.TerminalID = ""
		return fields
	}
	return fields
}

func populateCardTransferFields() validations.CardTransferFields {
	toCard := "1234567891234567"

	f := validations.CardTransferFields{
		CommonFields:   populateCommmonFields(),
		CardInfoFields: populateCardInfoFields(),
		AmountFields:   populateAmountFields(),
		ToCard:         toCard,
	}

	return f
}

func populateWorkingKeyFields() validations.WorkingKeyFields {
	f := validations.WorkingKeyFields{
		CommonFields: populateCommmonFields(),
	}
	return f
}

func populateCommmonFields() validations.CommonFields {
	f := validations.CommonFields{
		TerminalID:             "12345678",
		TranDateTime:           time.Now().UTC().String(),
		SystemTraceAuditNumber: rand.Int(),
		ClientID:               "noebs",
	}
	return f
}

func populateCardInfoFields() validations.CardInfoFields {
	f := validations.CardInfoFields{
		Pin:     "1234",
		Pan:     "1234567891234567",
		Expdate: "2209",
	}
	return f
}

func populateAmountFields() validations.AmountFields {
	currencyCode := "SDG"
	amount := 32.43
	f := validations.AmountFields{
		TranCurrencyCode: currencyCode,
		TranAmount:       float32(amount),
	}
	return f
}

func getSuccessfulPurchasePayload(service interface{}) []byte{

	// get the purchase struct only, try to generalize it later
	if _, ok := service.(validations.PurchaseFields); ok {
		f := populatePurchaseFields(false)
		jsonFields, err := json.Marshal(f)

		if err != nil{
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
	if err == nil {
		t.Fatalf("There is an error")
		return nil
	}
	return fields
}
