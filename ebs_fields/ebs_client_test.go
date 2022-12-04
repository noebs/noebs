package ebs_fields

import (
	"encoding/json"
	"testing"
)

type data struct {}
func (d data) TableName() string {
	return "cache_cards"
}

func Test_updateCardValidity(t *testing.T) {
	type args struct {
		req     []byte
		ebsCode int
	}
	var ebs = EBSParserFields{}
	ebs.PAN = "9222232"
	data, _ := json.Marshal(&ebs)
	tests := []struct {
		name string
		args args
	}{
		{"test_success", args{req: data, ebsCode: 10}},
	}
	type cards struct {
		Pan     string
		IsValid bool
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateCardValidity(tt.args.req, tt.args.ebsCode)
			var res cards
			testDB.Debug().Table("cache_cards").Select("pan", "is_valid").Scan(&res)
			if res.Pan != "9222232" || !res.IsValid {
				t.Errorf("card not working")
			}
		})
	}
}
