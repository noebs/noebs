package main

import (
	"math/rand"
	"time"
)

func populatePurchaseFields(missing bool) PurchaseFields {
	// this should be a generic function for all fields
	// it should also respects each struct types
	// lets test populating purchase fields
	fields := PurchaseFields{
		populateWorkingKeyFields(), populateCardInfoFields(),
		populateAmountFields(),
	}

	if missing {
		fields.TerminalID = ""
		return fields
	}
	return fields
}

func populateCardTransferFields() CardTransferFields {
	toCard := "1234567891234567"

	f := CardTransferFields{
		CommonFields:   populateCommmonFields(),
		CardInfoFields: populateCardInfoFields(),
		AmountFields:   populateAmountFields(),
		ToCard:         toCard,
	}

	return f
}

func populateWorkingKeyFields() WorkingKeyFields {
	f := WorkingKeyFields{
		CommonFields: populateCommmonFields(),
	}
	return f
}

func populateCommmonFields() CommonFields {
	f := CommonFields{
		TerminalID:             "12345678",
		TranDateTime:           time.Now(),
		SystemTraceAuditNumber: rand.Int(),
	}
	return f
}

func populateCardInfoFields() CardInfoFields {
	f := CardInfoFields{
		Pin:     "1234",
		Pan:     "1234567891234567",
		Expdate: "2209",
	}
	return f
}

func populateAmountFields() AmountFields {
	currencyCode := "SDG"
	amount := 32.43
	f := AmountFields{
		TranCurrencyCode: currencyCode,
		TranAmount:       float32(amount),
	}
	return f
}
