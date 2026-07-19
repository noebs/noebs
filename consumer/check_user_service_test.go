package consumer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/adonese/noebs/store"
)

func TestCheckUserReturnsOnlyIdentityMembership(t *testing.T) {
	db, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	user := seedProfile(t, storeSvc, tenantID, "0912141660")
	service := &Service{Store: storeSvc}

	result, err := service.CheckUser(context.Background(), tenantID, user.UserID, []string{"0912141660", "0999999999"})
	if err != nil {
		t.Fatalf("check user: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("result length = %d, want 2: %+v", len(result), result)
	}
	if result[0] != (CheckUserResult{Phone: "0912141660", IsUser: true}) {
		t.Fatalf("existing user result = %+v", result[0])
	}
	if result[1] != (CheckUserResult{Phone: "0999999999", IsUser: false}) {
		t.Fatalf("missing user result = %+v", result[1])
	}
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM cards LIMIT 1"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("identity-auth scope should not create card tables, err=%v", err)
	}
}

func TestCheckUserIncludesUsersWithoutCards(t *testing.T) {
	_, storeSvc, tenantID := newTestDBWithScopes(t, []string{store.MigrationScopeIdentityAuth})
	user := seedProfile(t, storeSvc, tenantID, "0912141660")
	service := &Service{Store: storeSvc}

	result, err := service.CheckUser(context.Background(), tenantID, user.UserID, []string{"0912141660"})
	if err != nil {
		t.Fatalf("check user: %v", err)
	}
	if len(result) != 1 || !result[0].IsUser {
		t.Fatalf("result = %+v, want the existing user without card data", result)
	}
}

func TestCheckUserRequiresStore(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	if _, err := service.CheckUser(context.Background(), "tenant-a", 1, []string{"0912141660"}); err == nil {
		t.Fatal("CheckUser() error = nil, want store error")
	}
}

func TestCheckUserRejectsBlankPhonesBeforeCardVault(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	for _, phones := range [][]string{
		nil,
		{},
		{" "},
		{"0912141660", " "},
	} {
		if _, err := service.CheckUser(context.Background(), "tenant-a", 1, phones); !errors.Is(err, ErrMissingMobile) {
			t.Fatalf("CheckUser(%q) error = %v, want %v", phones, err, ErrMissingMobile)
		}
	}
}

func TestNormalizeCheckUserPhonesTrimsDeduplicatesAndPreservesOrder(t *testing.T) {
	got, err := normalizeCheckUserPhones([]string{" 0912141660 ", "0999999999", "0912141660"})
	if err != nil {
		t.Fatalf("normalizeCheckUserPhones() error = %v", err)
	}
	want := []string{"0912141660", "0999999999"}
	if len(got) != len(want) {
		t.Fatalf("normalized length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("normalized[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeCheckUserPhonesRejectsOversizedBatch(t *testing.T) {
	phones := make([]string, maxCheckUserPhones+1)
	for i := range phones {
		phones[i] = fmt.Sprintf("09%08d", i)
	}
	if _, err := normalizeCheckUserPhones(phones); !errors.Is(err, ErrCheckUserBatchTooLarge) {
		t.Fatalf("normalizeCheckUserPhones() error = %v, want %v", err, ErrCheckUserBatchTooLarge)
	}
}

func TestCheckUserPropagatesIdentityLookupErrors(t *testing.T) {
	service := &Service{Store: &store.Store{}}
	_, err := service.CheckUser(context.Background(), "tenant-a", 1, []string{"0912141660"})
	if err == nil {
		t.Fatal("CheckUser() error = nil, want identity lookup error")
	}
	if !strings.Contains(err.Error(), "nil db") {
		t.Fatalf("CheckUser() error = %v, want identity lookup error", err)
	}
}
