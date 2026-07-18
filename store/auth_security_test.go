package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

const testAuthDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestAuthSecurityInputsAreValidatedBeforeDB(t *testing.T) {
	s := &Store{}
	ctx := context.Background()
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)

	if _, err := s.RecordAuthAttempt(ctx, "tenant", "", testAuthDigest, now, time.Minute); !errors.Is(err, ErrMissingAuthAction) {
		t.Fatalf("RecordAuthAttempt(missing action) error = %v, want %v", err, ErrMissingAuthAction)
	}
	if _, err := s.RecordAuthAttempt(ctx, "tenant", "otp", "bad", now, time.Minute); !errors.Is(err, ErrMissingAuthSubject) {
		t.Fatalf("RecordAuthAttempt(invalid subject) error = %v, want %v", err, ErrMissingAuthSubject)
	}
	if _, err := s.RecordAuthAttempt(ctx, "tenant", "otp", testAuthDigest, time.Time{}, time.Minute); !errors.Is(err, ErrInvalidAuthTime) {
		t.Fatalf("RecordAuthAttempt(missing time) error = %v, want %v", err, ErrInvalidAuthTime)
	}
	if err := s.StoreOTPChallenge(ctx, "tenant", "", testAuthDigest, now, now.Add(time.Minute), 5); !errors.Is(err, ErrMissingMobile) {
		t.Fatalf("StoreOTPChallenge(missing mobile) error = %v, want %v", err, ErrMissingMobile)
	}
	if err := s.ConsumeRefreshToken(ctx, "tenant", 0, testAuthDigest, now, now.Add(time.Hour)); !errors.Is(err, ErrInvalidUserID) {
		t.Fatalf("ConsumeRefreshToken(invalid user) error = %v, want %v", err, ErrInvalidUserID)
	}
}

func TestStoreRecordAuthAttemptUsesExplicitFixedWindow(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	window := 15 * time.Minute

	first, err := s.RecordAuthAttempt(ctx, "tenant", "otp-verify", testAuthDigest, now, window)
	if err != nil {
		t.Fatalf("first RecordAuthAttempt(): %v", err)
	}
	if first.Count != 1 || !first.ResetAt.Equal(now.Add(window)) {
		t.Fatalf("first result = %+v, want count 1 reset %s", first, now.Add(window))
	}
	second, err := s.RecordAuthAttempt(ctx, "tenant", "otp-verify", testAuthDigest, now.Add(time.Minute), window)
	if err != nil {
		t.Fatalf("second RecordAuthAttempt(): %v", err)
	}
	if second.Count != 2 || !second.ResetAt.Equal(first.ResetAt) {
		t.Fatalf("second result = %+v, want count 2 reset %s", second, first.ResetAt)
	}
	reset, err := s.RecordAuthAttempt(ctx, "tenant", "otp-verify", testAuthDigest, now.Add(window), window)
	if err != nil {
		t.Fatalf("reset RecordAuthAttempt(): %v", err)
	}
	if reset.Count != 1 || !reset.ResetAt.Equal(now.Add(2*window)) {
		t.Fatalf("reset result = %+v, want count 1 reset %s", reset, now.Add(2*window))
	}
}

func TestStoreOTPChallengeIsSingleUseAndExpiresAtExplicitTime(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)
	correct := testAuthDigest
	wrong := strings.Repeat("b", 64)

	if err := s.StoreOTPChallenge(ctx, "tenant", "0990000000", correct, now, now.Add(10*time.Minute), 5); err != nil {
		t.Fatalf("StoreOTPChallenge(): %v", err)
	}
	if err := s.ConsumeOTPChallenge(ctx, "tenant", "0990000000", wrong, now.Add(time.Minute)); !errors.Is(err, ErrInvalidOTPChallenge) {
		t.Fatalf("ConsumeOTPChallenge(wrong) error = %v, want %v", err, ErrInvalidOTPChallenge)
	}
	if err := s.ConsumeOTPChallenge(ctx, "tenant", "0990000000", correct, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("ConsumeOTPChallenge(correct): %v", err)
	}
	if err := s.ConsumeOTPChallenge(ctx, "tenant", "0990000000", correct, now.Add(3*time.Minute)); !errors.Is(err, ErrOTPChallengeConsumed) {
		t.Fatalf("ConsumeOTPChallenge(replay) error = %v, want %v", err, ErrOTPChallengeConsumed)
	}

	if err := s.StoreOTPChallenge(ctx, "tenant", "0990000001", correct, now, now.Add(10*time.Minute), 5); err != nil {
		t.Fatalf("StoreOTPChallenge(expiry case): %v", err)
	}
	if err := s.ConsumeOTPChallenge(ctx, "tenant", "0990000001", correct, now.Add(10*time.Minute)); !errors.Is(err, ErrOTPChallengeExpired) {
		t.Fatalf("ConsumeOTPChallenge(expired) error = %v, want %v", err, ErrOTPChallengeExpired)
	}
}

func TestStoreConsumeRefreshTokenAllowsOneConcurrentUse(t *testing.T) {
	ctx := context.Background()
	s := newIdentityAuthTestStore(t, ctx)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)

	errs := make(chan error, 2)
	var start sync.WaitGroup
	start.Add(1)
	for range 2 {
		go func() {
			start.Wait()
			errs <- s.ConsumeRefreshToken(ctx, "tenant", 42, testAuthDigest, now, now.Add(30*24*time.Hour))
		}()
	}
	start.Done()

	var successes, replays int
	for range 2 {
		err := <-errs
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrRefreshTokenReplay):
			replays++
		default:
			t.Fatalf("ConsumeRefreshToken() unexpected error: %v", err)
		}
	}
	if successes != 1 || replays != 1 {
		t.Fatalf("results = %d successes, %d replays; want 1 and 1", successes, replays)
	}
}
