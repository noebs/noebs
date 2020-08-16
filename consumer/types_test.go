package consumer

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/alicebob/miniredis"
)


var mr, _ = miniredis.Run()
var mockRedis = utils.GetRedisClient(mr.Addr())


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

	fromRedis := []string{`{"pan": "1234", "exp_date": "2209", "id": 1}`}

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
	have1 := ebs_fields.CardsRedis{PAN: "1334", Expdate: "2201", ID: 1}
	have2 := ebs_fields.CardsRedis{PAN: "1234", Expdate: "2202", ID: 2}
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
		{"testing to map", f, w},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &paymentTokens{
				Name:   tt.fields.Name,
				Amount: tt.fields.Amount,
				ID:     tt.fields.ID,
			}
			for k, v := range p.toMap(){

				switch v.(type){
				case float32, float64:
					continue
				}

				if tt.want[k] != v {
					t.Errorf("paymentTokens.toMap() = %v, want %v", tt.want[k], v)

					}

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
			if _, err := p.fromRedis(tt.args.id); (err != nil) != tt.wantErr {
				t.Errorf("paymentTokens.getFromRedis() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_paymentTokens_check(t *testing.T) {

	type args struct {
		id     string
		amount float32
	}
	tests := []struct {
		name   string
		args   args
		want   bool
		want1  validationError
	}{
		{"testing validation error", args{id: "my id", amount: 32}, true, validationError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &paymentTokens{
			}
			got, got1 := p.check(tt.args.id, tt.args.amount, mockRedis)
			if got != tt.want {
				t.Errorf("paymentTokens.check() got = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(got1, tt.want1) {
				t.Errorf("paymentTokens.check() got1 = %v, want %v", got1, tt.want1)
			}
		})
	}
}
