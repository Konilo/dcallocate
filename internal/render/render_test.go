package render

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Konilo/dcallocate/internal/portfolio"
)

func TestBindingBand(t *testing.T) {
	cases := []struct {
		target, lo, hi float64
	}{
		{0.00, 0.0000, 0.0000},
		{0.05, 0.0375, 0.0625},
		{0.10, 0.0750, 0.1250},
		{0.20, 0.1500, 0.2500},
		{0.50, 0.4500, 0.5500},
		{0.76, 0.7100, 0.8100},
		{1.00, 0.9500, 1.0500},
	}
	const eps = 1e-9
	for _, c := range cases {
		lo, hi := bindingBand(c.target)
		if math.Abs(lo-c.lo) > eps || math.Abs(hi-c.hi) > eps {
			t.Errorf("bindingBand(%g) = (%g, %g); want (%g, %g)",
				c.target, lo, hi, c.lo, c.hi)
		}
	}
}

// makeTree builds a synthetic two-leaf portfolio (root with leaves A and B)
// with the given current values and 0.5/0.5 target weights. amount is split
// to investAmt on A (rest on B) and Rollup is run so the root reflects sums.
func makeTree(currA, currB, investA, investB float64) *portfolio.Node {
	a := &portfolio.Node{Name: "A", Current: currA, Target: 0.5, Investment: investA}
	b := &portfolio.Node{Name: "B", Current: currB, Target: 0.5, Investment: investB}
	root := &portfolio.Node{Name: "Root", BaseCurrency: "EUR", Children: []*portfolio.Node{a, b}}
	root.Rollup()
	return root
}

func TestTree_BandCheck_Breach(t *testing.T) {
	// 100 EUR in A, 0 EUR in B → 100% A, 0% B. Contribute 0 so post % stays
	// at the unbalanced split. Both leaves breach their 50% target with
	// band [0.45, 0.55]. Without color, the only signal is the warning footer.
	root := makeTree(100, 0, 0, 0)
	var buf bytes.Buffer
	Tree(&buf, root, 0, false, true)
	out := buf.String()

	if !strings.Contains(out, "Portfolio is unbalanced") {
		t.Errorf("expected warning footer; got:\n%s", out)
	}
	if !strings.Contains(out, "2 nodes outside 5/25 band: A, B") {
		t.Errorf("expected footer to list both nodes; got:\n%s", out)
	}
}

func TestTree_BandCheck_NoBreach(t *testing.T) {
	// 50 EUR in A, 50 EUR in B → exactly on target. No breaches, no warning.
	root := makeTree(50, 50, 0, 0)
	var buf bytes.Buffer
	Tree(&buf, root, 0, false, true)
	out := buf.String()

	if strings.Contains(out, "Portfolio is unbalanced") {
		t.Errorf("did not expect warning footer; got:\n%s", out)
	}
}

