package gateway

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/golang-jwt/jwt/v5"
)

// JWTAuth provides an encapsulation for jwt auth
type JWTAuth struct {
	Key         []byte
	NoebsConfig ebs_fields.NoebsConfig
}

// Init initializes jwt auth
func (j *JWTAuth) Init() {
	log.Printf("jwt_key: %s", j.NoebsConfig.JWTKey)
	j.Key = []byte(j.NoebsConfig.JWTKey)
}

// GenerateJWT generates a JWT standard token with default values hardcoded. FIXME
func (j *JWTAuth) GenerateJWT(userID int64, mobile, tenantID string) (string, error) {
	// Create a new token object, specifying signing method and the claims
	// you would like it to contain.
	expiresAt := time.Now().Add(10 * time.Hour).UTC()
	if tenantID == "" {
		tenantID = "default"
	}
	claims := TokenClaims{
		UserID:   userID,
		Mobile:   mobile,
		TenantID: tenantID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "noebs",
			Subject:   fmt.Sprintf("%d", userID),
			IssuedAt:  jwt.NewNumericDate(time.Now().UTC()),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	log.Println("generating token")
	// Sign and get the complete encoded token as a string using the secret
	if j.Key == nil {
		return "", errors.New("empty jwt key")
	}
	log.Printf("jwt_key: %s", j.Key)
	if tokenString, err := token.SignedString(j.Key); err == nil {
		return tokenString, nil
	} else {
		return "", err
	}
}

// VerifyJWT giving a jwt token and a secret it validates the token against a hard coded TokenClaims struct
func (j *JWTAuth) VerifyJWT(tokenString string) (*TokenClaims, error) {
	parser := jwt.NewParser(jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	token, err := parser.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		return j.Key, nil
	})
	if token == nil {
		log.Println(err)
		return nil, err
	}

	claims, ok := token.Claims.(*TokenClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}
	if err != nil {
		return claims, err
	}
	if !token.Valid {
		return claims, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

// TokenClaims noebs standard claim
type TokenClaims struct {
	UserID   int64  `json:"uid"`
	Mobile   string `json:"mobile,omitempty"`
	TenantID string `json:"tenant_id,omitempty"`
	jwt.RegisteredClaims
}
