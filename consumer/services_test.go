package consumer

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
)

func TestService_isValidCard(t *testing.T) {

	env := newTestEnv(t)
	env.DB.Debug().AutoMigrate(&ebs_fields.CacheCards{})
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
	if err := env.DB.Create(&ebs_fields.Card{Pan: "99999"}).Error; err != nil {
		t.Fatalf("seed card 99999: %v", err)
	}
	if err := env.DB.Create(&ebs_fields.Card{Pan: "88888"}).Error; err != nil {
		t.Fatalf("seed card 88888: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{
				Db: env.DB,
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

func TestService_Notifications(t *testing.T) {

	env := newTestEnv(t)

	user := seedUser(t, env.DB, "0129751986", "Me@Passw0rd!")
	seed := PushData{UUID: "uuid-1", Body: "test me", UserMobile: user.Mobile, Phone: user.Mobile}
	if err := env.DB.Create(&seed).Error; err != nil {
		t.Fatalf("seed notification: %v", err)
	}

	token, err := env.Auth.GenerateJWT(user.ID, user.Mobile)
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}

	req := httptest.NewRequest("GET", "/notifications", nil)
	req.Header.Set("Authorization", token)

	resp, err := env.Router.Test(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}

	var data []PushData
	res, _ := io.ReadAll(resp.Body)
	json.Unmarshal(res, &data)
	if len(data) == 0 {
		t.Errorf("no response")
	}
	if data[0].Body != "test me" {
		t.Error("wrong data")
	}
	if resp.StatusCode != 200 {
		t.Errorf("expected: %d, got: %d", 200, resp.StatusCode)
	}
}
