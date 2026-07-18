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
	"time"

	gateway "github.com/adonese/noebs/apigateway"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"github.com/golang-jwt/jwt/v5"
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

func (s *Service) Login(ctx context.Context, tenantID, emailOrMobile, password, source string, now time.Time) (string, ebs_fields.User, error) {
	var empty ebs_fields.User
	if s == nil || s.Store == nil {
		return "", empty, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", empty, err
	}
	emailOrMobile = strings.ToLower(strings.TrimSpace(emailOrMobile))
	if emailOrMobile == "" {
		return "", empty, errors.New("missing mobile/email")
	}
	source, err = normalizeRequestSource(source)
	if err != nil {
		return "", empty, err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		mobileLimit("password-login-identifier", emailOrMobile, 10, 15*time.Minute),
		sourceLimit("password-login-source", source, 30, 15*time.Minute),
	); err != nil {
		return "", empty, err
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

func (s *Service) SingleLogin(ctx context.Context, tenantID string, req gateway.Token, source string, now time.Time) (string, ebs_fields.User, error) {
	var empty ebs_fields.User
	if s == nil || s.Store == nil {
		return "", empty, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", empty, err
	}
	mobile := strings.ToLower(strings.TrimSpace(req.Mobile))
	if mobile == "" {
		return "", empty, errors.New("missing mobile")
	}
	source, err = normalizeRequestSource(source)
	if err != nil {
		return "", empty, err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		mobileLimit("otp-login-mobile", mobile, 5, 15*time.Minute),
		sourceLimit("otp-login-source", source, 30, 15*time.Minute),
	); err != nil {
		return "", empty, err
	}

	u, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return "", empty, err
	}

	if err := verifyUserSignature(u.PublicKey, req.Signature, req.Message); err != nil {
		return "", empty, err
	}
	digest, err := s.otpDigest(tenantID, mobile, strings.TrimSpace(req.Message))
	if err != nil {
		return "", empty, err
	}
	if err := s.Store.ConsumeOTPChallenge(ctx, tenantID, mobile, digest, now); err != nil {
		if !isOTPChallengeRejection(err) {
			return "", empty, err
		}
		if metricErr := s.Store.IncrementSuspicious(ctx, tenantID, mobile); metricErr != nil {
			return "", empty, metricErr
		}
		return "", empty, ErrWrongOTP
	}

	token, err := s.Auth.GenerateJWT(u.ID, u.Mobile, tenantID)
	if err != nil {
		return "", empty, err
	}
	return token, sanitizeUser(*u), nil
}

// RefreshJWT generates a new access token using the provided JWT + signature.
func (s *Service) RefreshJWT(ctx context.Context, tenantID string, req gateway.Token, source string, now time.Time) (string, error) {
	if s == nil || s.Store == nil {
		return "", ErrMissingStore
	}
	if s.Auth == nil {
		return "", ErrMissingAuth
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return "", err
	}
	source, err = normalizeRequestSource(source)
	if err != nil {
		return "", err
	}

	oldToken := strings.TrimSpace(req.JWT)
	claims, err := s.Auth.VerifyJWT(oldToken)
	if err != nil {
		if !errors.Is(err, jwt.ErrTokenExpired) || claims == nil {
			return "", err
		}
	}
	if claims == nil || claims.UserID <= 0 {
		return "", store.ErrInvalidUserID
	}
	claimTenantID, err := store.ValidateTenantID(claims.TenantID)
	if err != nil {
		return "", err
	}
	if claimTenantID != tenantID {
		return "", ErrRefreshTenantMismatch
	}
	if claims.IssuedAt == nil {
		return "", ErrRefreshExpired
	}
	issuedAt := claims.IssuedAt.Time.UTC()
	refreshExpiresAt := issuedAt.Add(refreshMaxAge)
	if issuedAt.After(now.Add(refreshClockSkew)) || !now.Before(refreshExpiresAt) {
		return "", ErrRefreshExpired
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		sourceLimit("refresh-source", source, 120, 15*time.Minute),
	); err != nil {
		return "", err
	}

	user, err := s.Store.FindUserByID(ctx, tenantID, claims.UserID)
	if err != nil {
		return "", err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		mobileLimit("refresh-mobile", user.Mobile, 20, 15*time.Minute),
	); err != nil {
		return "", err
	}
	if strings.TrimSpace(req.Mobile) != user.Mobile {
		return "", ErrInvalidSignature
	}
	if err := verifyUserSignature(user.PublicKey, req.Signature, req.Message); err != nil {
		return "", err
	}
	newToken, err := s.Auth.GenerateJWT(user.ID, user.Mobile, tenantID)
	if err != nil {
		return "", err
	}
	tokenHash := sha256.Sum256([]byte(oldToken))
	if err := s.Store.ConsumeRefreshToken(ctx, tenantID, user.ID, fmt.Sprintf("%x", tokenHash), now, refreshExpiresAt); err != nil {
		if errors.Is(err, store.ErrRefreshTokenReplay) {
			return "", ErrRefreshReplay
		}
		return "", err
	}
	return newToken, nil
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
	} else if !store.ErrNotFound(err) {
		return ebs_fields.User{}, err
	}
	// Make sure username is unique
	if u.Username != "" {
		if _, err := s.Store.FindUserByUsername(ctx, tenantID, u.Username); err == nil {
			return ebs_fields.User{}, errors.New("username already exists")
		} else if !store.ErrNotFound(err) {
			return ebs_fields.User{}, err
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

func (s *Service) VerifyOTP(ctx context.Context, tenantID, mobile, otp, source string, now time.Time) (ebs_fields.User, error) {
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
	source, err = normalizeRequestSource(source)
	if err != nil {
		return ebs_fields.User{}, err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		mobileLimit("otp-verify-mobile", mobile, 5, 15*time.Minute),
		sourceLimit("otp-verify-source", source, 30, 15*time.Minute),
	); err != nil {
		return ebs_fields.User{}, err
	}

	u, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return ebs_fields.User{}, err
	}
	digest, err := s.otpDigest(tenantID, mobile, strings.TrimSpace(otp))
	if err != nil {
		return ebs_fields.User{}, err
	}
	if err := s.Store.ConsumeOTPChallenge(ctx, tenantID, mobile, digest, now); err != nil {
		if !isOTPChallengeRejection(err) {
			return ebs_fields.User{}, err
		}
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
		return ebs_fields.User{}, ErrMissingPassword
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

func (s *Service) GenerateSignInCode(ctx context.Context, tenantID, mobile, source string, now time.Time) error {
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
	source, err = normalizeRequestSource(source)
	if err != nil {
		return err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		mobileLimit("otp-generate-cooldown", mobile, 1, time.Minute),
		mobileLimit("otp-generate-mobile", mobile, 3, 15*time.Minute),
		sourceLimit("otp-generate-source", source, 20, 15*time.Minute),
	); err != nil {
		return err
	}
	_, err = s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return err
	}
	if _, err := s.Store.RecordLoginAttempt(ctx, tenantID, mobile, true); err != nil {
		return err
	}
	key, err := generateOTPCode()
	if err != nil {
		return err
	}
	digest, err := s.otpDigest(tenantID, mobile, key)
	if err != nil {
		return err
	}
	if err := s.Store.StoreOTPChallenge(ctx, tenantID, mobile, digest, now, now.Add(otpChallengeTTL), otpMaxAttempts); err != nil {
		return err
	}
	if err := utils.SendSMS(&s.NoebsConfig, utils.SMS{
		Mobile:  mobile,
		Message: fmt.Sprintf("Your one-time access code is: %s. DON'T share it with anyone.", key),
	}); err != nil {
		if cleanupErr := s.Store.DeleteOTPChallenge(ctx, tenantID, mobile); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}
	return nil
}
