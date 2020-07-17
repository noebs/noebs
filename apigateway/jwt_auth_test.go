package gateway

import (
	"reflect"
	"testing"
	"time"

	"github.com/dgrijalva/jwt-go"
)

var key = []byte("abcdef012345678")
var jj = &JWTAuth{Key: key}

func TestVerifyJWT(t *testing.T) {


	
	j, _ := jj.GenerateJWT("test")

	tests := []struct {
		name string
		have string
		want string
	}{
		{"test_successful_retrieval", "test", "test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := jj.VerifyJWT(j)
			if !reflect.DeepEqual(got.Username, tt.want) {
				t.Errorf("VerifyJWT() got = %v, want %v", got, tt.want)
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
	tk1 := args{n, n3h, "noebs"}
	c1 := jwt.StandardClaims{
		ExpiresAt: n3h,
		IssuedAt:  n,
		Issuer:    "noebs",
	}
	tests := []struct {
		name string
		args args
		want jwt.StandardClaims
	}{
		{"normal test", tk1, c1},
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
