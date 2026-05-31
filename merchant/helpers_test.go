package merchant

import (
	"errors"
	"testing"
)

func TestNECBillNewFromMapParsesRequiredFields(t *testing.T) {
	var bill necBill
	err := bill.NewFromMap(map[string]interface{}{
		"netAmount":    "10.5",
		"customerName": "Customer",
		"meterFees":    "1.25",
		"meterNumber":  "04203594959",
		"token":        "07246305192693082213",
	})
	if err != nil {
		t.Fatalf("NewFromMap() error = %v", err)
	}
	if bill.SalesAmount != 10.5 || bill.FixedFee != 1.25 || bill.CustomerName != "Customer" || bill.MeterNumber == "" || bill.Token == "" {
		t.Fatalf("bill = %+v", bill)
	}
}

func TestNECBillNewFromMapRejectsMalformedFields(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]interface{}
	}{
		{
			name: "missing amount",
			fields: map[string]interface{}{
				"customerName": "Customer",
				"meterFees":    "1.25",
				"meterNumber":  "04203594959",
				"token":        "07246305192693082213",
			},
		},
		{
			name: "bad amount",
			fields: map[string]interface{}{
				"netAmount":    "not-a-number",
				"customerName": "Customer",
				"meterFees":    "1.25",
				"meterNumber":  "04203594959",
				"token":        "07246305192693082213",
			},
		},
		{
			name: "non-finite amount",
			fields: map[string]interface{}{
				"netAmount":    "NaN",
				"customerName": "Customer",
				"meterFees":    "1.25",
				"meterNumber":  "04203594959",
				"token":        "07246305192693082213",
			},
		},
		{
			name: "non-string token",
			fields: map[string]interface{}{
				"netAmount":    "10.5",
				"customerName": "Customer",
				"meterFees":    "1.25",
				"meterNumber":  "04203594959",
				"token":        7246305192693082213,
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			bill := necBill{SalesAmount: 99, CustomerName: "old"}
			err := bill.NewFromMap(tt.fields)
			if !errors.Is(err, ErrInvalidBillInfo) {
				t.Fatalf("NewFromMap() error = %v, want %v", err, ErrInvalidBillInfo)
			}
			if bill.SalesAmount != 99 || bill.CustomerName != "old" {
				t.Fatalf("NewFromMap() mutated bill after failure: %+v", bill)
			}
		})
	}
}
