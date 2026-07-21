package groosh

import (
	"math/big"
	"sort"
)

// Split divides m into count deterministic shares while preserving every
// minor unit. Any remainder is distributed one minor unit at a time from the
// first share onward, with the same sign as m.
func (m Money) Split(count int) ([]Money, error) {
	const op = "Money.Split"
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if count <= 0 {
		return nil, newError(op, "count", ErrInvalidAllocation, nil, "must be greater than zero")
	}

	divisor := int64(count)
	quotient := m.minorUnits / divisor
	remainder := m.minorUnits % divisor
	result := make([]Money, count)
	for i := range result {
		result[i] = Money{minorUnits: quotient, unit: m.unit}
	}

	step := int64(1)
	if remainder < 0 {
		step = -1
		remainder = -remainder
	}
	for i := int64(0); i < remainder; i++ {
		result[int(i)].minorUnits += step
	}
	return result, nil
}

// Allocate apportions m according to non-negative integer weights using the
// largest-remainder method. Ties are resolved by original index, making the
// result deterministic. At least one weight must be positive. The returned
// minor units always sum exactly to m.
func (m Money) Allocate(weights []uint64) ([]Money, error) {
	const op = "Money.Allocate"
	if err := m.Validate(); err != nil {
		return nil, err
	}
	if len(weights) == 0 {
		return nil, newError(op, "weights", ErrInvalidAllocation, nil, "at least one weight is required")
	}

	totalWeight := new(big.Int)
	for _, weight := range weights {
		totalWeight.Add(totalWeight, new(big.Int).SetUint64(weight))
	}
	if totalWeight.Sign() == 0 {
		return nil, newError(op, "weights", ErrInvalidAllocation, nil,
			"at least one weight must be positive")
	}

	type share struct {
		index     int
		quotient  *big.Int
		remainder *big.Int
	}

	absTotal := new(big.Int).SetUint64(int64Magnitude(m.minorUnits))
	shares := make([]share, len(weights))
	allocated := new(big.Int)
	for i, weight := range weights {
		product := new(big.Int).Mul(absTotal, new(big.Int).SetUint64(weight))
		quotient, remainder := new(big.Int), new(big.Int)
		quotient.QuoRem(product, totalWeight, remainder)
		shares[i] = share{index: i, quotient: quotient, remainder: remainder}
		allocated.Add(allocated, quotient)
	}

	leftover := new(big.Int).Sub(new(big.Int).Set(absTotal), allocated)
	if !leftover.IsInt64() || leftover.Int64() > int64(len(shares)) {
		return nil, newError(op, "weights", ErrInvalidAllocation, nil,
			"internal apportionment invariant failed")
	}
	order := append([]share(nil), shares...)
	sort.SliceStable(order, func(i, j int) bool {
		comparison := order[i].remainder.Cmp(order[j].remainder)
		if comparison == 0 {
			return order[i].index < order[j].index
		}
		return comparison > 0
	})
	for i := int64(0); i < leftover.Int64(); i++ {
		shares[order[i].index].quotient.Add(shares[order[i].index].quotient, big.NewInt(1))
	}

	negative := m.minorUnits < 0
	result := make([]Money, len(shares))
	for i := range shares {
		minor, ok := signedInt64(shares[i].quotient, negative)
		if !ok {
			return nil, overflowError(op)
		}
		result[i] = Money{minorUnits: minor, unit: m.unit}
	}
	return result, nil
}

// AllocateRatios is a descriptive alias for Allocate.
func (m Money) AllocateRatios(weights []uint64) ([]Money, error) {
	return m.Allocate(weights)
}

func signedInt64(magnitude *big.Int, negative bool) (int64, bool) {
	if !negative {
		if !magnitude.IsInt64() {
			return 0, false
		}
		return magnitude.Int64(), true
	}
	value := new(big.Int).Neg(new(big.Int).Set(magnitude))
	if !value.IsInt64() {
		return 0, false
	}
	return value.Int64(), true
}
