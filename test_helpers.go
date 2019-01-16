package main

import (
	"math/rand"
	"noebs/validations"
	"time"
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
