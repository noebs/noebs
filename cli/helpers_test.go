package main

import (
	"reflect"
	"testing"
)

func Test_additionalFieldsToHash(t *testing.T) {

	a := `SalesAmount=10.3;FixedFee=22.3;Token=23232;MeterNumber=12345;CustomerName=mohamed`
	want := map[string]string{
		"SalesAmount": "10.3", "FixedFee": "22.3", "Token": "23232", "MeterNumber": "12345", "CustomerName": "mohamed",
	}
	tests := []struct {
		name    string
		args    string
		want    map[string]string
		wantErr bool
	}{
		{"successful case - nec", a, want, false},
		{"failed case - nec", "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := additionalFieldsToHash(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("unexpected map: %#v", got)
			}
		})
	}
}
