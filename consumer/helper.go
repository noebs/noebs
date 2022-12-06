// Package consumer contains all of apis regarding EBS Consumer Web services
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
	"errors"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
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

// userHasSessions is a handy function we can use to check if a user has any active sessions e.g., esp
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
		//	"code": "You are logging from another device. You can only have one valid session"})
		//return
		log.Print("The user is logging from a different location")
		return true
	}
	return false
}

// userExceedMaxSessions keep track of many login-attempts a user has made
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

func (s *Service) ToDatabasename(url string) string {
	data := map[string]string{
		// url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardInfo
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerIsAliveEndpoint:         "alive",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerWorkingKeyEndpoint:      "public_key",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBalanceEndpoint:         "balance",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillInquiryEndpoint:     "bill_inquiry",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBillPaymentEndpoint:     "bill_payment",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardTransferEndpoint:    "card_transfer",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerAccountTransferEndpoint: "account_transfer",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPayeesListEndpoint:      "payees_list",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerChangeIPinEndpoint:      "change_ipin",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPurchaseEndpoint:        "purchase",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerStatusEndpoint:          "status",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRPaymentEndpoint:       "qr_purchase", // the fuck is wrong with you guys
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerQRRefundEndpoint:        "qr_refund",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerPANFromMobile:           "msisdn_pan",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCardInfo:                "customer_info",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerGenerateVoucher:         "generate_voucher",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashInEndpoint:          "cashin",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCashOutEndpoint:         "cashout",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerComplete:                "complete_tran",
		s.NoebsConfig.ConsumerIP + ebs_fields.IPinGeneration:                  "generate_ipin",
		s.NoebsConfig.ConsumerIP + ebs_fields.IPinCompletion:                  "ipin_completion",
		s.NoebsConfig.ConsumerIP + ebs_fields.MerchantTransactionStatus:       "merchant_status",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerRegister:                "register",
		s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerCompleteRegistration:    "complete_card_issuance",
	}
	return data[url]
}

var tranData = make(chan PushData)

func (s *Service) Pusher() {
	for {
		select {
		case data := <-tranData:
			
			if data.Phone != "" { // Telecom operation
				user, err := ebs_fields.GetUserByMobile(data.Phone, s.Db)
				if err != nil {
					// not a tutipay user
					utils.SendSMS(&s.NoebsConfig, utils.SMS{Mobile: data.Phone, Message: data.Body})
				} else {
					data.To = user.DeviceID
					s.Db.Create(&data)
					s.SendPush(data)
				}
			} else {
				// Read the pan from the payload
				user, err := ebs_fields.GetUserByCard(data.EBSData.PAN, s.Db)
				if err != nil {
					s.Logger.Printf("error in Pusher service: %s", err)
				} else {
					data.To = user.DeviceID
					// Store to database first
					s.Db.Create(&data)
					s.SendPush(data)
				}
			}
		}
	}

}
