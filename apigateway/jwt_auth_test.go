package gateway

import (
	"reflect"
	"testing"

	log "github.com/sirupsen/logrus"
)

// var key = []byte("abcdef012345678")
var jj = &JWTAuth{}

func TestVerifyJWT(t *testing.T) {
	// jj.Key = "12345678"
	// j, _ := jj.GenerateJWT("test")

	tests := []struct {
		name string
		key  string
		have string
		want string
	}{
		// {"test_successful_retrieval","eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6InRlc3QiLCJleHAiOjE1OTg3NDM5MDMsImlzcyI6Im5vZWJzIn0.K923HlMMA-Dt1RD7L7DBQJNQMutNXskGXrFZj8cOcTk", "test", "test1"},
		{"test_nil", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6ImFkb25lc2UiLCJleHAiOjE1OTg3MzkyMzAsImlzcyI6Im5vZWJzIn0.bmq95t9TDQnsma4aaQXvrHpUea6P9hb-TK2qKirFyCI", "test", "test1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := jj.VerifyJWT(tt.key)
			if err != nil {
				t.Fatalf("Error is: %v", err)

			}
			if !reflect.DeepEqual(got.Mobile, tt.want) {
				log.Printf("The key is: %v", jj.Key)
				t.Errorf("VerifyJWT() got = %v, want = %v", got.Mobile, tt.want)
			}
		})
	}
}

func TestJWTAuth_GenerateJWT(t *testing.T) {
	type fields struct {
		Key []byte
	}
	type args struct {
		serviceID string
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
			got, err := j.GenerateJWT(tt.args.serviceID)
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
