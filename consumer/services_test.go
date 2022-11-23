package consumer

import (
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestService_isValidCard(t *testing.T) {

	testDB.Debug().AutoMigrate(&ebs_fields.CacheCards{})
	type args struct {
		card ebs_fields.CacheCards
	}
	tests := []struct {
		name    string
		args    args
		want    bool
		wantErr bool
	}{
		{"test is valid", args{ebs_fields.CacheCards{Pan: "99999"}}, true, false},
		{"test is valid", args{ebs_fields.CacheCards{Pan: "88888"}}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{
				Db: testDB,
			}
			got, err := s.isValidCard(tt.args.card)
			if (err != nil) != tt.wantErr {
				t.Errorf("Service.isValidCard() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Service.isValidCard() = %v, want %v", got, tt.want)
			}
		})
	}
}
