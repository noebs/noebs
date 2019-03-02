package main

import (
	"fmt"
	"github.com/dgrijalva/jwt-go"
	"time"
)

func generateJWT(secretKey []byte, serviceID string) (string, error) {
	// Create a new token object, specifying signing method and the claims
	// you would like it to contain.
	expiresAt := time.Now().Add(time.Hour * 24).UTC().Unix()

	claims := TokenClaims{
		serviceID,
		jwt.StandardClaims{
			ExpiresAt: expiresAt,
			Issuer:    "noebs",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	// Sign and get the complete encoded token as a string using the secret
	if tokenString, err := token.SignedString(secretKey); err == nil {
		fmt.Println(tokenString)
		return tokenString, nil
	} else {
		return "", err
	}
}

func verifyJWT(tokenString string) (bool, error) {
	// Parse takes the token string and a function for looking up the key. The latter is especially
	// useful if you use multiple keys for your application.  The standard is to use 'kid' in the
	// head of the token to identify which key to use, but the parsed token (head and claims) is provided
	// to the callback, providing flexibility.
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		// Don't forget to validate the alg is what you expect:
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		} else {
			return token, nil
		}
	})

	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		fmt.Println(claims["service_name"], claims["email"])

		// maybe inject onto database here...
		return true, nil

	} else if ve, ok := err.(*jwt.ValidationError); ok {
		if ve.Errors&jwt.ValidationErrorMalformed != 0 {
			return false, fmt.Errorf("not a valid token")

		} else if ve.Errors&(jwt.ValidationErrorExpired|jwt.ValidationErrorNotValidYet) != 0 {
			// Token is either expired or not active yet
			return false, fmt.Errorf("token timed out")

		} else {
			return false, fmt.Errorf("not a valid token: %v", err)

		}

	}
	return false, nil
}

type TokenClaims struct {
	ServiceName string `json:"service_name"`
	jwt.StandardClaims
}
