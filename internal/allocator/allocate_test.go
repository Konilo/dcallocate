package allocator

import (
	"math"
	"testing"
)

const eps = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < eps
}

func sumAmounts(allocs []Allocation) float64 {
	var s float64
	for _, a := range allocs {
		s += a.Amount
	}
	return s
}

func TestAllocate_TrivialBalanced(t *testing.T) {
	// Already at target; new money split proportionally.
	assets := []Asset{
		{Name: "A", Current: 100, Target: 0.5},
		{Name: "B", Current: 100, Target: 0.5},
	}
	got, err := Allocate(assets, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 allocations, got %d", len(got))
	}
	if !almostEqual(got[0].Amount, 50) || !almostEqual(got[1].Amount, 50) {
		t.Errorf("want 50/50, got %+v", got)
	}
	if got[0].Stuck || got[1].Stuck {
		t.Errorf("nothing should be stuck: %+v", got)
	}
}

func TestAllocate_OneOverTarget(t *testing.T) {
	// A over-allocated, B under: A stuck, B gets everything.
	assets := []Asset{
		{Name: "A", Current: 1000, Target: 0.5},
		{Name: "B", Current: 0, Target: 0.5},
	}
	got, err := Allocate(assets, 500)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got[0].Stuck || got[0].Amount != 0 {
		t.Errorf("A should be stuck with 0, got %+v", got[0])
	}
	if got[1].Stuck || !almostEqual(got[1].Amount, 500) {
		t.Errorf("B should get 500, got %+v", got[1])
	}
}

func TestAllocate_CascadingStick(t *testing.T) {
	// After sticking A, B becomes over-target too.
	// c_A=200 (t=0.3), c_B=200 (t=0.3), c_C=0 (t=0.4), amount=10
	// V=410. pass1: x_A=0.3*410-200=-77 → stick, x_B=-77 → stick, x_C=0.4*410-0=164 (positive, but pass restarts because of sticks).
	// pass2: sumActiveT=0.4, stuck=400, deficit=10 → x_C = (0.4/0.4)*10 - 0 = 10.
	assets := []Asset{
		{Name: "A", Current: 200, Target: 0.3},
		{Name: "B", Current: 200, Target: 0.3},
		{Name: "C", Current: 0, Target: 0.4},
	}
	got, err := Allocate(assets, 10)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got[0].Stuck || !got[1].Stuck {
		t.Errorf("A and B should be stuck: %+v", got)
	}
	if got[2].Stuck || !almostEqual(got[2].Amount, 10) {
		t.Errorf("C should get 10, got %+v", got[2])
	}
}

func TestAllocate_TwoStepCascade(t *testing.T) {
	// Pass 1 sticks A (severely over). Pass 2: B's recomputed share is too small,
	// so B also sticks. Pass 3: C absorbs everything.
	// c_A=1000 (t=0.4), c_B=900 (t=0.4), c_C=0 (t=0.2), amount=100.
	// V=2000.
	// pass1: x_A=0.4*2000-1000=-200→stick, x_B=0.4*2000-900=-100→stick, x_C=0.2*2000-0=400 (transient, will be redone).
	// pass2: stuck=1900, sumActiveT=0.2, deficit=100. x_C=(0.2/0.2)*100-0=100. Done.
	// Note: B was stuck in pass1 already because the original t_i*V-c_i is computed at start-of-pass.
	// All 3 cases (A, B, C) handled in 2 passes.
	assets := []Asset{
		{Name: "A", Current: 1000, Target: 0.4},
		{Name: "B", Current: 900, Target: 0.4},
		{Name: "C", Current: 0, Target: 0.2},
	}
	got, err := Allocate(assets, 100)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got[0].Stuck || !got[1].Stuck {
		t.Errorf("A and B should be stuck: %+v", got)
	}
	if !almostEqual(got[2].Amount, 100) {
		t.Errorf("C should get 100, got %g", got[2].Amount)
	}
}

func TestAllocate_SingleAsset(t *testing.T) {
	assets := []Asset{{Name: "A", Current: 100, Target: 1.0}}
	got, err := Allocate(assets, 50)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !almostEqual(got[0].Amount, 50) {
		t.Errorf("want 50, got %g", got[0].Amount)
	}
}

func TestAllocate_ZeroAmountBalanced(t *testing.T) {
	assets := []Asset{
		{Name: "A", Current: 100, Target: 0.5},
		{Name: "B", Current: 100, Target: 0.5},
	}
	got, err := Allocate(assets, 0)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !almostEqual(sumAmounts(got), 0) {
		t.Errorf("sum should be 0, got %g", sumAmounts(got))
	}
}

func TestAllocate_ZeroAmountUnbalanced(t *testing.T) {
	// With zero amount and an over-target asset, that asset sticks; the rest stays put.
	assets := []Asset{
		{Name: "A", Current: 100, Target: 0.4},
		{Name: "B", Current: 100, Target: 0.6},
	}
	got, err := Allocate(assets, 0)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got[0].Stuck {
		t.Errorf("A should be stuck at zero amount, got %+v", got[0])
	}
	// Sum of investments must always equal the contribution amount (=0 here).
	if !almostEqual(sumAmounts(got), 0) {
		t.Errorf("sum %g != 0", sumAmounts(got))
	}
}

