package portfolio

import (
	"errors"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

const fixtureToday = "2026-04-30"

func openFixture(t *testing.T) *os.File {
	t.Helper()
	f, err := os.Open("../../testdata/portfolio_min.xml")
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func parseFixture(t *testing.T) *Node {
	t.Helper()
	today, _ := time.Parse("2006-01-02", fixtureToday)
	tree, err := ParseReaderAt(openFixture(t), "Asset Classes", today)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return tree
}

func almostEqual(a, b, tol float64) bool { return math.Abs(a-b) < tol }

func TestParse_Tree(t *testing.T) {
	tree := parseFixture(t)
	if tree.Name != "Asset Classes" {
		t.Errorf("root name = %q, want Asset Classes", tree.Name)
	}
	if got := len(tree.Children); got != 2 {
		t.Fatalf("root has %d children, want 2", got)
	}
	if tree.Children[0].Name != "Stocks" || tree.Children[1].Name != "Cash" {
		t.Errorf("taxonomy nodes: got [%s, %s], want [Stocks, Cash]",
			tree.Children[0].Name, tree.Children[1].Name)
	}
}

func TestParse_LeafValuesAndTargets(t *testing.T) {
	tree := parseFixture(t)
	leaves := tree.Leaves()
	if got := len(leaves); got != 2 {
		t.Fatalf("got %d leaves, want 2 (Stocks, Cash)", got)
	}

	// Stocks leaf: SecA (€55) + SecB (€50) = €105. Target 90% (no per-asset split).
	if leaves[0].Name != "Stocks" {
		t.Errorf("leaves[0].Name = %q", leaves[0].Name)
	}
	if !almostEqual(leaves[0].Current, 105.0, 1e-6) {
		t.Errorf("Stocks current = %g, want 105", leaves[0].Current)
	}
	if !almostEqual(leaves[0].Target, 0.90, 1e-9) {
		t.Errorf("Stocks target = %g, want 0.90", leaves[0].Target)
	}

	// Cash leaf: Cash1 = €895. Target 10%.
	if leaves[1].Name != "Cash" {
		t.Errorf("leaves[1].Name = %q", leaves[1].Name)
	}
	if !almostEqual(leaves[1].Current, 895.0, 1e-6) {
		t.Errorf("Cash current = %g, want 895", leaves[1].Current)
	}
	if !almostEqual(leaves[1].Target, 0.10, 1e-9) {
		t.Errorf("Cash target = %g, want 0.10", leaves[1].Target)
	}
}

func TestParse_LeafAssignments(t *testing.T) {
	leaves := parseFixture(t).Leaves()

	// Stocks contains SecA (€55, security) + SecB (€50, security).
	if got := len(leaves[0].Assignments); got != 2 {
		t.Fatalf("Stocks assignments: got %d, want 2", got)
	}
	if leaves[0].Assignments[0] != (Assignment{Name: "SecA", Kind: "security", Current: 55}) {
		t.Errorf("Stocks.Assignments[0] = %+v", leaves[0].Assignments[0])
	}
	if leaves[0].Assignments[1] != (Assignment{Name: "SecB", Kind: "security", Current: 50}) {
		t.Errorf("Stocks.Assignments[1] = %+v", leaves[0].Assignments[1])
	}

	// Cash contains Cash1 (€895, account).
	if got := len(leaves[1].Assignments); got != 1 {
		t.Fatalf("Cash assignments: got %d, want 1", got)
	}
	if leaves[1].Assignments[0] != (Assignment{Name: "Cash1", Kind: "account", Current: 895}) {
		t.Errorf("Cash.Assignments[0] = %+v", leaves[1].Assignments[0])
	}
}

func TestParse_Rollup(t *testing.T) {
	tree := parseFixture(t)
	if !almostEqual(tree.Current, 1000.0, 1e-6) {
		t.Errorf("root current rollup = %g, want 1000", tree.Current)
	}
	if !almostEqual(tree.Target, 1.0, 1e-9) {
		t.Errorf("root target rollup = %g, want 1.0", tree.Target)
	}
}

func TestParse_TargetsSumToOne(t *testing.T) {
	leaves := parseFixture(t).Leaves()
	var sum float64
	for _, l := range leaves {
		sum += l.Target
	}
	if !almostEqual(sum, 1.0, 1e-9) {
		t.Errorf("sum of leaf targets = %g, want 1", sum)
	}
}

func TestParse_TaxonomyNotFound(t *testing.T) {
	today, _ := time.Parse("2006-01-02", fixtureToday)
	_, err := ParseReaderAt(openFixture(t), "Industries", today)
	var tnf *TaxonomyNotFoundError
	if !errors.As(err, &tnf) {
		t.Fatalf("got %v (%T), want TaxonomyNotFoundError", err, err)
	}
	if len(tnf.Available) != 1 || tnf.Available[0] != "Asset Classes" {
		t.Errorf("Available = %v, want [Asset Classes]", tnf.Available)
	}
}

func TestParse_NonEURBaseCurrency(t *testing.T) {
	// Swap every "EUR" → "USD" throughout the fixture: the whole portfolio is
	// then USD-based and should parse cleanly with BaseCurrency = "USD".
	xmlText := strings.ReplaceAll(readFile(t, "../../testdata/portfolio_min.xml"), "EUR", "USD")
	today, _ := time.Parse("2006-01-02", fixtureToday)
	tree, err := ParseReaderAt(strings.NewReader(xmlText), "Asset Classes", today)
	if err != nil {
		t.Fatalf("USD-base portfolio failed to parse: %v", err)
	}
	if tree.BaseCurrency != "USD" {
		t.Errorf("BaseCurrency = %q, want USD", tree.BaseCurrency)
	}
	// Same numeric structure as the EUR fixture.
	if !almostEqual(tree.Current, 1000.0, 1e-6) {
		t.Errorf("USD root current = %g, want 1000", tree.Current)
	}
}

func TestParse_NonEURSecurity(t *testing.T) {
	xmlText := strings.Replace(
		readFile(t, "../../testdata/portfolio_min.xml"),
		"<currencyCode>EUR</currencyCode>\n      <prices>\n        <price t=\"2026-04-30\" v=\"500000000\"/>",
		"<currencyCode>USD</currencyCode>\n      <prices>\n        <price t=\"2026-04-30\" v=\"500000000\"/>",
		1,
	)
	today, _ := time.Parse("2006-01-02", fixtureToday)
	_, err := ParseReaderAt(strings.NewReader(xmlText), "Asset Classes", today)
	if err == nil || !strings.Contains(err.Error(), "USD") {
		t.Errorf("expected non-EUR error mentioning USD, got %v", err)
	}
}

func TestParse_UnknownTransactionType(t *testing.T) {
	xmlText := strings.Replace(
		readFile(t, "../../testdata/portfolio_min.xml"),
		"<type>DEPOSIT</type>",
		"<type>WEIRD_NEW_TYPE</type>",
		1,
	)
	today, _ := time.Parse("2006-01-02", fixtureToday)
	_, err := ParseReaderAt(strings.NewReader(xmlText), "Asset Classes", today)
	if err == nil || !strings.Contains(err.Error(), "WEIRD_NEW_TYPE") {
		t.Errorf("expected unknown-type error, got %v", err)
	}
}

func TestParse_BadWeightSum(t *testing.T) {
	xmlText := strings.Replace(
		readFile(t, "../../testdata/portfolio_min.xml"),
		"<weight>9000</weight>",
		"<weight>5000</weight>",
		1,
	)
	today, _ := time.Parse("2006-01-02", fixtureToday)
	_, err := ParseReaderAt(strings.NewReader(xmlText), "Asset Classes", today)
	if err == nil || !strings.Contains(err.Error(), "leaf target weights") {
		t.Errorf("expected bad-weight-sum error, got %v", err)
	}
}

func TestParse_AssignmentOnNonLeafNode(t *testing.T) {
	// Attach a security assignment to the root taxonomy node (which has
	// children). dcallocate must reject this rather than silently dropping
	// the security from totals and the allocation solve.
	xmlText := strings.Replace(
		readFile(t, "../../testdata/portfolio_min.xml"),
		"<assignments/>",
		`<assignments>
          <assignment>
            <investmentVehicle class="security" reference="1"/>
            <weight>10000</weight>
            <rank>0</rank>
          </assignment>
        </assignments>`,
		1,
	)
	today, _ := time.Parse("2006-01-02", fixtureToday)
	_, err := ParseReaderAt(strings.NewReader(xmlText), "Asset Classes", today)
	if err == nil {
		t.Fatalf("expected error for assignment on non-leaf taxonomy node, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"non-leaf", "Asset Classes", "SecA"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q should mention %q", msg, want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestLatestPrice_PrefersLatestWhenOnOrAfterLastPrice(t *testing.T) {
	today, _ := time.Parse("2006-01-02", "2026-05-23")
	hist := priceXML{T: "2026-05-23", V: 6465146880000}   // 64651.4688
	latest := priceXML{T: "2026-05-23", V: 6505221000000} // 65052.2100

	cases := []struct {
		name     string
		security securityXML
		wantV    float64
		wantDate string
	}{
		{
			name: "latest same date as last price -> latest wins (tie)",
			security: securityXML{
				Prices: pricesXML{Items: []priceXML{hist}},
				Latest: &latest,
			},
			wantV:    65052.2100,
			wantDate: "2026-05-23",
		},
		{
			name: "latest newer than last price -> latest wins",
			security: securityXML{
				Prices: pricesXML{Items: []priceXML{{T: "2026-05-22", V: 6502844140000}}},
				Latest: &latest,
			},
			wantV:    65052.2100,
			wantDate: "2026-05-23",
		},
		{
			name: "latest older than last price -> last price wins",
			security: securityXML{
				Prices: pricesXML{Items: []priceXML{hist}},
				Latest: &priceXML{T: "2026-05-22", V: 6502844140000},
			},
			wantV:    64651.4688,
			wantDate: "2026-05-23",
		},
		{
			name: "no latest -> last price wins",
			security: securityXML{
				Prices: pricesXML{Items: []priceXML{hist}},
			},
			wantV:    64651.4688,
			wantDate: "2026-05-23",
		},
		{
			name: "no prices but latest present -> latest wins",
			security: securityXML{
				Latest: &latest,
			},
			wantV:    65052.2100,
			wantDate: "2026-05-23",
		},
		{
			name: "latest in the future -> ignored, fall back to last price",
			security: securityXML{
				Prices: pricesXML{Items: []priceXML{hist}},
				Latest: &priceXML{T: "2026-05-24", V: 9999999999999},
			},
			wantV:    64651.4688,
			wantDate: "2026-05-23",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, d, err := latestPrice(&c.security, today)
			if err != nil {
				t.Fatalf("latestPrice: %v", err)
			}
			if !almostEqual(v, c.wantV, 1e-4) {
				t.Errorf("price = %.4f, want %.4f", v, c.wantV)
			}
			if got := d.Format("2006-01-02"); got != c.wantDate {
				t.Errorf("date = %s, want %s", got, c.wantDate)
			}
		})
	}
}

func TestLatestPrice_NoUsablePriceErrors(t *testing.T) {
	today, _ := time.Parse("2006-01-02", "2026-05-23")
	if _, _, err := latestPrice(&securityXML{}, today); err == nil {
		t.Errorf("expected error for security with no prices and no latest")
	}
	if _, _, err := latestPrice(&securityXML{
		Prices: pricesXML{Items: []priceXML{{T: "2026-05-24", V: 1}}},
	}, today); err == nil {
		t.Errorf("expected error when all prices are after today and no latest")
	}
}

func TestParse_XPathParity(t *testing.T) {
	// The two fixtures encode the same portfolio in PP's two save formats.
	// After parsing, every node and every leaf assignment must match exactly.
	today, _ := time.Parse("2006-01-02", fixtureToday)
	idFile, err := os.Open("../../testdata/portfolio_min.xml")
	if err != nil {
		t.Fatalf("open id fixture: %v", err)
	}
	t.Cleanup(func() { idFile.Close() })
	xpathFile, err := os.Open("../../testdata/portfolio_min_xpath.xml")
	if err != nil {
		t.Fatalf("open xpath fixture: %v", err)
	}
	t.Cleanup(func() { xpathFile.Close() })

	idTree, err := ParseReaderAt(idFile, "Asset Classes", today)
	if err != nil {
		t.Fatalf("parse id fixture: %v", err)
	}
	xpathTree, err := ParseReaderAt(xpathFile, "Asset Classes", today)
	if err != nil {
		t.Fatalf("parse xpath fixture: %v", err)
	}

	compareNodes(t, "", idTree, xpathTree)
}

// compareNodes walks two parsed trees in parallel and reports the first
// structural or numeric divergence with a path. Tolerance on Current and
// Target is the same we use elsewhere (1e-6 / 1e-9 respectively).
func compareNodes(t *testing.T, path string, a, b *Node) {
	t.Helper()
	here := path + "/" + a.Name
	if a.Name != b.Name {
		t.Errorf("%s: name mismatch: id=%q xpath=%q", path, a.Name, b.Name)
	}
	if !almostEqual(a.Current, b.Current, 1e-6) {
		t.Errorf("%s: Current mismatch: id=%g xpath=%g", here, a.Current, b.Current)
	}
	if !almostEqual(a.Target, b.Target, 1e-9) {
		t.Errorf("%s: Target mismatch: id=%g xpath=%g", here, a.Target, b.Target)
	}
	if len(a.Children) != len(b.Children) {
		t.Fatalf("%s: child count mismatch: id=%d xpath=%d", here, len(a.Children), len(b.Children))
	}
	for i := range a.Children {
		compareNodes(t, here, a.Children[i], b.Children[i])
	}
	if len(a.Assignments) != len(b.Assignments) {
		t.Fatalf("%s: assignment count mismatch: id=%d xpath=%d", here, len(a.Assignments), len(b.Assignments))
	}
	for i := range a.Assignments {
		if a.Assignments[i].Name != b.Assignments[i].Name ||
			a.Assignments[i].Kind != b.Assignments[i].Kind ||
			!almostEqual(a.Assignments[i].Current, b.Assignments[i].Current, 1e-6) {
			t.Errorf("%s: assignment[%d] mismatch: id=%+v xpath=%+v",
				here, i, a.Assignments[i], b.Assignments[i])
		}
	}
}
