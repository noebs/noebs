package consumer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func Test_cardsFromZ(t *testing.T) {
	lcards := []ebs_fields.CardsRedis{
		{
			PAN:     "1234",
			Expdate: "2209",
			IsMain:  false,
			ID:      1,
		},
	}
	_, err := json.Marshal(lcards)
	if err != nil {
		t.Fatalf("there is an error in testing: %v\n", err)
	}

	fromRedis := []string{`{"pan": "1234", "exp_date": "2209"}`}

	tests := []struct {
		name string
		args []string
		want []ebs_fields.CardsRedis
	}{
		{"Successful Test",
			fromRedis,
			lcards,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardsFromZ(fromRedis); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("cardsFromZ() = %v, want %v", got, tt.want)
			} else {
				fmt.Printf("they are: %v - %v", got[0], tt.want[0])
			}
		})
	}
}

func Test_generateCardsIds(t *testing.T) {
	have1 := ebs_fields.CardsRedis{PAN: "1334", Expdate: "2201"}
	have2 := ebs_fields.CardsRedis{PAN: "1234", Expdate: "2202"}
	have := &[]ebs_fields.CardsRedis{
		have1, have2,
	}
	want := []ebs_fields.CardsRedis{
		{PAN: "1334", Expdate: "2201", ID: 1},
		{PAN: "1234", Expdate: "2202", ID: 2},
	}
	tests := []struct {
		name string
		have *[]ebs_fields.CardsRedis
		want []ebs_fields.CardsRedis
	}{
		{"testing equality", have, want},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generateCardsIds(tt.have)
			for i, c := range *tt.have {
				if !reflect.DeepEqual(c, tt.want[i]) {
					t.Errorf("have: %v, want: %v", c, tt.want[i])
				}
			}
		})
	}
}

func Test_paymentTokens_toMap(t *testing.T) {

	type fields struct {
		Name   string
		Amount float32
		ID     string
	}
	f := fields{Name: "mohamed", Amount: 30.2, ID: "my id"}
	w := map[string]interface{}{
		"name": "mohamed", "amount": 30.2, "id": "my id",
	}
	tests := []struct {
		name   string
		fields fields
		want   map[string]interface{}
	}{
		{"successful test", f, w},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &paymentTokens{
				Name:   tt.fields.Name,
				Amount: tt.fields.Amount,
				ID:     tt.fields.ID,
			}
			if got := p.toMap(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("paymentTokens.toMap() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_paymentTokens_getFromRedis(t *testing.T) {
	type fields struct {
		Name   string
		Amount float32
		ID     string
		UUID   string
	}
	type args struct {
		id string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &paymentTokens{
				Name:   tt.fields.Name,
				Amount: tt.fields.Amount,
				ID:     tt.fields.ID,
				UUID:   tt.fields.UUID,
			}
			if err := p.getFromRedis(tt.args.id); (err != nil) != tt.wantErr {
				t.Errorf("paymentTokens.getFromRedis() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
