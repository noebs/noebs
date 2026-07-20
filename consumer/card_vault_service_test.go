package consumer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestOpaqueCardOperationsUseOnlyCardVaultSchema(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}
	ctx := context.Background()
	userID := int64(42)
	now := time.Date(2026, time.July, 18, 12, 0, 0, 0, time.UTC)

	first := enrollOpaqueConsumerTestCard(t, service, tenantID, userID, "4242424242424242", "Primary", now)
	second := enrollOpaqueConsumerTestCard(t, service, tenantID, userID, "5555555555554242", "Travel", now.Add(time.Minute))
	if first.CardID == second.CardID || first.MaskedPAN != "****4242" || second.MaskedPAN != "****4242" {
		t.Fatalf("opaque cards = %+v and %+v", first, second)
	}

	cards, err := service.ListOpaqueCardsForUserID(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("list opaque cards: %v", err)
	}
	if len(cards) != 2 || cards[0].CardID != first.CardID || !cards[0].IsMain || cards[1].CardID != second.CardID || cards[1].IsMain {
		t.Fatalf("initial cards = %+v", cards)
	}
	assertPublicCardSummaryShape(t, cards)

	for name, call := range map[string]func() error{
		"rename": func() error {
			return service.RenameOpaqueCardForUserID(ctx, tenantID, userID, " "+second.CardID, "Mutated")
		},
		"set main": func() error {
			return service.SetOpaqueMainCardForUserID(ctx, tenantID, userID, second.CardID+" ")
		},
		"retire": func() error {
			return service.RetireOpaqueCardForUserID(ctx, tenantID, userID, "\t"+second.CardID)
		},
	} {
		t.Run("non-canonical "+name, func(t *testing.T) {
			if err := call(); !errors.Is(err, store.ErrInvalidCardID) {
				t.Fatalf("error = %v, want %v", err, store.ErrInvalidCardID)
			}
		})
	}
	cards, err = service.ListOpaqueCardsForUserID(ctx, tenantID, userID)
	if err != nil || len(cards) != 2 || cards[0].CardID != first.CardID || !cards[0].IsMain || cards[1].Name != "Travel" || cards[1].IsMain {
		t.Fatalf("cards changed after rejected IDs: %+v, %v", cards, err)
	}

	if err := service.RenameOpaqueCardForUserID(ctx, tenantID, userID, second.CardID, "Trips"); err != nil {
		t.Fatalf("rename opaque card: %v", err)
	}
	if err := service.SetOpaqueMainCardForUserID(ctx, tenantID, userID, second.CardID); err != nil {
		t.Fatalf("set opaque main card: %v", err)
	}
	cards, err = service.ListOpaqueCardsForUserID(ctx, tenantID, userID)
	if err != nil || len(cards) != 2 || cards[0].CardID != second.CardID || cards[0].Name != "Trips" || !cards[0].IsMain || cards[1].IsMain {
		t.Fatalf("updated cards = %+v, %v", cards, err)
	}

	if err := service.RetireOpaqueCardForUserID(ctx, tenantID, userID, second.CardID); err != nil {
		t.Fatalf("retire opaque card: %v", err)
	}
	cards, err = service.ListOpaqueCardsForUserID(ctx, tenantID, userID)
	if err != nil || len(cards) != 1 || cards[0].CardID != first.CardID || !cards[0].IsMain {
		t.Fatalf("cards after main retirement = %+v, %v", cards, err)
	}

	var cardRows int
	if err := db.GetContext(ctx, &cardRows, "SELECT COUNT(*) FROM cards"); err != nil {
		t.Fatalf("count card rows: %v", err)
	}
	if cardRows != 2 {
		t.Fatalf("card-vault rows: cards=%d", cardRows)
	}
	var identityTableExists bool
	if err := db.GetContext(ctx, &identityTableExists, "SELECT to_regclass('users') IS NOT NULL"); err != nil {
		t.Fatalf("inspect identity table: %v", err)
	}
	if identityTableExists {
		t.Fatal("card-vault scope created identity tables")
	}
}

func TestSetOpaqueMainCardRejectsUnknownCard(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeCardVault})
	service := &Service{Store: storeSvc}

	err := service.SetOpaqueMainCardForUserID(context.Background(), tenantID, 42, "0f8fad5b-d9cb-469f-a165-70867728950e")
	if !errors.Is(err, store.ErrCardNotFound) {
		t.Fatalf("error = %v, want %v", err, store.ErrCardNotFound)
	}
}

