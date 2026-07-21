package groosh_test

import (
	"errors"
	"math"
	"math/big"
	"reflect"
	"testing"

	"github.com/adonese/noebs/groosh"
)

func minorValues(values []groosh.Money) []int64 {
	result := make([]int64, len(values))
	for i := range values {
		result[i] = values[i].MinorUnits()
	}
	return result
}

func TestSplitPreservesPositiveNegativeAndSmallTotals(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	for _, tt := range []struct {
		total int64
		count int
		want  []int64
	}{
		{10, 3, []int64{4, 3, 3}},
		{-10, 3, []int64{-4, -3, -3}},
		{2, 5, []int64{1, 1, 0, 0, 0}},
		{-2, 5, []int64{-1, -1, 0, 0, 0}},
		{0, 3, []int64{0, 0, 0}},
	} {
		got, err := mustMoney(t, tt.total, unit).Split(tt.count)
		if err != nil || !reflect.DeepEqual(minorValues(got), tt.want) {
			t.Errorf("Split(%d, %d) = %v, %v; want %v", tt.total, tt.count, minorValues(got), err, tt.want)
		}
		for i := range got {
			if got[i].Unit() != unit {
				t.Errorf("share %d lost its unit", i)
			}
		}
	}
	for _, count := range []int{0, -1} {
		if _, err := mustMoney(t, 1, unit).Split(count); !errors.Is(err, groosh.ErrInvalidAllocation) {
			t.Errorf("Split count %d error = %v", count, err)
		}
	}
}

func TestAllocateUsesDeterministicLargestRemainder(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	for _, tt := range []struct {
		total   int64
		weights []uint64
		want    []int64
	}{
		{10, []uint64{1, 2, 3}, []int64{2, 3, 5}},
		{-10, []uint64{1, 2, 3}, []int64{-2, -3, -5}},
		{2, []uint64{1, 1, 1}, []int64{1, 1, 0}},
		{-2, []uint64{1, 1, 1}, []int64{-1, -1, 0}},
		{7, []uint64{0, 1, 0}, []int64{0, 7, 0}},
		{0, []uint64{1, 0, 2}, []int64{0, 0, 0}},
	} {
		weightsBefore := append([]uint64(nil), tt.weights...)
		money := mustMoney(t, tt.total, unit)
		got, err := money.Allocate(tt.weights)
		if err != nil || !reflect.DeepEqual(minorValues(got), tt.want) {
			t.Errorf("Allocate(%d, %v) = %v, %v; want %v", tt.total, tt.weights, minorValues(got), err, tt.want)
		}
		if !reflect.DeepEqual(tt.weights, weightsBefore) {
			t.Fatal("Allocate mutated weights")
		}
		alias, err := money.AllocateRatios(tt.weights)
		if err != nil || !reflect.DeepEqual(alias, got) {
			t.Errorf("AllocateRatios differs: %v, %v", alias, err)
		}
	}
}

func TestAllocateRejectsMissingOrAllZeroWeights(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	money := mustMoney(t, 1, unit)
	for _, weights := range [][]uint64{nil, {}, {0}, {0, 0, 0}} {
		if _, err := money.Allocate(weights); !errors.Is(err, groosh.ErrInvalidAllocation) {
			t.Errorf("Allocate(%v) error = %v", weights, err)
		}
	}
}

func TestAllocatePreservesInt64ExtremesWithoutIntermediateOverflow(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	for _, total := range []int64{math.MinInt64, math.MaxInt64} {
		got, err := mustMoney(t, total, unit).Allocate([]uint64{math.MaxUint64, math.MaxUint64, 1, 0})
		if err != nil {
			t.Fatalf("Allocate(%d): %v", total, err)
		}
		sum := new(big.Int)
		for i := range got {
			sum.Add(sum, big.NewInt(got[i].MinorUnits()))
		}
		if sum.Cmp(big.NewInt(total)) != 0 {
			t.Fatalf("shares %v sum to %s, want %d", minorValues(got), sum, total)
		}
	}
}

func TestAllocateInt64ExtremeSharesAreExact(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	tests := []struct {
		total   int64
		weights []uint64
		want    []int64
	}{
		{math.MinInt64, []uint64{math.MaxUint64}, []int64{math.MinInt64}},
		{math.MinInt64, []uint64{0, math.MaxUint64}, []int64{0, math.MinInt64}},
		{math.MaxInt64, []uint64{math.MaxUint64, math.MaxUint64},
			[]int64{4611686018427387904, 4611686018427387903}},
	}
	for _, tt := range tests {
		got, err := mustMoney(t, tt.total, unit).Allocate(tt.weights)
		if err != nil || !reflect.DeepEqual(minorValues(got), tt.want) {
			t.Errorf("Allocate(%d, %v) = %v, %v; want %v",
				tt.total, tt.weights, minorValues(got), err, tt.want)
		}
	}
}

func TestSplitInt64EndpointsPreserveOrderingAndTotal(t *testing.T) {
	unit := testUnit(t, "USD", 1, 2)
	for _, total := range []int64{math.MinInt64, math.MaxInt64} {
		parts, err := mustMoney(t, total, unit).Split(7)
		if err != nil {
			t.Fatal(err)
		}
		sum := new(big.Int)
		for i := range parts {
			sum.Add(sum, big.NewInt(parts[i].MinorUnits()))
			if i > 0 {
				difference := new(big.Int).Sub(big.NewInt(parts[i-1].MinorUnits()), big.NewInt(parts[i].MinorUnits()))
				if new(big.Int).Abs(difference).Cmp(big.NewInt(1)) > 0 {
					t.Fatalf("adjacent shares differ by more than one: %v", minorValues(parts))
				}
				if total > 0 && parts[i-1].MinorUnits() < parts[i].MinorUnits() {
					t.Fatalf("positive remainder order is not deterministic: %v", minorValues(parts))
				}
				if total < 0 && parts[i-1].MinorUnits() > parts[i].MinorUnits() {
					t.Fatalf("negative remainder order is not deterministic: %v", minorValues(parts))
				}
			}
		}
		if sum.Cmp(big.NewInt(total)) != 0 {
			t.Fatalf("Split(%d) sum = %s", total, sum)
		}
	}
}

func TestAllocationOnInvalidMoneyFails(t *testing.T) {
	var money groosh.Money
	if _, err := money.Split(2); !errors.Is(err, groosh.ErrInvalidMoney) {
		t.Fatalf("Split invalid money error = %v", err)
	}
	if _, err := money.Allocate([]uint64{1}); !errors.Is(err, groosh.ErrInvalidMoney) {
		t.Fatalf("Allocate invalid money error = %v", err)
	}
}
