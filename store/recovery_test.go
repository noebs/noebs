package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"golang.org/x/crypto/bcrypt"
)

func TestOTPChallengePurposesAreIsolated(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	signInDigest := strings.Repeat("a", 64)
	recoveryDigest := strings.Repeat("b", 64)

	if err := s.StoreOTPChallengeForPurpose(ctx, "tenant", "0990000000", OTPChallengePurposeSignIn, signInDigest, now, now.Add(10*time.Minute), 5); err != nil {
		t.Fatalf("store sign-in challenge: %v", err)
	}
	if err := s.StoreOTPChallengeForPurpose(ctx, "tenant", "0990000000", OTPChallengePurposePasswordRecovery, recoveryDigest, now, now.Add(10*time.Minute), 5); err != nil {
		t.Fatalf("store recovery challenge: %v", err)
	}
	if err := s.ConsumeOTPChallengeForPurpose(ctx, "tenant", "0990000000", OTPChallengePurposePasswordRecovery, signInDigest, now.Add(time.Minute)); !errors.Is(err, ErrInvalidOTPChallenge) {
		t.Fatalf("cross-purpose consume error = %v, want %v", err, ErrInvalidOTPChallenge)
	}
	if err := s.ConsumeOTPChallengeForPurpose(ctx, "tenant", "0990000000", OTPChallengePurposeSignIn, signInDigest, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("consume sign-in challenge: %v", err)
	}
	if err := s.ConsumeOTPChallengeForPurpose(ctx, "tenant", "0990000000", OTPChallengePurposePasswordRecovery, recoveryDigest, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("consume recovery challenge: %v", err)
	}
}

