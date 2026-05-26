package gateway

import (
	"errors"
	"reflect"
	"testing"

	log "github.com/sirupsen/logrus"
)

// var key = []byte("abcdef012345678")
var jj = &JWTAuth{}

func TestVerifyJWT(t *testing.T) {
	jj.Key = []byte("test-key")
	token, err := jj.GenerateJWT(42, "0990000000", "tenant")
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

func TestJWTAuth_GenerateJWT_MissingTenantID(t *testing.T) {
	j := &JWTAuth{Key: []byte("test-key")}
	_, err := j.GenerateJWT(42, "0990000000", "")
	if !errors.Is(err, ErrMissingTenantID) {
		t.Fatalf("expected ErrMissingTenantID, got %v", err)
	}
}

func TestJWTAuth_GenerateJWT_RejectsDefaultTenantID(t *testing.T) {
	j := &JWTAuth{Key: []byte("test-key")}
	_, err := j.GenerateJWT(42, "0990000000", "default")
	if !errors.Is(err, ErrInvalidTenantID) {
		t.Fatalf("expected ErrInvalidTenantID, got %v", err)
	}
}

func TestJWTAuth_VerifyJWT_MissingKey(t *testing.T) {
	j := &JWTAuth{}
	_, err := j.VerifyJWT("token")
	if !errors.Is(err, ErrMissingJWTKey) {
		t.Fatalf("expected ErrMissingJWTKey, got %v", err)
	}
}
