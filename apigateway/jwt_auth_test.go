package gateway

import (
	"reflect"
	"testing"

	log "github.com/sirupsen/logrus"
)

// var key = []byte("abcdef012345678")
var jj = &JWTAuth{}

func TestVerifyJWT(t *testing.T) {
	jj.Key = []byte("test-key")
	token, err := jj.GenerateJWT(42, "0990000000")
	if err != nil {
		t.Fatalf("GenerateJWT error: %v", err)
	}

	got, err := jj.VerifyJWT(token)
	if err != nil {
		t.Fatalf("VerifyJWT error: %v", err)
	}
	if got.UserID != 42 {
		log.Printf("The key is: %v", jj.Key)
		t.Errorf("VerifyJWT() userID = %v, want = %v", got.UserID, 42)
	}
	if !reflect.DeepEqual(got.Mobile, "0990000000") {
		log.Printf("The key is: %v", jj.Key)
		t.Errorf("VerifyJWT() mobile = %v, want = %v", got.Mobile, "0990000000")
	}
}

func TestJWTAuth_GenerateJWT(t *testing.T) {
	type fields struct {
		Key []byte
	}
	type args struct {
		userID uint
		mobile string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    string
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			j := &JWTAuth{
				Key: tt.fields.Key,
			}
			got, err := j.GenerateJWT(tt.args.userID, tt.args.mobile)
			if (err != nil) != tt.wantErr {
				t.Errorf("JWTAuth.GenerateJWT() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("JWTAuth.GenerateJWT() = %v, want %v", got, tt.want)
			}
		})
	}
}