func TestNonCanonicalEnrollmentIDsFailBeforeNetworkIO(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate enrollment key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("marshal enrollment key: %v", err)
	}

	var requests atomic.Int64
	service := &Service{
		HTTPClient: &http.Client{Transport: opaqueRoundTripFunc(func(*http.Request) (*http.Response, error) {
			requests.Add(1)
			return nil, errors.New("unexpected network request")
		})},
		NoebsConfig: ebs_fields.NoebsConfig{EBSConsumerKey: base64.StdEncoding.EncodeToString(der)},
	}
	const (
		enrollmentID = "0f8fad5b-d9cb-469f-a165-70867728950e"
		railUUID     = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	)

	tests := []struct {
		name         string
		enrollmentID string
		railUUID     string
		want         error
	}{
		{name: "leading enrollment whitespace", enrollmentID: " " + enrollmentID, railUUID: railUUID, want: store.ErrInvalidEnrollmentIntent},
		{name: "trailing enrollment whitespace", enrollmentID: enrollmentID + " ", railUUID: railUUID, want: store.ErrInvalidEnrollmentIntent},
		{name: "leading rail whitespace", enrollmentID: enrollmentID, railUUID: " " + railUUID, want: store.ErrInvalidRailUUID},
		{name: "trailing rail whitespace", enrollmentID: enrollmentID, railUUID: railUUID + " ", want: store.ErrInvalidRailUUID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := service.ConfirmOpaqueCardEnrollment(context.Background(), "tenant", 42, tt.enrollmentID, ConfirmCardEnrollmentRequest{
				RailUUID: tt.railUUID,
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf("rejected identifier issued %d network requests", got)
			}
		})
	}
}

func enrollOpaqueConsumerTestCard(t *testing.T, service *Service, tenantID string, userID int64, pan, name string, now time.Time) ebs_fields.CardSummary {
	t.Helper()
	ctx := context.Background()
	intent, err := service.CreateCardEnrollmentIntentForUserID(ctx, tenantID, userID, now)
	if err != nil {
		t.Fatalf("create enrollment intent: %v", err)
	}
	begin, err := service.BeginCardEnrollmentForUserID(ctx, tenantID, userID, BeginCardEnrollmentCommand{
		EnrollmentID: intent.EnrollmentID,
		PAN:          pan,
		Expiry:       "2912",
		Name:         name,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatalf("begin enrollment: %v", err)
	}
	if begin.RailUUID != intent.RailUUID {
		t.Fatalf("begin rail UUID = %q, want %q", begin.RailUUID, intent.RailUUID)
	}
	claim, err := service.ClaimCardEnrollmentRailForUserID(ctx, tenantID, userID, ClaimCardEnrollmentRailCommand{
		EnrollmentID: intent.EnrollmentID,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("claim enrollment rail: %v", err)
	}
	if !claim.Granted {
		t.Fatal("first enrollment rail claim was not granted")
	}
	card, err := service.CompleteCardEnrollmentForUserID(ctx, tenantID, userID, CompleteCardEnrollmentCommand{
		EnrollmentID: intent.EnrollmentID,
		PAN:          pan,
		Expiry:       "2912",
		Name:         name,
	}, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("complete enrollment: %v", err)
	}
	return card
}

func assertPublicCardSummaryShape(t *testing.T, cards []ebs_fields.CardSummary) {
	t.Helper()
	encoded, err := json.Marshal(cards)
	if err != nil {
		t.Fatalf("marshal card summaries: %v", err)
	}
	var values []map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &values); err != nil {
		t.Fatalf("decode card summaries: %v", err)
	}
	allowed := map[string]bool{
		"card_id": true, "name": true, "masked_pan": true,
		"exp_date": true, "is_main": true, "status": true,
	}
	for i, value := range values {
		if len(value) != len(allowed) {
			t.Fatalf("card %d fields = %v", i, value)
		}
		for field := range value {
			if !allowed[field] {
				t.Fatalf("card %d exposes private field %q", i, field)
			}
		}
	}
}

type opaqueRoundTripFunc func(*http.Request) (*http.Response, error)

func (f opaqueRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
