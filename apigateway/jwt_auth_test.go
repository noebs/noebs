package gateway

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	if got.SessionEpoch != 1 {
		t.Errorf("VerifyJWT() session epoch = %d, want 1", got.SessionEpoch)
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

func TestJWTAuth_GenerateJWT_RejectsInvalidUserID(t *testing.T) {
	j := &JWTAuth{Key: []byte("test-key")}
	for _, userID := range []int64{0, -1} {
		_, err := j.GenerateJWT(userID, "0990000000", "tenant")
		if !errors.Is(err, ErrInvalidUserIdentity) {
			t.Fatalf("GenerateJWT(%d) error = %v, want %v", userID, err, ErrInvalidUserIdentity)
		}
	}
}

func TestJWTAuth_VerifyJWTRejectsInvalidUserIdentity(t *testing.T) {
	j := &JWTAuth{Key: []byte("test-key")}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, TokenClaims{TenantID: "tenant"})
	signed, err := token.SignedString(j.Key)
	if err != nil {
		t.Fatalf("SignedString() error = %v", err)
	}

	_, err = j.VerifyJWT(signed)
	if !errors.Is(err, ErrInvalidUserIdentity) {
		t.Fatalf("VerifyJWT() error = %v, want %v", err, ErrInvalidUserIdentity)
	}
}

func TestJWTAuth_VerifyJWT_MissingKey(t *testing.T) {
	j := &JWTAuth{}
	_, err := j.VerifyJWT("token")
	if !errors.Is(err, ErrMissingJWTKey) {
		t.Fatalf("expected ErrMissingJWTKey, got %v", err)
	}
}

func TestJWTAuthGenerateJWTUsesExplicitClockAndUniqueTokenID(t *testing.T) {
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	j := &JWTAuth{Key: []byte("test-key"), Now: func() time.Time { return now }}

	first, err := j.GenerateJWT(42, "0990000000", "tenant")
	if err != nil {
		t.Fatalf("first GenerateJWT(): %v", err)
	}
	second, err := j.GenerateJWT(42, "0990000000", "tenant")
	if err != nil {
		t.Fatalf("second GenerateJWT(): %v", err)
	}
	if first == second {
		t.Fatal("same-clock GenerateJWT() returned identical tokens")
	}
	firstClaims, err := j.VerifyJWT(first)
	if err != nil {
		t.Fatalf("VerifyJWT(first): %v", err)
	}
	secondClaims, err := j.VerifyJWT(second)
	if err != nil {
		t.Fatalf("VerifyJWT(second): %v", err)
	}
	if firstClaims.ID == "" || secondClaims.ID == "" || firstClaims.ID == secondClaims.ID {
		t.Fatalf("token IDs = %q and %q, want distinct non-empty values", firstClaims.ID, secondClaims.ID)
	}
	if !firstClaims.IssuedAt.Time.Equal(now) || !firstClaims.ExpiresAt.Time.Equal(now.Add(10*time.Hour)) {
		t.Fatalf("token times = issued %s expires %s", firstClaims.IssuedAt.Time, firstClaims.ExpiresAt.Time)
	}
}
