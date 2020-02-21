package ebs_fields

import (
	"reflect"
	"testing"
)

func Test_isEBS(t *testing.T) {

	tests := []struct {
		name string
		args string
		want bool
	}{
		// failed tests
		{"63918658585", "63918658585", true},
		{"failed tests", "858563918658585", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEBS(tt.args); got != tt.want {
				t.Errorf("isEBS() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConsumerCardTransferFields_MustMarshal(t *testing.T) {

	c := ConsumerCardTransferAndMobileFields{
		ConsumerCardTransferFields: ConsumerCardTransferFields{
			ConsumerCommonFields: ConsumerCommonFields{
				ApplicationId: "323232",
				TranDateTime:  "323232",
				UUID:          "23232",
			},
			ConsumerCardHolderFields: ConsumerCardHolderFields{
				Pan:     "32323232",
				Ipin:    "2231",
				ExpDate: "2202",
			},
			AmountFields: AmountFields{
				TranAmount:       0.32320,
				TranCurrencyCode: "4242",
			},
			ToCard: "424242",
		},
		Mobile: "42424242",
	}

	tests := []struct {
		name   string
		fields ConsumerCardTransferAndMobileFields
		want   []byte
	}{
		{"testing marshalling", c, []byte{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			if got := c.MustMarshal(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ConsumerCardTransferFields.MustMarshal() = %v, want %v", string(got), tt.want)
			}
		})
	}
}