func TestJSON_BreachFieldsPerNode(t *testing.T) {
	// 100 in A, 0 in B, no contribution → both leaves breach their 0.5 target.
	root := makeTree(100, 0, 0, 0)
	var buf bytes.Buffer
	if err := JSON(&buf, root, 0); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Amount       float64 `json:"amount"`
		CurrentTotal float64 `json:"currentTotal"`
		PostTotal    float64 `json:"postTotal"`
		Root         struct {
			Breach   bool `json:"breach"`
			BandLo   *float64
			BandHi   *float64
			Children []struct {
				Name             string  `json:"name"`
				PostContribution float64 `json:"postContribution"`
				BandLo           float64 `json:"bandLo"`
				BandHi           float64 `json:"bandHi"`
				Breach           bool    `json:"breach"`
			} `json:"children"`
		} `json:"root"`
		Breaches []string `json:"breaches"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, buf.String())
	}
	if got.CurrentTotal != 100 || got.PostTotal != 100 {
		t.Errorf("totals: got currentTotal=%v postTotal=%v; want 100/100", got.CurrentTotal, got.PostTotal)
	}
	if got.Root.Breach {
		t.Errorf("root should never report a breach; got true")
	}
	if got.Root.BandLo != nil || got.Root.BandHi != nil {
		t.Errorf("root band fields should be omitted; got lo=%v hi=%v", got.Root.BandLo, got.Root.BandHi)
	}
	if len(got.Root.Children) != 2 {
		t.Fatalf("expected 2 children; got %d", len(got.Root.Children))
	}
	a, b := got.Root.Children[0], got.Root.Children[1]
	if a.Name != "A" || b.Name != "B" {
		t.Errorf("child names: got %q,%q; want A,B", a.Name, b.Name)
	}
	const eps = 1e-9
	if math.Abs(a.PostContribution-1.0) > eps || math.Abs(b.PostContribution-0.0) > eps {
		t.Errorf("postContribution: got %v,%v; want 1.0,0.0", a.PostContribution, b.PostContribution)
	}
	if math.Abs(a.BandLo-0.45) > eps || math.Abs(a.BandHi-0.55) > eps {
		t.Errorf("A band: got [%v,%v]; want [0.45,0.55]", a.BandLo, a.BandHi)
	}
	if !a.Breach || !b.Breach {
		t.Errorf("both leaves should breach; got A=%v B=%v", a.Breach, b.Breach)
	}
	if len(got.Breaches) != 2 || got.Breaches[0] != "A" || got.Breaches[1] != "B" {
		t.Errorf("breaches list: got %v; want [A B]", got.Breaches)
	}
}

func TestTree_BandCheckOff(t *testing.T) {
	// Same unbalanced tree as the breach test, but bandCheck off: no extra
	// columns, no marker, no warning. Header should not contain "post %" or
	// "5/25 band".
	root := makeTree(100, 0, 0, 0)
	var buf bytes.Buffer
	Tree(&buf, root, 0, false, false)
	out := buf.String()

	if strings.Contains(out, "post %") || strings.Contains(out, "5/25 band") {
		t.Errorf("bandCheck=false should omit new columns; got:\n%s", out)
	}
	if strings.Contains(out, "Portfolio is unbalanced") {
		t.Errorf("bandCheck=false should suppress warning footer; got:\n%s", out)
	}
}

// assertRowsAligned walks every full-width row in out and checks two
// invariants: (1) the row is exactly wantWidth runes wide; (2) the 2-space
// inter-cell separator is intact at each of sepCols. Skips lines that
// shouldn't fill the table width: the horizontal "─" separators, the
// "Total contributed" line, the optional "! Portfolio is unbalanced..."
// warning, and the empty trailing line.
func assertRowsAligned(t *testing.T, out string, wantWidth int, sepCols []int) {
	t.Helper()
	for i, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" ||
			strings.HasPrefix(line, "Total contributed") ||
			strings.HasPrefix(line, "! Portfolio") {
			continue
		}
		// Horizontal separator: all "─" runes.
		runes := []rune(line)
		allSep := len(runes) > 0
		for _, r := range runes {
			if r != '─' {
				allSep = false
				break
			}
		}
		if allSep {
			// The separator is still expected to span the table width.
			if utf8.RuneCountInString(line) != wantWidth {
				t.Errorf("line %d (separator) width = %d runes, want %d", i, len(runes), wantWidth)
			}
			continue
		}
		if len(runes) != wantWidth {
			t.Errorf("line %d width = %d runes, want %d:\n%s", i, len(runes), wantWidth, line)
			continue
		}
		for _, c := range sepCols {
			if runes[c] != ' ' || runes[c+1] != ' ' {
				t.Errorf("line %d: expected 2-space separator at cols %d-%d, got %q in:\n%s",
					i, c, c+1, string(runes[c:c+2]), line)
			}
		}
	}
}

// alignmentFixture returns a 4-leaf root with no Assignments (so every
// rendered row is a full-width data row, not a name+current assignment
// row). Targets are chosen to exercise edge-case band widths:
//   - 0.95 → band hi reaches 100% ("90.00-100.00", the 12-char max-width case)
//   - 0.04 → absolute-5pp band
//   - 0.005 ×2 → relative-25% band on a very small target
func alignmentFixture() *portfolio.Node {
	leaves := []*portfolio.Node{
		{Name: "Stocks", Current: 95, Target: 0.95},
		{Name: "Bonds", Current: 4, Target: 0.04},
		{Name: "Cash", Current: 0.5, Target: 0.005},
		{Name: "Reserve", Current: 0.5, Target: 0.005},
	}
	root := &portfolio.Node{
		Name:         "Asset Classes",
		BaseCurrency: "EUR",
		Children:     leaves,
	}
	root.Rollup()
	return root
}

func TestTree_ColumnAlignment_BandCheckOn(t *testing.T) {
	// 7-column layout: 44 + 2 + 14 + 2 + 8 + 2 + 8 + 2 + 9 + 2 + 12 + 2 + 14 = 121.
	root := alignmentFixture()
	var buf bytes.Buffer
	Tree(&buf, root, 0, false, true)
	assertRowsAligned(t, buf.String(), 121, []int{44, 60, 70, 80, 91, 105})
}

func TestTree_ColumnAlignment_BandCheckOff(t *testing.T) {
	// 5-column layout: 44 + 2 + 14 + 2 + 8 + 2 + 8 + 2 + 14 = 96.
	root := alignmentFixture()
	var buf bytes.Buffer
	Tree(&buf, root, 0, false, false)
	assertRowsAligned(t, buf.String(), 96, []int{44, 60, 70, 80})
}
