package ebs_fields

import (
	"encoding/json"
	"testing"
)

func TestQuickPaymentMarshalsCardTransferPayload(t *testing.T) {
	payload := QuickPaymentFields{
		EncodedPaymentToken: "token-should-not-go-to-ebs",
		ConsumerCardTransferFields: ConsumerCardTransferFields{
			ConsumerCommonFields: ConsumerCommonFields{
				ApplicationId: "app-1",
				UUID:          "request-uuid",
				TranDateTime:  "310526120000",
			},
			ConsumerCardHolderFields: ConsumerCardHolderFields{
				Pan:     "9222081700009999",
				Ipin:    "encrypted-ipin",
				ExpDate: "2601",
			},
			AmountFields: AmountFields{
				TranAmount:       25,
				TranCurrencyCode: "SDG",
			},
			ToCard:      "9222081700000000",
			DynamicFees: 17,
		},
	}

	var got map[string]any
	if err := json.Unmarshal(payload.MarshallP2pFields(), &got); err != nil {
		t.Fatalf("unmarshal quick-payment payload: %v", err)
	}
	if got["token"] != nil {
		t.Fatalf("payload leaked token: %s", payload.MarshallP2pFields())
	}
	if got["toCard"] != "9222081700000000" {
		t.Fatalf("toCard = %v, want card-vault destination", got["toCard"])
	}
	if got["dynamicFees"] != float64(17) {
		t.Fatalf("dynamicFees = %v, want 17", got["dynamicFees"])
	}
}
