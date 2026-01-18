package gateway

import (
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-redis/redis/v7"
	"github.com/golang-jwt/jwt"
)

// JWTAuth provides an encapsulation for jwt auth
type JWTAuth struct {
	Key         []byte
	NoebsConfig ebs_fields.NoebsConfig
}

type GetRedisClient func(string) *redis.Client

// Init initializes jwt auth
func (j *JWTAuth) Init() {
	log.Printf("jwt_key: %s", j.NoebsConfig.JWTKey)
	j.Key = []byte(j.NoebsConfig.JWTKey)
}

// GenerateJWT generates a JWT standard token with default values hardcoded. FIXME
func (j *JWTAuth) GenerateJWT(userID uint, mobile string) (string, error) {
	// Create a new token object, specifying signing method and the claims
	// you would like it to contain.
	expiresAt := time.Now().Add(10 * time.Hour).UTC().Unix()
	claims := TokenClaims{
		userID,
		mobile,
		jwt.StandardClaims{
			ExpiresAt: expiresAt,
			Issuer:    "noebs",
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
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return j.Key, nil
	})
	if token == nil {
		log.Println(err)
		return nil, err
	}

	if claims, ok := token.Claims.(*TokenClaims); ok && token.Valid {
		log.Println("why am i here?")
		return claims, nil
	} else {
		return claims, err
	}
}

// verifyWithClaim deprecated it shouldn't be used.
func (j *JWTAuth) verifyWithClaim(tokenString string) error {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return j.Key, nil
	})

	if token.Valid {
		fmt.Println("You look nice today")
	} else if ve, ok := err.(*jwt.ValidationError); ok {
		if ve.Errors&jwt.ValidationErrorMalformed != 0 {
			return errors.New("That's not even a token")
		} else if ve.Errors&(jwt.ValidationErrorExpired|jwt.ValidationErrorNotValidYet) != 0 {
			// Token is either expired or not active yet
			return errors.New("Timing is everything")
		} else {
			return errors.New("Couldn't handle this token:")
		}
	} else {
		return errors.New("Couldn't handle this token")
	}
	return nil
}

// TokenClaims noebs standard claim
type TokenClaims struct {
	UserID uint   `json:"uid"`
	Mobile string `json:"mobile,omitempty"`
	jwt.StandardClaims
}

// secretFromClaims returns the claim's secret. in this case it is a user name
func (j *JWTAuth) secretFromClaims(token string, skipTime bool) (string, error) {
	claims, err := j.VerifyJWT(token)
	if e, ok := err.(*jwt.ValidationError); ok {
		if e.Errors&jwt.ValidationErrorExpired > 0 && skipTime {
			return claims.Mobile, nil
		} else {
			return "", errors.New("token is invalid")
		}
	} else {
		return "", errors.New("token is invalid")
	}
}
