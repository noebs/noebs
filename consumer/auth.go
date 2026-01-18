package consumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"firebase.google.com/go/v4/messaging"
	noebsCrypto "github.com/adonese/crypto"
	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/utils"
	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt"
	"github.com/pquerna/otp/totp"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Auther interface {
	VerifyJWT(token string) (*gateway.TokenClaims, error)
	GenerateJWT(userID uint, mobile string) (string, error)
}

// GenerateAPIKey An Admin-only endpoint that is used to generate api key for our clients
// the user must submit their email to generate a unique token per email.
// FIXME #59 #58 #61 api generation should be decoupled from apigateway package
func (s *Service) GenerateAPIKey(c *fiber.Ctx) error {
	var m map[string]string
	if err := parseJSON(c, &m); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "bad_request"})
	}
	if _, ok := m["email"]; ok {
		ctx := c.UserContext()
		k, _ := gateway.GenerateAPIKey()
		s.Redis.SAdd(ctx, "apikeys", k)
		return c.Status(http.StatusOK).JSON(fiber.Map{"result": k})
	}
	return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "missing_field"})
}

// ApiKeyMiddleware used to authenticate clients using X-Email and X-API-Key headers
// FIXME issue #58 #61
func (s *Service) ApiKeyMiddleware(c *fiber.Ctx) error {
	email := c.Get("X-Email")
	key := c.Get("X-API-Key")
	if email == "" || key == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "unauthorized"})
	}
	ctx := c.UserContext()
	res, err := s.Redis.HGet(ctx, "api_keys", email).Result()
	if err != redis.Nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "unauthorized"})
	}
	if key == res {
		return c.Next()
	} else {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "unauthorized"})
	}
}

// FIXME issue #58 #61
func (s *Service) IpFilterMiddleware(c *fiber.Ctx) error {
	ip := c.IP()
	if u := getMobile(c); u != "" {
		ctx := c.UserContext()
		s.Redis.HIncrBy(ctx, u+":ips_count", ip, 1)
		return c.Next()
	} else {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "unauthorized_access"})
	}
}

// LoginHandler noebs signin page
func (s *Service) LoginHandler(c *fiber.Ctx) error {
	var req ebs_fields.User
	if err := bindJSON(c, &req); err != nil {
		// The request is wrong
		s.Logger.Printf("The request is wrong. %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	s.Logger.Printf("the processed request is: %+v\n", req)
	u := ebs_fields.User{}
	if notFound := s.Db.Where("email = ? or mobile = ?", strings.ToLower(req.Mobile), strings.ToLower(req.Mobile)).First(&u).Error; errors.Is(notFound, gorm.ErrRecordNotFound) {
		// service id is not found
		s.Logger.Printf("User with service_id %s is not found.", req.Mobile)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": notFound.Error(), "code": "not_found"})
	}
	if !u.IsVerified {
		// user has not verified their phone number with OTP
		s.Logger.Printf("User with service_id %s is not verified.", req.Mobile)
		// c.JSON(http.StatusUnauthorized, gin.H{"code": "unauthorized_access", "message": "verify phone number with OTP"})
		// return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(req.Password)); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "wrong password entered", "code": "wrong_password"})
	}

	token, err := s.Auth.GenerateJWT(u.ID, u.Mobile)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(err)
	}
	c.Set("Authorization", token)
	return c.Status(http.StatusOK).JSON(fiber.Map{"authorization": token, "user": u})
}

// SingleLoginHandler is used for one-time authentications. It checks a signed entered otp keys against
// the user's credentials (user's stored public key)
//
// NOTES
// This function only allows one-time authentication VIA the same device that the user originally has signed up with.
func (s *Service) SingleLoginHandler(c *fiber.Ctx) error {

	var req gateway.Token
	_ = parseJSON(c, &req)
	s.Logger.Printf("the processed request is: %v\n", req)

	u := ebs_fields.User{}
	if notFound := s.Db.Where("username = ? or email = ? or mobile = ?", strings.ToLower(req.Mobile), strings.ToLower(req.Mobile), strings.ToLower(req.Mobile)).First(&u).Error; errors.Is(notFound, gorm.ErrRecordNotFound) {
		s.Logger.Printf("User with service_id %s is not found.", req.Mobile)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": notFound.Error(), "code": "not_found"})
	}

	if _, encErr := noebsCrypto.VerifyWithHeaders(u.PublicKey, req.Signature, req.Message); encErr != nil {
		s.Logger.Printf("invalid signature in refresh: %v", encErr)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": encErr.Error(), "code": "bad_request"})
	}

	// Validate the otp using user's stored public key
	if totp.Validate(req.Message, u.EncodePublickey32()) == false {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "wrong otp entered", "code": "wrong_otp"})
	}
	token, err := s.Auth.GenerateJWT(u.ID, u.Mobile)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(err)
	}
	c.Set("Authorization", token)
	return c.Status(http.StatusOK).JSON(fiber.Map{"authorization": token, "user": u})
}

