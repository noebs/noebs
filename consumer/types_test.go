package consumer

import (
	"encoding/json"
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
		args []ebs_fields.CardsRedis
		want []ebs_fields.CardsRedis
	}{
		{"Successful Test",
			b2Slice(fromRedis),
			lcards,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cardsFromZ(fromRedis); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("cardsFromZ() = %v, want %v", got, tt.want)
			}
		})
	}
}

func b2Slice(b []string) []ebs_fields.CardsRedis {
	var cards []ebs_fields.CardsRedis
	json.Unmarshal([]byte(b[0]), &cards)
	return cards
}
