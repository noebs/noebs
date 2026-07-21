package store

import (
	"context"
	"testing"
	"time"
)

func testCurrencyUnitID(t *testing.T, ctx context.Context, walletStore *Store, currency string) int64 {
	t.Helper()
	unit, err := walletStore.GetCurrencyUnit(ctx, currency, time.Now().UTC())
	if err != nil {
		t.Fatalf("resolve current %s currency unit: %v", currency, err)
	}
	if unit.ID <= 0 {
		t.Fatalf("current %s currency unit id = %d, want positive", currency, unit.ID)
	}
	return unit.ID
}
