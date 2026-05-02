// Package allocator computes per-asset allocation of a contribution. Two modes:
//
//   - Allocate (default, rebalance-by-investing): water-filling with x_i >= 0.
//     Assets already at or above their target share are stuck (receive 0); the
//     contribution flows to the rest in proportion to their renormalised target
//     weights. Iterative — at most N passes.
//
//   - AllocateWithSelling: closed-form rebalance allowing x_i < 0 (a sell).
//     Each x_i = t_i * (sum(c) + amount) - c_i; sum(x_i) = amount; weights
//     land exactly on target in one pass. Use when selling is permitted.
//
// Water-filling algorithm (projection onto the simplex with x >= 0):
//
//	V = sum(c_i) + amount
//	loop (at most N iterations):
//	  for i not stuck: x_i = (t_i / sum_active(t)) * (V - sum_stuck(c)) - c_i
//	  if any x_i < 0: stick those (x_i := 0), repeat
//	  else: done
//
// Termination: at each pass at least one asset is added to the stuck set
// (otherwise we exit), so the loop runs at most N times.
package allocator

import (
	"errors"
	"fmt"
	"math"
)

// WeightSumTolerance is the allowed deviation when checking that target
// weights sum to 1.0.
const WeightSumTolerance = 1e-4

// Asset is one entry in the portfolio with its current value and target weight.
type Asset struct {
	Name    string
	Current float64
	Target  float64
}

// Allocation is the per-asset result of water-filling.
type Allocation struct {
	Asset
	// Amount is how much of the contribution to invest in this asset.
	Amount float64
	// Stuck is true if the asset was already at or above its target share
	// of the post-contribution total and therefore receives nothing.
	Stuck bool
}

// Allocate computes how to split `amount` across `assets` so that the resulting
// weights converge toward the targets, never selling any asset (water-filling
// with x_i >= 0). For the selling-allowed variant, see AllocateWithSelling.
//
// The function is pure: no I/O, no globals.
func Allocate(assets []Asset, amount float64) ([]Allocation, error) {
	sumCurrent, err := validate(assets, amount)
	if err != nil {
		return nil, err
	}

	n := len(assets)
	result := make([]Allocation, n)
	for i, a := range assets {
		result[i] = Allocation{Asset: a}
	}

	v := sumCurrent + amount

	// At most n+1 passes: each non-trivial pass sticks at least one asset.
	for pass := 0; pass <= n; pass++ {
		var sumActiveT, sumStuckC float64
		for _, r := range result {
			if r.Stuck {
				sumStuckC += r.Current
			} else {
				sumActiveT += r.Target
			}
		}
		if sumActiveT == 0 {
			// Sum of weights is 1.0 and amount >= 0, so V >= sum_stuck_c
			// (proof: if every asset were stuck, sum c_i > sum t_i * V = V,
			// i.e. amount < 0, contradicting the precondition). So we should
			// never get here when at least one t_i > 0 and amount >= 0.
			return nil, errors.New("allocator: no asset left to absorb the contribution (degenerate inputs)")
		}

		deficit := v - sumStuckC
		anyNegative := false
		for i := range result {
			if result[i].Stuck {
				continue
			}
			desired := (result[i].Target / sumActiveT) * deficit
			x := desired - result[i].Current
			if x < 0 {
				result[i].Stuck = true
				result[i].Amount = 0
				anyNegative = true
			} else {
				result[i].Amount = x
			}
		}
		if !anyNegative {
			return result, nil
		}
	}
	return nil, errors.New("allocator: water-filling did not converge (unexpected)")
}

// AllocateWithSelling computes the per-asset deltas needed to land exactly on
// the target weights using the contribution `amount`, allowing each delta to
// be negative (a sell). Sum of deltas equals amount.
//
// Closed-form: x_i = t_i * (sum(c) + amount) - c_i. The Stuck flag is always
// false in the result — every asset participates.
//
// Use this when selling is permitted (e.g. annual rebalance). For the
// no-selling case, use Allocate.
func AllocateWithSelling(assets []Asset, amount float64) ([]Allocation, error) {
	sumCurrent, err := validate(assets, amount)
	if err != nil {
		return nil, err
	}
	v := sumCurrent + amount
	result := make([]Allocation, len(assets))
	for i, a := range assets {
		result[i] = Allocation{
			Asset:  a,
			Amount: a.Target*v - a.Current,
		}
	}
	return result, nil
}

// validate runs the input checks shared by Allocate and AllocateWithSelling.
// Returns the sum of current values on success.
func validate(assets []Asset, amount float64) (float64, error) {
	if len(assets) == 0 {
		return 0, errors.New("allocator: at least one asset required")
	}
	if amount < 0 {
		return 0, fmt.Errorf("allocator: amount must be >= 0, got %g", amount)
	}
	var sumWeights, sumCurrent float64
	for _, a := range assets {
		if a.Current < 0 {
			return 0, fmt.Errorf("allocator: asset %q has negative current value %g", a.Name, a.Current)
		}
		if a.Target < 0 {
			return 0, fmt.Errorf("allocator: asset %q has negative target weight %g", a.Name, a.Target)
		}
		sumWeights += a.Target
		sumCurrent += a.Current
	}
	if math.Abs(sumWeights-1.0) > WeightSumTolerance {
		return 0, fmt.Errorf("allocator: target weights sum to %g, expected 1.0 (tol %g)", sumWeights, WeightSumTolerance)
	}
	return sumCurrent, nil
}