// RefreshHandler generates a new access token to the user using
// their signed public key.
// the user will sign their username with their private key, and noebs will verify
// the signature using the stored public key for the user
func (s *Service) RefreshHandler(c *fiber.Ctx) error {
	var req gateway.Token
	if err := bindJSON(c, &req); err != nil {
		// The request is wrong
		s.Logger.Printf("The request is wrong. %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	claims, err := s.Auth.VerifyJWT(req.JWT)
	if e, ok := err.(*jwt.ValidationError); ok {
		if e.Errors&jwt.ValidationErrorExpired != 0 {
			s.Logger.Info("refresh: auth username is: ", claims.Mobile)
			var user ebs_fields.User
			if claims.UserID != 0 {
				if err := s.Db.First(&user, claims.UserID).Error; err != nil {
					user, _ = ebs_fields.GetUserByMobile(claims.Mobile, s.Db)
				}
			} else {
				user, _ = ebs_fields.GetUserByMobile(claims.Mobile, s.Db)
			}
			// should verify signature here...
			if user.PublicKey == "" {
				s.Logger.Printf("user: %s has no registered pubkey", user.Mobile)
			}
			s.Logger.Printf("grabbed user is: %#v", user.Mobile)
			if _, encErr := noebsCrypto.VerifyWithHeaders(user.PublicKey, req.Signature, req.Message); encErr != nil {
				s.Logger.Printf("invalid signature in refresh: %v", encErr)
				return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": encErr.Error(), "code": "bad_request"})
			}
			auth, _ := s.Auth.GenerateJWT(user.ID, user.Mobile)
			c.Set("Authorization", auth)
			return c.Status(http.StatusOK).JSON(fiber.Map{"authorization": auth})

		} else {
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "Malformed token", "code": "jwt_malformed"})
		}
	} else if err == nil {
		// FIXME it is better to let the endpoint explicitly Get the claim off the user
		//  as we will assume the auth server will reside in a different domain!
		s.Logger.Printf("the username is: %s", claims.Mobile)
		auth, _ := s.Auth.GenerateJWT(claims.UserID, claims.Mobile)
		c.Set("Authorization", auth)
		return c.Status(http.StatusOK).JSON(fiber.Map{"authorization": auth})
	}
	return nil
}

// CreateUser to register a new user to noebs
func (s *Service) CreateUser(c *fiber.Ctx) error {
	u := ebs_fields.User{}
	if err := bindJSON(c, &u); err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	// FIXME: Optimize these checks
	// Make sure user is unique
	var tmpUser ebs_fields.User
	if res := s.Db.Where("mobile = ?", u.Mobile).First(&tmpUser); res.Error == nil {
		// User already exists
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "User with this mobile number already exists"})
	}
	// Make sure username is unique
	if u.Username != "" {
		if res := s.Db.Where("username = ?", u.Username).First(&tmpUser); res.Error == nil {
			// User already exists
			return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "User with this username already exists"})
		}
	} else {
		// Currently we set the username to be the same as the mobile number
		u.Username = u.Mobile
	}

	// validate u.Password to include at least one capital letter, one symbol and one number
	// and that it is at least 8 characters long
	if !validatePassword(u.Password) {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "Password must be at least 8 characters long, and must include at least one capital letter, one symbol and one number", "code": "password_invalid"})
	}

	// make sure that the user doesn't exist in the database
	if err := u.HashPassword(); err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"message": err.Error()})
	}
	if err := s.Db.Create(&u).Error; err != nil {
		// unable to create this user; see possible reasons
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "duplicate_username"})
	}
	c.Status(http.StatusCreated).JSON(fiber.Map{"ok": "object was successfully created", "details": u})
	go gateway.SyncLedger(u)
	return nil
}

