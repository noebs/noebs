// Package gateway implments various auth logic used across noebs services
package gateway

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt"
	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

var apiKey = make([]byte, 16)

// AuthMiddleware is a JWT authorization middleware. It is used in our consumer services
// to get a username from the payload (maybe change it to mobile number at somepoint)
func (a *JWTAuth) AuthMiddleware() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// just handle the simplest case, authorization is not provided.
		h := c.Get("Authorization")
		if h == "" {
			return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "empty header was sent", "code": "unauthorized"})
		}
		claims, err := a.VerifyJWT(h)
		log.Printf("They key is: %v", a.Key)
		if e, ok := err.(*jwt.ValidationError); ok {
			if e.Errors&jwt.ValidationErrorExpired != 0 {
				// in this case you might need to give it another spin
				return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "Token has expired", "code": "jwt_expired"})
			} else {
				return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"message": "Malformed token", "code": "jwt_malformed"})
			}
		} else if err == nil {
			// FIXME it is better to let the endpoint explicitly Get the claim off the user
			//  as we will assume the auth server will reside in a different domain!
			c.Locals("user_id", claims.UserID)
			if isValidMobile(claims.Mobile) {
				c.Locals("mobile", claims.Mobile)
				c.Locals("username", claims.Mobile)
			}
			log.Printf("the username is: %s", claims.Mobile)
			return c.Next()
		}
		return nil
	}

}

// GenerateSecretKey generates secret key for jwt signing
func GenerateSecretKey(n int) ([]byte, error) {
	key := make([]byte, n)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// NoebsCors reads from noebs config to setup cors headers for the server
func NoebsCors(headers []string) fiber.Handler {
	cors := strings.Join(headers, ",")
	return func(c *fiber.Ctx) error {
		if c.Method() != fiber.MethodOptions {
			c.Set("Access-Control-Allow-Origin", cors)
			return c.Next()
		} else {
			c.Set("Access-Control-Allow-Origin", cors)
			c.Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Set("Access-Control-Allow-Headers", "authorization, origin, content-type, accept, X-CSRF-TOKEN")
			c.Set("Allow", "HEAD,GET,POST,PUT,PATCH,DELETE,OPTIONS")
			c.Set("Content-Type", "application/json")
			return c.SendStatus(http.StatusOK)
		}
	}
}

func GenerateAPIKey() (string, error) {
	_, err := rand.Read(apiKey)
	a := fmt.Sprintf("%x", apiKey)
	return a, err
}

func isMember(ctx context.Context, key, val string, r *redis.Client) bool {
	b, _ := r.SIsMember(ctx, key, val).Result()
	return b
}

func getMap(ctx context.Context, key, val string, r *redis.Client) (bool, error) {
	res, err := r.HGet(ctx, "apikeys", key).Result()
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

var mobileRegex = regexp.MustCompile(`^[0-9]{10}$`)

func isValidMobile(mobile string) bool {
	return mobileRegex.MatchString(mobile)
}
