package consumer

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

type Auther interface {
	VerifyJWT(token string) (*gateway.TokenClaims, error)
	GenerateJWT(userID int64, mobile, tenantID string) (string, error)
}

// GenerateAPIKey creates an API key for a given email (admin-only at the HTTP layer).
func (s *Service) GenerateAPIKey(ctx context.Context, tenantID, email string) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", err
	}
	email = strings.TrimSpace(email)
	if email == "" {
		return "", errors.New("missing email")
	}
	k, err := gateway.GenerateAPIKey()
	if err != nil {
		return "", err
	}
	if err := s.Store.CreateAPIKey(ctx, tenantID, email, k); err != nil {
		return "", err
	}
	return k, nil
}

func (s *Service) Login(ctx context.Context, tenantID, emailOrMobile, password string) (string, ebs_fields.User, error) {
	var empty ebs_fields.User
	if s == nil || s.Store == nil {
		return "", empty, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", empty, err
	}
	emailOrMobile = strings.TrimSpace(emailOrMobile)
	if emailOrMobile == "" {
		return "", empty, errors.New("missing mobile/email")
	}
	u, err := s.Store.GetUserByEmailOrMobile(ctx, tenantID, emailOrMobile)
	if err != nil {
		return "", empty, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte(password)); err != nil {
		return "", empty, ErrWrongPassword
	}
	token, err := s.Auth.GenerateJWT(u.ID, u.Mobile, tenantID)
	if err != nil {
		return "", empty, err
	}
	return token, sanitizeUser(*u), nil
}

func (s *Service) SingleLogin(ctx context.Context, tenantID string, req gateway.Token) (string, ebs_fields.User, error) {
	var empty ebs_fields.User
	if s == nil || s.Store == nil {
		return "", empty, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", empty, err
	}
	mobile := strings.TrimSpace(req.Mobile)
	if mobile == "" {
		return "", empty, errors.New("missing mobile")
	}

	u, err := s.Store.GetUserByUsernameEmailOrMobile(ctx, tenantID, mobile)
	if err != nil {
		return "", empty, err
	}

	if err := verifyUserSignature(u.PublicKey, req.Signature, req.Message); err != nil {
		return "", empty, err
	}
	if !totp.Validate(req.Message, u.EncodePublickey32()) {
		return "", empty, ErrWrongOTP
	}

	token, err := s.Auth.GenerateJWT(u.ID, u.Mobile, tenantID)
	if err != nil {
		return "", empty, err
	}
	return token, sanitizeUser(*u), nil
}

// RefreshJWT generates a new access token using the provided JWT + signature.
func (s *Service) RefreshJWT(ctx context.Context, req gateway.Token) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	claims, err := s.Auth.VerifyJWT(req.JWT)
	if err != nil {
		if !errors.Is(err, jwt.ErrTokenExpired) || claims == nil {
			return "", err
		}
	}
	tenantID, err := store.ValidateTenantID(claims.TenantID)
	if err != nil {
		return "", err
	}

	var user *ebs_fields.User
	if claims.UserID != 0 {
		user, err = s.Store.FindUserByID(ctx, tenantID, claims.UserID)
	} else {
		user, err = s.Store.GetUserByMobile(ctx, tenantID, claims.Mobile)
	}
	if err != nil {
		return "", err
	}
	if err := verifyUserSignature(user.PublicKey, req.Signature, req.Message); err != nil {
		return "", err
	}
	return s.Auth.GenerateJWT(user.ID, user.Mobile, tenantID)
}

func verifyUserSignature(publicKey, signature, message string) error {
	publicKey = strings.TrimSpace(publicKey)
	signature = strings.TrimSpace(signature)
	if publicKey == "" || signature == "" || strings.TrimSpace(message) == "" {
		return ErrInvalidSignature
	}
	block, _ := pem.Decode([]byte("-----BEGIN PUBLIC KEY-----\n" + publicKey + "\n-----END PUBLIC KEY-----"))
	if block == nil || block.Type != "PUBLIC KEY" {
		return ErrInvalidSignature
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	rsaPublicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return ErrInvalidSignature
	}
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	digest := sha256.Sum256([]byte(message))
	if err := rsa.VerifyPKCS1v15(rsaPublicKey, crypto.SHA256, digest[:], signatureBytes); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSignature, err)
	}
	return nil
}

