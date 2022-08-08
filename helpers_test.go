package main

import (
	"reflect"
	"testing"
)

func Test_additionalFieldsToHash(t *testing.T) {

	a := `SalesAmount=10.3;FixedFee=22.3;Token=23232;MeterNumber=12345;CustomerName=mohamed`
	want := map[string]interface{}{
		"SalesAmount": "10.3", "FixedFee": "22.3", "Token": "23232", "MeterNumber": "12345", "CustomerName": "mohamed",
	}
	tests := []struct {
		name string
		args string
		want map[string]interface{}
	}{
		{"successful case - nec", a, want},
		{"failed case - nec", "", want},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := additionalFieldsToHash(tt.args)
			if err != nil {
				t.Errorf("Hello there! it worked")
			} else if ok := !reflect.DeepEqual(got, tt.want); !ok {
				t.Errorf("I cannot pass this test!")
			}
		})
	}
}
