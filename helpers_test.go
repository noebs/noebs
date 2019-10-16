package main

import (
	"reflect"
	"testing"
)

//func Test_handleAdditionalFields(t *testing.T) {
//	type args struct {
//		fields *ebs_fields.GenericEBSResponseFields
//	}
//	tests := []struct {
//		name string
//		args args
//		want map[string]interface{}
//	}{
//		// TODO: Add test cases.
//	}
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			if got := handleAdditionalFields(tt.args.fields); !reflect.DeepEqual(got, tt.want) {
//				t.Errorf("handleAdditionalFields() = %v, want %v", got, tt.want)
//			}
//		})
//	}
//}

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

//func Test_handleChan(t *testing.T) {
//	//var ch = make(chan *ebs_fields.GenericEBSResponseFields)
//	f := generateFields()
//	billChan <- *f
//	want := wantFields()
//	tests := []struct {
//		name  string
//		want  necBill
//		have necBill
//	}{
//		{"nec successful stuff", want, want},
//	}
//	for _, tt := range tests {
//		t.Run(tt.name, func(t *testing.T) {
//			got, ok := handleChan()
//			if !ok {
//				t.Errorf("there's an error")
//			}
//			g := got.(necBill)
//			if ok := reflect.DeepEqual(g, tt.want); !ok{
//				t.Errorf("there is no workign here: have: :%v, %v", g, tt.want)
//			}
//		})
//	}
//}

func wantFields() necBill {
	v := necBill{
		SalesAmount:  10.3,
		FixedFee:     22.3,
		Token:        "23232",
		MeterNumber:  "12345",
		CustomerName: "mohamed",
	}
	return v
}

func Test_generateUUID(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateUUID(); got != tt.want {
				t.Errorf("generateUUID() = %v, want %v", got, tt.want)
			}
		})
	}

}