func (s *Service) CreateUser(ctx context.Context, tenantID string, u ebs_fields.User) (ebs_fields.User, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.User{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.User{}, err
	}
	u.Mobile = strings.TrimSpace(u.Mobile)
	if u.Mobile == "" {
		return ebs_fields.User{}, ErrMissingMobile
	}
	u.Username = strings.TrimSpace(u.Username)
	u.Email = strings.TrimSpace(u.Email)

	// Make sure user is unique
	if _, err := s.Store.GetUserByMobile(ctx, tenantID, u.Mobile); err == nil {
		return ebs_fields.User{}, errors.New("mobile already exists")
	}
	// Make sure username is unique
	if u.Username != "" {
		if _, err := s.Store.FindUserByUsername(ctx, tenantID, u.Username); err == nil {
			return ebs_fields.User{}, errors.New("username already exists")
		}
	} else {
		u.Username = u.Mobile
	}

	if !validatePassword(u.Password) {
		return ebs_fields.User{}, ErrPasswordInvalid
	}
	if err := u.HashPassword(); err != nil {
		return ebs_fields.User{}, err
	}
	if err := s.Store.CreateUser(ctx, tenantID, &u); err != nil {
		return ebs_fields.User{}, err
	}
	return sanitizeUser(u), nil
}

func (s *Service) VerifyOTP(ctx context.Context, tenantID, mobile, otp string) (ebs_fields.User, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.User{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.User{}, err
	}
	mobile = strings.ToLower(strings.TrimSpace(mobile))
	if mobile == "" || strings.TrimSpace(otp) == "" {
		return ebs_fields.User{}, ErrEmptyOTP
	}

	u, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return ebs_fields.User{}, err
	}
	if !u.VerifyOtp(otp) {
		if err := s.Store.IncrementSuspicious(ctx, tenantID, mobile); err != nil {
			return ebs_fields.User{}, err
		}
		return ebs_fields.User{}, ErrInvalidOTP
	}
	if err := s.Store.UpdateUserColumns(ctx, tenantID, u.ID, map[string]any{"is_password_otp": true, "is_verified": true}); err != nil {
		return ebs_fields.User{}, err
	}
	u.IsPasswordOTP = true
	u.IsVerified = true
	return sanitizeUser(*u), nil
}

func (s *Service) ChangePassword(ctx context.Context, tenantID, mobile, newPassword string) (ebs_fields.User, error) {
	if s == nil || s.Store == nil {
		return ebs_fields.User{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return ebs_fields.User{}, err
	}
	mobile = strings.ToLower(strings.TrimSpace(mobile))
	if mobile == "" {
		return ebs_fields.User{}, ErrMissingMobile
	}
	if strings.TrimSpace(newPassword) == "" {
		return ebs_fields.User{}, errors.New("missing new password")
	}

	u, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return ebs_fields.User{}, err
	}
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), 8)
	if err != nil {
		return ebs_fields.User{}, err
	}
	if err := s.Store.UpdateUserPassword(ctx, tenantID, u.ID, string(hashedPassword)); err != nil {
		return ebs_fields.User{}, err
	}
	return sanitizeUser(*u), nil
}

func (s *Service) GenerateSignInCode(ctx context.Context, tenantID, mobile string) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.TrimSpace(mobile)
	if mobile == "" {
		return ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	if _, err := s.Store.RecordLoginAttempt(ctx, tenantID, mobile, true); err != nil {
		return err
	}
	key, err := user.GenerateOtp()
	if err != nil {
		return err
	}
	if err := utils.SendSMS(&s.NoebsConfig, utils.SMS{
		Mobile:  mobile,
		Message: fmt.Sprintf("Your one-time access code is: %s. DON'T share it with anyone.", key),
	}); err != nil {
		return err
	}
	return nil
}
