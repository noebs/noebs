package consumer

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/adonese/noebs/store"
	"github.com/adonese/noebs/utils"
	"golang.org/x/crypto/bcrypt"
)

const (
	recoveryCredentialTTL   = 10 * time.Minute
	recoveryCredentialBytes = 32
)

var (
	ErrInvalidRecoveryChallenge  = errors.New("invalid_recovery_challenge")
	ErrInvalidRecoveryCredential = errors.New("invalid_recovery_credential")
)

type PasswordRecoveryRequest struct {
	Mobile string `json:"mobile"`
}

type PasswordRecoveryVerification struct {
	Mobile string `json:"mobile"`
	OTP    string `json:"otp"`
}

type PasswordRecoveryReset struct {
	RecoveryCredential string `json:"recovery_credential"`
	NewPassword        string `json:"new_password"`
	NewPublicKey       string `json:"new_public_key"`
}

type RecoveryCredentialCommand struct {
	UserID int64  `json:"user_id"`
	Mobile string `json:"mobile"`
}

type RecoveryCredentialResult struct {
	RecoveryCredential string `json:"recovery_credential"`
	ExpiresIn          int64  `json:"expires_in"`
}

type SessionValidationCommand struct {
	UserID       int64 `json:"user_id"`
	SessionEpoch int64 `json:"session_epoch"`
}

func (s *Service) ValidateSession(ctx context.Context, tenantID string, cmd SessionValidationCommand) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if err := s.Store.ValidateSessionEpoch(ctx, tenantID, cmd.UserID, cmd.SessionEpoch); err != nil {
		if errors.Is(err, store.ErrSessionRevoked) {
			return ErrSessionRevoked
		}
		return err
	}
	return nil
}

// RequestPasswordRecovery sends a purpose-scoped challenge only for a verified
// account. Missing, unverified, and delivery-failure cases all return the same
// successful result so the endpoint cannot be used to enumerate accounts.
func (s *Service) RequestPasswordRecovery(ctx context.Context, tenantID, mobile, source string, now time.Time) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	mobile = strings.ToLower(strings.TrimSpace(mobile))
	if mobile == "" {
		return ErrMissingMobile
	}
	source, err = normalizeRequestSource(source)
	if err != nil {
		return err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		mobileLimit("recovery-request-cooldown", mobile, 1, time.Minute),
		mobileLimit("recovery-request-mobile", mobile, 3, 15*time.Minute),
		sourceLimit("recovery-request-source", source, 20, 15*time.Minute),
	); err != nil {
		return err
	}

	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		if store.ErrNotFound(err) {
			return nil
		}
		return err
	}
	if !user.IsVerified {
		return nil
	}

	code, err := generateOTPCode()
	if err != nil {
		return err
	}
	digest, err := s.otpDigest(tenantID, mobile, code)
	if err != nil {
		return err
	}
	if err := s.Store.StoreOTPChallengeForPurpose(ctx, tenantID, mobile, store.OTPChallengePurposePasswordRecovery, digest, now, now.Add(otpChallengeTTL), otpMaxAttempts); err != nil {
		return err
	}
	if err := utils.SendSMS(&s.NoebsConfig, utils.SMS{
		Mobile:  mobile,
		Message: fmt.Sprintf("Your password recovery code is: %s. DON'T share it with anyone.", code),
	}); err != nil {
		_ = s.Store.DeleteOTPChallengeForPurpose(ctx, tenantID, mobile, store.OTPChallengePurposePasswordRecovery)
		if s.Logger != nil {
			s.Logger.WithField("event", "auth_challenge_delivery_failed").
				WithField("flow", "password_recovery").WithError(err).
				Error("authentication challenge delivery failed")
		}
	}
	return nil
}

// VerifyPasswordRecoveryOTP deliberately requires no device signature. A
// successful OTP exchange yields an opaque, one-time credential, not a session.
func (s *Service) VerifyPasswordRecoveryOTP(ctx context.Context, tenantID, mobile, otp, source string, now time.Time) (RecoveryCredentialResult, error) {
	if s == nil || s.Store == nil {
		return RecoveryCredentialResult{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	mobile = strings.ToLower(strings.TrimSpace(mobile))
	otp = strings.TrimSpace(otp)
	if mobile == "" || otp == "" {
		return RecoveryCredentialResult{}, ErrInvalidRecoveryChallenge
	}
	source, err = normalizeRequestSource(source)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		mobileLimit("recovery-verify-mobile", mobile, 5, 15*time.Minute),
		sourceLimit("recovery-verify-source", source, 30, 15*time.Minute),
	); err != nil {
		return RecoveryCredentialResult{}, err
	}

	digest, err := s.otpDigest(tenantID, mobile, otp)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	if err := s.Store.ConsumeOTPChallengeForPurpose(ctx, tenantID, mobile, store.OTPChallengePurposePasswordRecovery, digest, now); err != nil {
		if !isOTPChallengeRejection(err) {
			return RecoveryCredentialResult{}, err
		}
		_ = s.Store.IncrementSuspicious(ctx, tenantID, mobile)
		return RecoveryCredentialResult{}, ErrInvalidRecoveryChallenge
	}

	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		if store.ErrNotFound(err) {
			return RecoveryCredentialResult{}, ErrInvalidRecoveryChallenge
		}
		return RecoveryCredentialResult{}, err
	}
	if !user.IsVerified {
		return RecoveryCredentialResult{}, ErrInvalidRecoveryChallenge
	}
	return s.issueRecoveryCredential(ctx, tenantID, user.ID, now)
}

