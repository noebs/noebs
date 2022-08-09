// Package gateway implments various auth logic used across noebs services
package gateway

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v7"
	"github.com/golang-jwt/jwt"
	log "github.com/sirupsen/logrus"
)

var apiKey = make([]byte, 16)

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
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Token has expired", "code": "jwt_expired"})
				return
			} else {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": "Malformed token", "code": "jwt_malformed"})
				return
			}
		} else if err == nil {
			// FIXME it is better to let the endpoint explicitly Get the claim off the user
			//  as we will assume the auth server will reside in a different domain!
			c.Set("mobile", claims.Mobile)
			log.Printf("the username is: %s", claims.Mobile)
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