func (s *Service) VerifyOTP(c *fiber.Ctx) error {
	var req ebs_fields.User
	_ = parseJSON(c, &req)
	if req.OTP == "" || req.Mobile == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "otp was not sent", "code": "empty_otp"})
	}
	s.Logger.Printf("the processed request is: %v\n", req)
	u := ebs_fields.User{}
	if notFound := s.Db.Where("mobile = ?", strings.ToLower(req.Mobile)).First(&u).Error; errors.Is(notFound, gorm.ErrRecordNotFound) {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": notFound.Error(), "code": "not_found"})
	}

	// I think this one is buggy
	if valid := u.VerifyOtp(req.OTP); !valid {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "Invalid otp", "code": "invalid_otp"})
	}
	s.Db.Model(&req).Where("mobile = ?", req.Mobile).Update("is_password_otp", true)
	s.Db.Model(&req).Where("mobile = ?", req.Mobile).Update("is_verified", true)
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok", "user": u, "pubkey": s.NoebsConfig.EBSConsumerKey})
}

// BalanceStep part of our 2fa steps for account recovery
func (s *Service) BalanceStep(c *fiber.Ctx) error {
	// FIXME(adonese): we need to check for `is_password_otp` = true
	type data struct {
		ebs_fields.ConsumerBalanceFields
		Mobile string `json:"mobile,omitempty"`
	}
	url := s.NoebsConfig.ConsumerIP + ebs_fields.ConsumerBalanceEndpoint // EBS simulator endpoint url goes here.

	var req data
	_ = parseJSON(c, &req)
	user, _ := ebs_fields.NewUserWithCards(req.Mobile, s.Db)
	var isMatched bool
	if user.Cards == nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "no matching card was found", "code": "card_not_matched"})
	}
	for _, card := range user.Cards {
		if req.Pan == card.Pan {
			isMatched = true
			req.ExpDate = card.Expiry
		}
	}
	if !isMatched {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "no matching card was found", "code": "card_not_matched"})
	}

	// make transaction to ebs here
	mobile := req.Mobile
	req.Mobile = ""
	req.ApplicationId = s.NoebsConfig.ConsumerID
	jsonBuffer, err := json.Marshal(req)
	if err != nil {
		// there's an error in parsing the struct. Server error.
		er := ebs_fields.ErrorDetails{Details: nil, Code: http.StatusBadRequest, Message: "Unable to parse the request", Status: ebs_fields.ParsingError}
		return c.Status(http.StatusBadRequest).JSON(ebs_fields.ErrorResponse{ErrorDetails: er})
	}

	// the only part left is fixing EBS errors. Formalizing them per se.
	_, _, ebsErr := ebs_fields.EBSHttpClient(url, jsonBuffer)
	if ebsErr != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "Invalid credentials", "code": "transaction_failed"})
	}

	// generate a jwt here
	token, err := s.Auth.GenerateJWT(user.ID, mobile)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(err)
	}
	c.Set("Authorization", token)
	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok", "authorization": token})
}

// ChangePassword used to change a user's password using their old one
func (s *Service) ChangePassword(c *fiber.Ctx) error {
	mobile := getMobile(c)
	var req ebs_fields.User
	if err := bindJSON(c, &req); err != nil || req.NewPassword == "" {
		s.Logger.Printf("The request is wrong. %v", err)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "Bad request.", "code": "bad_request"})
	}
	s.Logger.Printf("the processed request is: %+v\n", req)
	u := ebs_fields.User{}
	if notFound := s.Db.Where("mobile = ?", strings.ToLower(mobile)).First(&u).Error; errors.Is(notFound, gorm.ErrRecordNotFound) {
		// service id is not found
		s.Logger.Printf("User with service_id %s is not found.", req.Mobile)
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": notFound.Error(), "code": "not_found"})
	}

	// Create and update the user's password
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 8)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(err)
	}
	if err := s.Db.Model(&ebs_fields.User{}).Where("mobile = ?", u.Mobile).Update("password", string(hashedPassword)).Error; err != nil {
		return c.Status(http.StatusInternalServerError).JSON(err)
	}

	return c.Status(http.StatusOK).JSON(fiber.Map{"result": "ok", "user": u})
}

