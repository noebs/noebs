package gateway

import (
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"time"
)

func VerifyJWT(tokenString string, secret []byte) (*TokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("Unexpected signing method: %v", token.Header["alg"])
		}

		// hmacSampleSecret is a []byte containing your secret, e.g. []byte("my_secret_key")
		return secret, nil
	})

	// a user might had submitted a non-jwt token
	if err != nil {
		return nil, err

	}

	if claims, ok := token.Claims.(*TokenClaims); ok && token.Valid {
		return claims, nil

	} else {
		return nil, err
	}
}

func GenerateJWT(serviceID string, secret []byte) (string, error) {
	// Create a new token object, specifying signing method and the claims
	// you would like it to contain.
	expiresAt := time.Now().Add(time.Hour * 1000).UTC().Unix()

	claims := TokenClaims{
		serviceID,
		jwt.StandardClaims{
			ExpiresAt: expiresAt,
			Issuer:    "noebs",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Sign and get the complete encoded token as a string using the secret
	if tokenString, err := token.SignedString(secret); err == nil {
		fmt.Println(tokenString)
		return tokenString, nil
	} else {
		return "", err
	}
}

type TokenClaims struct {
	Username string `json:"username"`
	jwt.StandardClaims
}
