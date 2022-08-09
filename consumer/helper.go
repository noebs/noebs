//Package consumer contains all of apis regarding EBS Consumer Web services
// the package is structured in such a way that separetes between the payment apis
// and the [services] apis.
//
// Payment APIs
// All of the payment apis are in [payment_apis.go] file, they include basically all of
// EBS Consumer web service docs [v3.0.0].
//
// Helper APIs
// We also have help apis in [services.go]
package consumer

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-redis/redis/v7"
	"github.com/pquerna/otp/totp"
)

var (
	serverError       = errors.New("unable to connect to the DB")
	ErrCreateDbRow    = errors.New("unable to create a new db row/column")
	errNoServiceID    = errors.New("empty Service ID was entered")
	errObjectNotFound = errors.New("object not found")
)

func isMember(key, val string, r *redis.Client) bool {
	b, _ := r.SIsMember(key, val).Result()
	return b
}

// validatePassword to include at least one capital letter, one symbol and one number
// and that it is at least 8 characters long
func validatePassword(password string) bool {
	if len(password) < 8 {
		return false
	}
	var hasUpper, hasSymbol, hasNumber bool
	// check if password contains @, &, #, $, %, ^, *, (, ), _, -, +, =, !, ?, ., /, <, >, [, ], {, }, |, \, ;, :, "
	if strings.ContainsAny(password, "@&#$%^*()_-+=!.?/<>[]:{}|\\;:\"") {
		hasSymbol = true
	}
	for _, c := range password {
		if unicode.IsUpper(c) {
			hasUpper = true
		}
		if unicode.IsSymbol(c) {
			hasSymbol = true
		}

		if unicode.IsNumber(c) {
			hasNumber = true
		}
	}
	return hasUpper && hasSymbol && hasNumber
}

//userHasSessions is a handy function we can use to check if a user has any active sessions e.g., esp
// when we don't want our users to use our app in new devices.
// we have to implement a way to:
// - identify user's devices
// - allow them to use the app in other devices in case their original device was lost
func userHasSessions(s *Service, username string) bool {
	// Make sure the user doesn't have any active sessions!
	lCount, err := s.Redis.Get(username + ":logged_in_devices").Result()

	num, _ := strconv.Atoi(lCount)
	// Allow for the user to be logged in -- add allowance through someway
	if err != redis.Nil && num > 1 {
		// The user is already logged in somewhere else. Communicate that to them, clearly!
		//c.JSON(http.StatusBadRequest, gin.H{"code": "user_logged_elsewhere",
		//	"error": "You are logging from another device. You can only have one valid session"})
		//return
		log.Print("The user is logging from a different location")
		return true
	}
	return false
}

//userExceedMaxSessions keep track of many login-attempts a user has made
func userExceededMaxSessions(s *Service, username string) bool {
	// make sure number of failed logged_in counts doesn't exceed the allowed threshold.
	res, err := s.Redis.Get(username + ":login_counts").Result()
	if err == redis.Nil {
		// the has just logged in
		s.Redis.Set(username+":login_counts", 0, time.Hour)
	} else if err == nil {
		count, _ := strconv.Atoi(res)
		if count >= 5 {
			// Allow users to use another login method (e.g., totp, or they should reset their password)
			// Lock their account
			//s.Redis.HSet(username, "suspecious_behavior", 1)
			s.Redis.HIncrBy(username, "suspicious_behavior", 1)
			ttl, _ := s.Redis.TTL(username + ":login_counts").Result()
			log.Printf("user exceeded max sessions %v", ttl)
			return true
		}
	}
	return false
}

// storeCards accepts either ebs_fields.CardRedis or []ebs_fields.CardRedis
// and depending on the input, it stores it in Redis
func (s *Service) storeCards(cards any, username string, edit bool) error {
	var err error

	if username == "" {
		return errors.New("unauthorized_access")
	}

	switch fields := cards.(type) {
	case ebs_fields.CardsRedis:
		fields.NewExpDate = ""
		fields.NewName = ""
		fields.NewPan = ""
		fields.ID = 0
		buf, _ := json.Marshal(&fields)
		s.store(buf, username, edit)
	case []ebs_fields.CardsRedis:
		for _, card := range fields {
			card.NewExpDate = ""
			card.NewName = ""
			card.NewPan = ""
			card.ID = 0
			buf, _ := json.Marshal(&card)
			log.Printf("the current card is: %v", card)
			s.store(buf, username, edit)
		}
	}
	if err != nil {
		return err
	}
	return nil
}

func (s *Service) store(buf []byte, username string, edit bool) error {
	z := &redis.Z{
		Member: buf,
	}
	if edit {
		_, err := s.Redis.ZAdd(username+":cards", z).Result()
		if err != nil {
			return err
		}
	} else {
		_, err := s.Redis.ZAdd(username+":cards", z).Result()
		if err != nil {
			return err
		}
	}
	return nil
}

func generateOtp(secret string) (string, error) {
	passcode, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		return "", err
	}
	return passcode, nil
}