// VerifyFirebase used to confirm that the user's token is valid
func (s *Service) VerifyFirebase(c *fiber.Ctx) error {
	var req ebs_fields.User
	if err := bindJSON(c, &req); err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	ctx := c.UserContext()
	fb, err := s.FirebaseApp.Auth(ctx)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"message": err.Error(), "code": "internal_error"})
	}
	token, err := fb.VerifyIDToken(ctx, req.FirebaseIDToken)
	if err != nil {
		return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"message": err.Error(), "code": "internal_error"})
	}
	s.Logger.Printf("Verified ID token: %v\n", token)
	return nil
}

func (s *Service) SendPush(ctx context.Context, data PushData) error {
	// Obtain a messaging.Client from the App.
	client, err := s.FirebaseApp.Messaging(ctx)
	if err != nil {
		s.Logger.Printf("error getting Messaging clients: %v\n", err)
	}
	// This registration token comes from the client FCM SDKs.
	registrationToken := data.To

	// This is the same struct as PushData, but due to firebase api
	// we had to make it in a map of string:any
	firebaseData := map[string]string{
		"type":           data.Type,
		"time":           fmt.Sprint(data),
		"uuid":           data.EBSData.UUID,
		"call_to_action": data.CallToAction,
	}

	if data.PaymentRequest != (ebs_fields.QrData{}) { // If you get an error in Payment Request notifications this may be the reason.
		if PaymentRequest, err := json.Marshal(data.PaymentRequest); err == nil {
			firebaseData["payment_request"] = string(PaymentRequest)
		} else {
			log.Printf("Error marshalling PaymentRequest: %v", err)
		}
	}

	// See documentation on defining a message payload.
	message := &messaging.Message{
		Token:        registrationToken,
		Notification: &messaging.Notification{Title: data.Title, Body: data.Body},
		Android:      &messaging.AndroidConfig{},
		Webpush:      &messaging.WebpushConfig{},
		APNS:         &messaging.APNSConfig{},
		FCMOptions:   &messaging.FCMOptions{},
		Topic:        "",
		Condition:    "",
		Data:         firebaseData,
	}

	// Send a message to the device corresponding to the provided
	// registration token.
	response, err := client.Send(ctx, message)
	if err != nil {
		s.Logger.Printf("the error is: %v", err)
		return err
	}
	// Response is a message ID string.
	log.Print(response)
	return nil
}

// GenerateSignInCode allows noebs users to access their accounts in case they forgotten their passwords
func (s *Service) GenerateSignInCode(c *fiber.Ctx, allowInsecure bool) error {
	var req gateway.Token
	_ = parseJSON(c, &req)
	s.Logger.Printf("the req is: %+v", req)
	// default username to mobile, in case username was not provided
	if req.Mobile == "" {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": "Mobile number was not sent", "code": "bad_request"})
	}
	user, _ := ebs_fields.GetUserByMobile(req.Mobile, s.Db)
	key, err := user.GenerateOtp()
	if err != nil {
		return c.Status(http.StatusBadRequest).JSON(fiber.Map{"message": err.Error(), "code": "bad_request"})
	}
	log.Printf("the key is: %s", key)
	// this function doesn't have to be blocking.
	go utils.SendSMS(&s.NoebsConfig, utils.SMS{Mobile: req.Mobile, Message: fmt.Sprintf("Your one-time access code is: %s. DON'T share it with anyone.", key)})
	return c.Status(http.StatusCreated).JSON(fiber.Map{"status": "ok", "message": "Password reset link has been sent to your mobile number. Use the info to login in to your account."})
}

// APIAuth API-Key middleware. Currently is used by consumer services
func (s *Service) APIAuth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		if key := c.Get("api-key"); key != "" {
			ctx := c.UserContext()
			if !isMember(ctx, "apikeys", key, s.Redis) {
				return c.Status(http.StatusUnauthorized).JSON(fiber.Map{"code": "wrong_api_key",
					"message": "visit https://soluspay.net/contact for a key"})
			}
		}
		return c.Next()
	}

}