func TestConsumeSignInChallengeVerifiesUserAtomically(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	user := ebs_fields.User{Mobile: "0990000000", Username: "0990000000", Password: "hash", PublicKey: "key"}
	if err := s.CreateUser(ctx, "tenant", &user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := s.StoreOTPChallenge(ctx, "tenant", user.Mobile, testAuthDigest, now, now.Add(10*time.Minute), 5); err != nil {
		t.Fatalf("store challenge: %v", err)
	}

	if err := s.ConsumeSignInChallengeAndVerifyUser(ctx, "tenant", user.Mobile, testAuthDigest, user.ID+1, now.Add(time.Minute)); !ErrNotFound(err) {
		t.Fatalf("consume with missing user error = %v, want not found", err)
	}
	if err := s.ConsumeSignInChallengeAndVerifyUser(ctx, "tenant", user.Mobile, testAuthDigest, user.ID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("consume after rolled-back transition: %v", err)
	}
	stored, err := s.GetUserByMobile(ctx, "tenant", user.Mobile)
	if err != nil {
		t.Fatalf("get verified user: %v", err)
	}
	if !stored.IsVerified || !stored.IsPasswordOTP {
		t.Fatalf("stored flags = verified:%v password_otp:%v", stored.IsVerified, stored.IsPasswordOTP)
	}
	if err := s.ConsumeOTPChallenge(ctx, "tenant", user.Mobile, testAuthDigest, now.Add(3*time.Minute)); !errors.Is(err, ErrOTPChallengeConsumed) {
		t.Fatalf("challenge replay error = %v, want %v", err, ErrOTPChallengeConsumed)
	}
}

func TestRecoveryCredentialResetIsOneTimeTenantBoundAndRotatesIdentity(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	oldHash, err := bcrypt.GenerateFromPassword([]byte("Old1!Password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash old password: %v", err)
	}
	user := ebs_fields.User{
		Mobile:      "0990000000",
		Username:    "0990000000",
		Password:    string(oldHash),
		PublicKey:   "old-key",
		DeviceID:    "old-device",
		DeviceToken: "old-push-token",
		IsVerified:  true,
	}
	if err := s.CreateUser(ctx, "tenant", &user); err != nil {
		t.Fatalf("create user: %v", err)
	}

	expired := strings.Repeat("c", 64)
	first := strings.Repeat("d", 64)
	sibling := strings.Repeat("e", 64)
	if err := s.StorePasswordRecoveryCredential(ctx, "tenant", expired, user.ID, now, now.Add(time.Minute)); err != nil {
		t.Fatalf("store expired credential: %v", err)
	}
	if err := s.ResetIdentityWithRecoveryCredential(ctx, "tenant", expired, "new-hash", "new-key", now.Add(time.Minute)); !errors.Is(err, ErrInvalidRecoveryCredential) {
		t.Fatalf("expired credential error = %v, want %v", err, ErrInvalidRecoveryCredential)
	}
	if err := s.StorePasswordRecoveryCredential(ctx, "tenant", first, user.ID, now, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("store first credential: %v", err)
	}
	if err := s.StorePasswordRecoveryCredential(ctx, "tenant", sibling, user.ID, now, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("store sibling credential: %v", err)
	}
	if err := s.ResetIdentityWithRecoveryCredential(ctx, "other-tenant", first, "new-hash", "new-key", now.Add(time.Minute)); !errors.Is(err, ErrInvalidRecoveryCredential) {
		t.Fatalf("cross-tenant credential error = %v, want %v", err, ErrInvalidRecoveryCredential)
	}

	newHash, err := bcrypt.GenerateFromPassword([]byte("New2@Password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash new password: %v", err)
	}
	if err := s.ResetIdentityWithRecoveryCredential(ctx, "tenant", first, string(newHash), "new-key", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("reset identity: %v", err)
	}
	for name, token := range map[string]string{"replay": first, "sibling": sibling} {
		if err := s.ResetIdentityWithRecoveryCredential(ctx, "tenant", token, string(newHash), "newer-key", now.Add(3*time.Minute)); !errors.Is(err, ErrInvalidRecoveryCredential) {
			t.Fatalf("%s credential error = %v, want %v", name, err, ErrInvalidRecoveryCredential)
		}
	}

	stored, err := s.GetUserByMobile(ctx, "tenant", user.Mobile)
	if err != nil {
		t.Fatalf("get recovered user: %v", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored.Password), []byte("New2@Password")); err != nil {
		t.Fatalf("new password was not stored: %v", err)
	}
	if stored.PublicKey != "new-key" || stored.DeviceID != "" || stored.DeviceToken != "" || stored.SessionEpoch != 2 {
		t.Fatalf("recovered identity = key:%q device:%q push:%q epoch:%d", stored.PublicKey, stored.DeviceID, stored.DeviceToken, stored.SessionEpoch)
	}
	if err := s.ValidateSessionEpoch(ctx, "tenant", user.ID, 1); !errors.Is(err, ErrSessionRevoked) {
		t.Fatalf("pre-reset session epoch error = %v, want %v", err, ErrSessionRevoked)
	}
	if err := s.ValidateSessionEpoch(ctx, "tenant", user.ID, 2); err != nil {
		t.Fatalf("current session epoch: %v", err)
	}
}

func TestResumeUnverifiedRegistrationCannotRotateVerifiedAccount(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	user := ebs_fields.User{Mobile: "0990000000", Username: "0990000000", Password: "first-hash", PublicKey: "first-key"}
	if err := s.CreateUser(ctx, "tenant", &user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := s.ResumeUnverifiedRegistration(ctx, "tenant", user.Mobile, "second-hash", "second-key", now); err != nil {
		t.Fatalf("resume unverified registration: %v", err)
	}
	if err := s.SetUserVerified(ctx, "tenant", user.ID, true); err != nil {
		t.Fatalf("verify user: %v", err)
	}
	if err := s.ResumeUnverifiedRegistration(ctx, "tenant", user.Mobile, "third-hash", "third-key", now.Add(time.Minute)); err != nil {
		t.Fatalf("repeat verified registration: %v", err)
	}
	stored, err := s.GetUserByMobile(ctx, "tenant", user.Mobile)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if stored.Password != "second-hash" || stored.PublicKey != "second-key" {
		t.Fatalf("verified credentials changed: password=%q key=%q", stored.Password, stored.PublicKey)
	}
}
