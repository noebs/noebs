package gateway

import (
	"reflect"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
	log "github.com/sirupsen/logrus"
)

// var key = []byte("abcdef012345678")
var jj = &JWTAuth{}


func TestVerifyJWT(t *testing.T) {
	// jj.Key = "12345678"
	// j, _ := jj.GenerateJWT("test")
	
	tests := []struct {
		name string
		key string
		have string
		want string
	}{
		// {"test_successful_retrieval","eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6InRlc3QiLCJleHAiOjE1OTg3NDM5MDMsImlzcyI6Im5vZWJzIn0.K923HlMMA-Dt1RD7L7DBQJNQMutNXskGXrFZj8cOcTk", "test", "test1"},
		{"test_nil","eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJ1c2VybmFtZSI6ImFkb25lc2UiLCJleHAiOjE1OTg3MzkyMzAsImlzcyI6Im5vZWJzIn0.bmq95t9TDQnsma4aaQXvrHpUea6P9hb-TK2qKirFyCI", "test", "test1"},

	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := jj.VerifyJWT(tt.key)
			if err != nil {
				t.Fatalf("Error is: %v", err)
				
			}
			if !reflect.DeepEqual(got.Username, tt.want) {
				log.Printf("The key is: %v", jj.Key)
				t.Errorf("VerifyJWT() got = %v, want = %v", got.Username, tt.want)
			}
		})
	}
}

func Test_generateClaims(t *testing.T) {
	type args struct {
		iat    int64
		eat    int64
		issuer string
	}
	n := time.Now().Unix()
	n3h := time.Now().Add(3 * time.Hour).Unix()
	have := args{n, n3h, "noebs"}
	want := jwt.StandardClaims{
		ExpiresAt: n3h,
		IssuedAt:  n,
		Issuer:    "noebs",
	}
	tests := []struct {
		name string
		args args
		want jwt.StandardClaims
	}{
		{"normal test", have, want},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := generateClaims(tt.args.iat, tt.args.eat, tt.args.issuer); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("generateClaims() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_TimeOut(t *testing.T) {
	type args struct {
		name  string
		key   []byte
		token TokenClaims
	}

	n := time.Now().Add(-10 * time.Hour).Unix()
	eat1 := time.Now().Add(3 * time.Minute).Unix()
	t1 := generateClaims(n, eat1, "noebs")
	nToken1 := TokenClaims{
		Username:       "noebs",
		StandardClaims: t1,
	}

	j := &JWTAuth{}
	tk1, _ := j.GenerateJWTWithClaim("noebs", nToken1)

	tests := []struct {
		name string
		args string
		want TokenClaims
	}{
		{"normal test", tk1, nToken1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := j.VerifyJWT(tt.args)
			if err != nil {
				t.Errorf("there's an error: %v", err)
			}
			if !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("want: %v -- have: %v", tt.want, *got)

			}
		})
	}
}

func Test_verifyWithClaim1(t *testing.T) {
	type args struct {
		name  string
		key   []byte
		token TokenClaims
	}

	n := time.Now().Add(-10 * time.Hour).Unix()
	eat1 := time.Now().Add(3 * time.Minute).Unix()
	t1 := generateClaims(n, eat1, "noebs")
	nToken1 := TokenClaims{
		Username:       "noebs",
		StandardClaims: t1,
	}

	tk1, _ := jj.GenerateJWTWithClaim("noebs", nToken1)

	tests := []struct {
		name string
		args string
		want TokenClaims
	}{
		{"normal test", tk1, nToken1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := jj.verifyWithClaim(tt.args)
			if err != nil {
				t.Errorf("there's an error: %v", err)
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
