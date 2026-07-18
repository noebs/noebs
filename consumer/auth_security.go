package consumer

import (
	"context"
	"crypto/hmac"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strings"
	"time"

	"github.com/adonese/noebs/store"
)

const (
	otpChallengeTTL  = 10 * time.Minute
	otpMaxAttempts   = 5
	refreshMaxAge    = 30 * 24 * time.Hour
	refreshClockSkew = 5 * time.Minute
)

type authLimitRule struct {
	action  string
	subject string
	limit   int
	window  time.Duration
}

func mobileLimit(action, mobile string, limit int, window time.Duration) authLimitRule {
	return authLimitRule{action: action, subject: authSubjectHash("mobile", mobile), limit: limit, window: window}
}

func sourceLimit(action, source string, limit int, window time.Duration) authLimitRule {
	return authLimitRule{action: action, subject: authSubjectHash("source", source), limit: limit, window: window}
}

func (s *Service) enforceAuthLimits(ctx context.Context, tenantID string, now time.Time, rules ...authLimitRule) error {
	for _, rule := range rules {
		result, err := s.Store.RecordAuthAttempt(ctx, tenantID, rule.action, rule.subject, now, rule.window)
		if err != nil {
			return err
		}
		if result.Count <= rule.limit {
			continue
		}
		return &RateLimitError{RetryAfter: result.ResetAt.Sub(now)}
	}
	return nil
}

func normalizeRequestSource(source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", ErrMissingRequestSource
	}
	ip := net.ParseIP(source)
	if ip == nil {
		return "", ErrInvalidRequestSource
	}
	return ip.String(), nil
}

func authSubjectHash(kind, value string) string {
	digest := sha256.Sum256([]byte(kind + "\x00" + strings.TrimSpace(value)))
	return hex.EncodeToString(digest[:])
}

func generateOTPCode() (string, error) {
	value, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func (s *Service) otpDigest(tenantID, mobile, code string) (string, error) {
	if s == nil || s.NoebsConfig.JWTKey == "" {
		return "", ErrMissingOTPSecret
	}
	mac := hmac.New(sha256.New, []byte(s.NoebsConfig.JWTKey))
	_, _ = mac.Write([]byte(tenantID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(mobile))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func isOTPChallengeRejection(err error) bool {
	return errorsIsAny(err,
		store.ErrOTPChallengeNotFound,
		store.ErrOTPChallengeExpired,
		store.ErrOTPChallengeConsumed,
		store.ErrOTPAttemptsExceeded,
		store.ErrInvalidOTPChallenge,
	)
}

func errorsIsAny(err error, targets ...error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}