func TestAllocate_TargetZero(t *testing.T) {
	// Asset with target 0 and positive current value (e.g. a "do not grow" cash bucket)
	// should always be stuck; full investment goes to the others.
	assets := []Asset{
		{Name: "Cash", Current: 1000, Target: 0.0},
		{Name: "Stock", Current: 0, Target: 1.0},
	}
	got, err := Allocate(assets, 200)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !got[0].Stuck {
		t.Errorf("Cash should be stuck: %+v", got[0])
	}
	if !almostEqual(got[1].Amount, 200) {
		t.Errorf("Stock should get 200, got %g", got[1].Amount)
	}
}

func TestAllocate_SumPreserved(t *testing.T) {
	// Sum of x_i must equal amount, and no x_i may be negative, in all cases.
	assets := []Asset{
		{Name: "A", Current: 1234, Target: 0.20},
		{Name: "B", Current: 567, Target: 0.30},
		{Name: "C", Current: 89, Target: 0.50},
	}
	amount := 1000.0
	got, err := Allocate(assets, amount)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !almostEqual(sumAmounts(got), amount) {
		t.Errorf("sum=%g want %g", sumAmounts(got), amount)
	}
	for _, a := range got {
		if a.Amount < 0 {
			t.Errorf("negative amount: %+v", a)
		}
	}
}

func TestAllocate_BadInputs(t *testing.T) {
	cases := []struct {
		name   string
		assets []Asset
		amount float64
	}{
		{"empty", []Asset{}, 100},
		{"negative-amount", []Asset{{Name: "A", Current: 0, Target: 1}}, -1},
		{"weights-not-summing", []Asset{
			{Name: "A", Current: 0, Target: 0.3},
			{Name: "B", Current: 0, Target: 0.3},
		}, 100},
		{"negative-current", []Asset{
			{Name: "A", Current: -1, Target: 0.5},
			{Name: "B", Current: 0, Target: 0.5},
		}, 100},
		{"negative-target", []Asset{
			{Name: "A", Current: 0, Target: -0.1},
			{Name: "B", Current: 0, Target: 1.1},
		}, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Allocate(tc.assets, tc.amount); err == nil {
				t.Errorf("expected error, got none")
			}
		})
	}
}

func TestAllocate_WeightsWithinTolerance(t *testing.T) {
	// Sum of weights slightly off but within tolerance must succeed.
	assets := []Asset{
		{Name: "A", Current: 100, Target: 0.5 + WeightSumTolerance/4},
		{Name: "B", Current: 100, Target: 0.5 - WeightSumTolerance/4},
	}
	if _, err := Allocate(assets, 100); err != nil {
		t.Errorf("unexpected error within tolerance: %v", err)
	}
}

func TestAllocateWithSelling_Balanced(t *testing.T) {
	// Already at target; amount split proportionally — same answer as Allocate.
	assets := []Asset{
		{Name: "A", Current: 100, Target: 0.5},
		{Name: "B", Current: 100, Target: 0.5},
	}
	got, err := AllocateWithSelling(assets, 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !almostEqual(got[0].Amount, 50) || !almostEqual(got[1].Amount, 50) {
		t.Errorf("want 50/50, got %+v", got)
	}
}

func TestAllocateWithSelling_NegativeDelta(t *testing.T) {
	// Mirrors the R study's rebalance-with-selling example:
	// v_pre=(28500,1500), tw=(0.9,0.1), C=1000 → deltas (-600, +1600).
	assets := []Asset{
		{Name: "A", Current: 28500, Target: 0.9},
		{Name: "B", Current: 1500, Target: 0.1},
	}
	got, err := AllocateWithSelling(assets, 1000)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !almostEqual(got[0].Amount, -600) {
		t.Errorf("A want -600, got %g", got[0].Amount)
	}
	if !almostEqual(got[1].Amount, 1600) {
		t.Errorf("B want 1600, got %g", got[1].Amount)
	}
	if !almostEqual(sumAmounts(got), 1000) {
		t.Errorf("sum want 1000, got %g", sumAmounts(got))
	}
}

func TestAllocateWithSelling_ZeroContribution(t *testing.T) {
	// Pure rebalance with no fresh cash: deltas land each on target; sum = 0.
	assets := []Asset{
		{Name: "A", Current: 600, Target: 0.5},
		{Name: "B", Current: 400, Target: 0.5},
	}
	got, err := AllocateWithSelling(assets, 0)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if !almostEqual(got[0].Amount, -100) || !almostEqual(got[1].Amount, 100) {
		t.Errorf("want -100/+100, got %+v", got)
	}
	if !almostEqual(sumAmounts(got), 0) {
		t.Errorf("sum want 0, got %g", sumAmounts(got))
	}
}

func TestAllocateWithSelling_NeverStuck(t *testing.T) {
	// Even with extreme drift, AllocateWithSelling does not mark anything stuck.
	assets := []Asset{
		{Name: "A", Current: 10000, Target: 0.1},
		{Name: "B", Current: 0, Target: 0.9},
	}
	got, err := AllocateWithSelling(assets, 100)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got[0].Stuck || got[1].Stuck {
		t.Errorf("AllocateWithSelling never sets Stuck: %+v", got)
	}
}
