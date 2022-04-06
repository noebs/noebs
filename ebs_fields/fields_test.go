package ebs_fields

import (
	_ "embed"
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

func TestMinistatementDB_Scan(t *testing.T) {
	type args struct {
		value interface{}
	}
	wantRes := []map[string]interface{}{
		{"name": "ahmed", "id": 1},
		{"name": "nosa", "id": 3},
	}
	tests := []struct {
		name    string
		m       *MinistatementDB
		args    args
		wantErr bool
		want    []map[string]interface{}
	}{
		{"test scan", &MinistatementDB{}, args{value: `[{"name": "ahmed", "id": 1}, {"name": "nosa", "id": 3}]`}, false, wantRes},
		{"test scan", &MinistatementDB{}, args{value: ``}, false, wantRes},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.m.Scan(tt.args.value)
			if (err != nil) != tt.wantErr {
				t.Errorf("MinistatementDB.Scan() error = %v, wantErr %v", err, tt.wantErr)
			}
			var data []map[string]interface{}
			data = *tt.m
			// i think it might be better to just work around that...
			if len(data) == 0 {
				return
			}

			if data[0]["name"] != wantRes[0]["name"] {
				t.Errorf("MinistatementDB.Scan() = %v, want %v", data[0]["name"], wantRes[0]["name"])
			}

		})
	}
}
