package consumer

import (
	"encoding/json"
	"fmt"
	"github.com/adonese/noebs/ebs_fields"
	"reflect"
	"testing"
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
