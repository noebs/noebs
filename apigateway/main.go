// Package gateway implments various auth logic used across noebs services
package gateway

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/dgrijalva/jwt-go"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	log "github.com/sirupsen/logrus"
)

var apiKey = make([]byte, 16)

// var jwtKey = keyFromEnv()

//AuthMiddleware is a JWT authorization middleware. It is used in our consumer services
//to get a username from the payload (maybe change it to mobile number at somepoint)
func (a *JWTAuth) AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// just handle the simplest case, authorization is not provided.
		h := c.GetHeader("Authorization")
		if h == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "empty header was sent",
				"code": "unauthorized"})
			return
		}

		claims, err := a.VerifyJWT(h)
		log.Printf("They key is: %v", a.Key)
		if e, ok := err.(*jwt.ValidationError); ok {
			if e.Errors&jwt.ValidationErrorExpired != 0 {
				// in this case you might need to give it another spin
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Token has expired", "code": "jwt_expired"})
				return
				// allow for expired tokens to live...FIXME
				//c.Set("username", claims.Username)
				//c.Next()
			} else {
				//FIXME #66 it this code doesn't use the same key we have
				//jwt key is not initalized here
				c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Malformed token", "code": "jwt_malformed"})
				return
			}
		} else if err == nil {
			// FIXME it is better to let the endpoint explicitly Get the claim off the user
			//  as we will assume the auth server will reside in a different domain!

			c.Set("username", claims.Username)
			log.Printf("the username is: %s", claims.Username)
			c.Next()
		}
	}

}

//GenerateSecretKey generates secret key for jwt signing
func GenerateSecretKey(n int) ([]byte, error) {
	key := make([]byte, n)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// keyFromEnv either generates or retrieve a jwt which will be used to generate a secret key
//FIXME issue #61
func keyFromEnv(redisClient *redis.Client) []byte {
	// it either checks for environment for the specific key, or generates and saves a one
	if key := os.Getenv("noebs_jwt"); key != "" {
		return []byte(key)
	}

	if key := redisClient.Get("jwt").String(); key != "" {
		return []byte(key)
	}
	key, _ := GenerateSecretKey(50)
	redisClient.Set("jwt", key, 0)
	err := os.Setenv("noebs_jwt", string(key))
	log.Printf("the error in env is: %v", err)
	return key
}

//OptionsMiddleware for cors headers
func OptionsMiddleware(c *gin.Context) {
	if c.Request.Method != "OPTIONS" {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Next()
	} else {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Access-Control-Allow-Headers", "authorization, origin, content-type, accept, X-CSRF-TOKEN")
		c.Header("Allow", "HEAD,GET,POST,PUT,PATCH,DELETE,OPTIONS")
		c.Header("Content-Type", "application/json")
		c.AbortWithStatus(http.StatusOK)
	}
}

func GenerateAPIKey() (string, error) {
	_, err := rand.Read(apiKey)
	a := fmt.Sprintf("%x", apiKey)
	return a, err
}

func isMember(key, val string, r *redis.Client) bool {
	b, _ := r.SIsMember(key, val).Result()
	return b
}

func getMap(key, val string, r *redis.Client) (bool, error) {
	res, err := r.HGet("apikeys", key).Result()
	if err != nil {
		return false, err
	}
	if res != val {
		return false, errors.New("wrong_key")
	}

	return true, nil
}

var (
	serverError       = errors.New("unable to connect to the DB")
	ErrCreateDbRow    = errors.New("unable to create a new db row/column")
	errNoServiceID    = errors.New("empty Service ID was entered")
	errObjectNotFound = errors.New("object not found")
)