func (s *Service) ResetPasswordWithRecoveryCredential(ctx context.Context, tenantID string, req PasswordRecoveryReset, source string, now time.Time) error {
	if s == nil || s.Store == nil {
		return ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	credential := strings.TrimSpace(req.RecoveryCredential)
	if credential == "" {
		return ErrInvalidRecoveryCredential
	}
	if !validatePassword(req.NewPassword) {
		return ErrPasswordInvalid
	}
	_, publicKey, err := parseUserPublicKey(req.NewPublicKey)
	if err != nil {
		return err
	}
	source, err = normalizeRequestSource(source)
	if err != nil {
		return err
	}
	if err := s.enforceAuthLimits(ctx, tenantID, now,
		authLimitRule{
			action:  "recovery-reset-credential",
			subject: authSubjectHash("recovery_credential", credential),
			limit:   5,
			window:  15 * time.Minute,
		},
		sourceLimit("recovery-reset-source", source, 30, 15*time.Minute),
	); err != nil {
		return err
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	tokenHash := recoveryCredentialDigest(credential)
	if err := s.Store.ResetIdentityWithRecoveryCredential(ctx, tenantID, tokenHash, string(passwordHash), publicKey, now); err != nil {
		if errors.Is(err, store.ErrInvalidRecoveryCredential) || errors.Is(err, store.ErrMissingRecoveryCredential) {
			return ErrInvalidRecoveryCredential
		}
		return err
	}
	return nil
}

// IssueRecoveryCredential is the trusted card-recovery counterpart to the OTP
// exchange. It still returns only the same narrowly scoped opaque credential.
func (s *Service) IssueRecoveryCredential(ctx context.Context, tenantID string, cmd RecoveryCredentialCommand, now time.Time) (RecoveryCredentialResult, error) {
	if s == nil || s.Store == nil {
		return RecoveryCredentialResult{}, ErrMissingStore
	}
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	if cmd.UserID <= 0 {
		return RecoveryCredentialResult{}, store.ErrInvalidUserID
	}
	mobile := strings.TrimSpace(cmd.Mobile)
	if mobile == "" {
		return RecoveryCredentialResult{}, ErrMissingMobile
	}
	user, err := s.Store.GetUserByMobile(ctx, tenantID, mobile)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	if user.ID != cmd.UserID || !user.IsVerified {
		return RecoveryCredentialResult{}, store.ErrInvalidUserID
	}
	return s.issueRecoveryCredential(ctx, tenantID, user.ID, now)
}

func (s *Service) issueRecoveryCredential(ctx context.Context, tenantID string, userID int64, now time.Time) (RecoveryCredentialResult, error) {
	raw := make([]byte, recoveryCredentialBytes)
	if _, err := cryptorand.Read(raw); err != nil {
		return RecoveryCredentialResult{}, err
	}
	credential := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.Store.StorePasswordRecoveryCredential(ctx, tenantID, recoveryCredentialDigest(credential), userID, now, now.Add(recoveryCredentialTTL)); err != nil {
		return RecoveryCredentialResult{}, err
	}
	return RecoveryCredentialResult{
		RecoveryCredential: credential,
		ExpiresIn:          int64(recoveryCredentialTTL / time.Second),
	}, nil
}

func recoveryCredentialDigest(credential string) string {
	digest := sha256.Sum256([]byte(credential))
	return hex.EncodeToString(digest[:])
}

func (s *Service) IssueRecoveryCredentialInIdentityAuth(ctx context.Context, tenantID string, cmd RecoveryCredentialCommand) (RecoveryCredentialResult, error) {
	tenantID, err := store.ValidateTenantID(tenantID)
	if err != nil {
		return RecoveryCredentialResult{}, err
	}
	var result RecoveryCredentialResult
	if err := s.doAdminServiceCommand(ctx, tenantID, identityAuthCommandTarget, "/internal/identity-auth/recovery-credential", cmd, &result); err != nil {
		return RecoveryCredentialResult{}, err
	}
	if strings.TrimSpace(result.RecoveryCredential) == "" || result.ExpiresIn <= 0 {
		return RecoveryCredentialResult{}, ErrInvalidRecoveryCredential
	}
	return result, nil
}
