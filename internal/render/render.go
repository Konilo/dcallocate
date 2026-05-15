// Package render formats an annotated portfolio tree for display (pretty
// tree to a TTY) or for machine consumption (JSON).
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/Konilo/dcallocate/internal/portfolio"
)

const (
	ansiReset = "\x1b[0m"
	ansiDim   = "\x1b[2m"
	ansiBold  = "\x1b[1m"
	ansiRed   = "\x1b[31m"

	nameColWidth = 44
)

// Tree writes a human-readable tree of the portfolio + computed allocations
// to w. If color is true, ANSI escape codes are emitted (header & footer
// bold; tree glyphs dim where they lead only to stuck subtrees). Money
// columns are labelled with the portfolio's base currency, read from
// root.BaseCurrency.
//
// If bandCheck is true (default in the CLI), two extra columns are shown —
// post-contribution % and the node's 5/25 binding band — and a warning is
// printed when any node ends up outside its band.
//
// Highlighting rules:
//
//   - A continuation bar `│   ` represents a vertical line carrying down to
//     a *later sibling* of some ancestor — it is bright iff at least one of
//     that ancestor's later siblings contains an unstuck leaf.
//   - The connector `├── n` is split: the `├` glyph has a right arm reaching
//     n itself and a downward stem reaching n's later siblings, so it is
//     bright iff either n is unstuck or some later sibling contains an
//     unstuck leaf. The `── ` stub plus n's name follow n's own stuck
//     status.
//   - The connector `└── n` has no downward stem, so the whole glyph follows
//     n.Stuck.
//
// Inner nodes' Stuck rollup means Stuck == false ⇔ subtree has an unstuck
// leaf, so checking a sibling's Stuck flag is sufficient.
func Tree(w io.Writer, root *portfolio.Node, amount float64, color, bandCheck bool) {
	rootCurrent := root.Current
	postTotal := rootCurrent + amount
	ccy := root.BaseCurrency

	// Body line column widths: name (nameColWidth) + 2sp + 14 (10.2f " <ccy>")
	// + 2sp + 8 (6.2f " %") + 2sp + 8 + 2sp + 14 invest. When bandCheck is on,
	// two extra columns slot between target % and invest: 9 (post %, with
	// optional `!` marker) + 2sp + 12 (5/25 band, right-padded "%.2f-%.2f").
	// 12 chars accommodates the longest realistic band ("90.00-100.00" when
	// a target sits at or just below 1.0).
	var header string
	if bandCheck {
		header = fmt.Sprintf("%s  %14s  %8s  %8s  %9s  %12s  %14s",
			padRunes("asset", nameColWidth),
			"current",
			"now %",
			"target %",
			"post %",
			"5/25 band",
			"invest")
	} else {
		header = fmt.Sprintf("%s  %14s  %8s  %8s  %14s",
			padRunes("asset", nameColWidth),
			"current",
			"now %",
			"target %",
			"invest")
	}
	if color {
		fmt.Fprintln(w, ansiBold+header+ansiReset)
	} else {
		fmt.Fprintln(w, header)
	}
	fmt.Fprintln(w, strings.Repeat("─", utf8.RuneCountInString(header)))

	// contViz: per-ancestor data needed to draw that ancestor's continuation
	// column on a descendant row. wasLast picks `    ` over `│   `; barBright
	// picks bright over dim when the bar is drawn.
	type contViz struct {
		wasLast   bool
		barBright bool
	}

	dimIf := func(s string, dim bool) string {
		if dim && color {
			return ansiDim + s + ansiReset
		}
		return s
	}

	buildPrefix := func(ancestors []contViz) string {
		var b strings.Builder
		for _, a := range ancestors {
			if a.wasLast {
				b.WriteString("    ")
			} else {
				b.WriteString(dimIf("│   ", !a.barBright))
			}
		}
		return b.String()
	}

	// breaches accumulates nodes whose post-contribution weight falls outside
	// their 5/25 band; populated by walk when bandCheck is on, consumed by
	// the footer below.
	var breaches []*portfolio.Node

	// walk renders n. ancestors holds one contViz per ancestor (root excluded
	// since it contributes no column). isLast = n is its parent's last child.
	// laterUnstuck = n has at least one later sibling whose subtree contains
	// an unstuck leaf (drives the `├` glyph's brightness when !isLast).
	var walk func(n *portfolio.Node, ancestors []contViz, isLast, laterUnstuck bool, depth int)
	walk = func(n *portfolio.Node, ancestors []contViz, isLast, laterUnstuck bool, depth int) {
		prefix := buildPrefix(ancestors)

		conn := ""
		connRunes := 0
		if depth > 0 {
			connRunes = 4
			if isLast {
				conn = dimIf("└── ", n.Stuck)
			} else {
				// Split connector: `├` is bright iff either arm reaches
				// unstuck — n itself (right arm) or some later sibling
				// (down stem). `── ` plus the name follow n.Stuck.
				conn = dimIf("├", n.Stuck && !laterUnstuck) + dimIf("── ", n.Stuck)
			}
		}

		nameWidth := nameColWidth - len(ancestors)*4 - connRunes
		if nameWidth < 0 {
			nameWidth = 0
		}
		name := dimIf(padRunes(truncRunes(n.Name, nameWidth), nameWidth), n.Stuck)

		nowPct := 0.0
		if rootCurrent > 0 {
			nowPct = n.Current / rootCurrent * 100
		}
		investStr := fmt.Sprintf("%+10.2f %s", n.Investment, ccy)
		if n.Stuck {
			investStr = fmt.Sprintf("%14s", "—")
		}

		coreCells := fmt.Sprintf("  %10.2f %s  %6.2f %%  %6.2f %%",
			n.Current, ccy, nowPct, n.Target*100)
		coreCells = dimIf(coreCells, n.Stuck)
		investCell := dimIf("  "+investStr, n.Stuck)

		extras := ""
		if bandCheck {
			postPct := 0.0
			if postTotal > 0 {
				postPct = (n.Current + n.Investment) / postTotal * 100
			}
			postCell := fmt.Sprintf("%6.2f %% ", postPct) // 9 chars: 8 + trailing space for marker slot
			bandCell := strings.Repeat(" ", 12)
			breach := false
			// Skip band for root (depth 0): its band is mathematically always
			// trivially satisfied (post % = 100% by construction) and the
			// information is not useful at the totals row.
			if n.Target > 0 && depth > 0 {
				lo, hi := bindingBand(n.Target)
				bandCell = fmt.Sprintf("%12s", fmt.Sprintf("%.2f-%.2f", lo*100, hi*100))
				if nodeBreaches(n, postTotal) {
					breach = true
					postCell = fmt.Sprintf("%6.2f %%!", postPct)
				}
			}
			// Apply dim to the band cell (purely informational) and to the
			// post % cell when not breaching. A breach keeps the marker
			// visible: red overrides the dim.
			bandCell = dimIf(bandCell, n.Stuck)
			if breach {
				if color {
					postCell = ansiRed + postCell + ansiReset
				}
				breaches = append(breaches, n)
			} else {
				postCell = dimIf(postCell, n.Stuck)
			}
			extras = "  " + postCell + "  " + bandCell
		}

		fmt.Fprintln(w, prefix+conn+name+coreCells+extras+investCell)

		// Ancestors carried into descendants. Root contributes no column.
		var nextAncestors []contViz
		if depth >= 1 {
			nextAncestors = append(ancestors, contViz{wasLast: isLast, barBright: laterUnstuck})
		} else {
			nextAncestors = ancestors
		}

		for i, c := range n.Children {
			childIsLast := i == len(n.Children)-1
			childLaterUnstuck := false
			for j := i + 1; j < len(n.Children); j++ {
				if !n.Children[j].Stuck {
					childLaterUnstuck = true
					break
				}
			}
			walk(c, nextAncestors, childIsLast, childLaterUnstuck, depth+1)
		}

		if !n.IsLeaf() {
			return
		}

		// Leaf classification: render its underlying assignments as visual
		// children. Assignments share the leaf's stuck status (display-only,
		// not allocated independently), so the whole assignment row follows
		// n.Stuck. Continuation columns above are unchanged.
		asgPrefix := buildPrefix(nextAncestors)
		asgConnRunes := 4
		asgNameWidth := nameColWidth - len(nextAncestors)*4 - asgConnRunes
		if asgNameWidth < 0 {
			asgNameWidth = 0
		}
		for i, a := range n.Assignments {
			plain := "├── "
			if i == len(n.Assignments)-1 {
				plain = "└── "
			}
			asgConn := dimIf(plain, n.Stuck)
			asgName := dimIf(padRunes(truncRunes(a.Name, asgNameWidth), asgNameWidth), n.Stuck)
			asgValues := dimIf(fmt.Sprintf("  %10.2f %s", a.Current, ccy), n.Stuck)
			fmt.Fprintln(w, asgPrefix+asgConn+asgName+asgValues)
		}
	}
	walk(root, nil, true, false, 0)

	fmt.Fprintln(w, strings.Repeat("─", utf8.RuneCountInString(header)))

	if bandCheck && len(breaches) > 0 {
		names := make([]string, len(breaches))
		for i, n := range breaches {
			names[i] = n.Name
		}
		noun := "node"
		if len(breaches) > 1 {
			noun = "nodes"
		}
		warn := fmt.Sprintf("! Portfolio is unbalanced, %d %s outside 5/25 band: %s",
			len(breaches), noun, strings.Join(names, ", "))
		if color {
			fmt.Fprintln(w, ansiBold+ansiRed+warn+ansiReset)
		} else {
			fmt.Fprintln(w, warn)
		}
	}

	footer := fmt.Sprintf("Total contributed: %+.2f %s  (post-contribution portfolio: %.2f %s)",
		amount, ccy, rootCurrent+amount, ccy)
	if color {
		fmt.Fprintln(w, ansiBold+footer+ansiReset)
	} else {
		fmt.Fprintln(w, footer)
	}
}

// jsonNode mirrors portfolio.Node for JSON emission, with derived fields:
// post-contribution share, 5/25 band bounds, and a breach flag. Carried in
// render rather than on portfolio.Node to keep the parser/portfolio package
// free of allocation-derived concerns.
//
// All weight fields (target, postContribution, bandLo, bandHi) are absolute
// ratios in [0, 1] of the post-contribution portfolio total — matching the
// convention used by portfolio.Node.Target.
type jsonNode struct {
	Name             string                 `json:"name"`
	BaseCurrency     string                 `json:"baseCurrency,omitempty"`
	Current          float64                `json:"current"`
	Target           float64                `json:"target"`
	Investment       float64                `json:"investment"`
	Stuck            bool                   `json:"stuck"`
	PostContribution float64                `json:"postContribution"`
	BandLo           *float64               `json:"bandLo,omitempty"`
	BandHi           *float64               `json:"bandHi,omitempty"`
	Breach           bool                   `json:"breach"`
	Children         []jsonNode             `json:"children,omitempty"`
	Assignments      []portfolio.Assignment `json:"assignments,omitempty"`
}

// toJSONNode recursively converts a portfolio.Node into a jsonNode, filling
// in derived fields. depth lets us skip band emission for the root, where
// the band is trivially [0.95, 1.05] and not informative.
func toJSONNode(n *portfolio.Node, postTotal float64, depth int) jsonNode {
	jn := jsonNode{
		Name:         n.Name,
		BaseCurrency: n.BaseCurrency,
		Current:      n.Current,
		Target:       n.Target,
		Investment:   n.Investment,
		Stuck:        n.Stuck,
		Assignments:  n.Assignments,
	}
	if postTotal > 0 {
		jn.PostContribution = (n.Current + n.Investment) / postTotal
	}
	if n.Target > 0 && depth > 0 {
		lo, hi := bindingBand(n.Target)
		jn.BandLo = &lo
		jn.BandHi = &hi
		jn.Breach = nodeBreaches(n, postTotal)
	}
	for _, c := range n.Children {
		jn.Children = append(jn.Children, toJSONNode(c, postTotal, depth+1))
	}
	return jn
}

// JSON writes the tree as a JSON document with a small envelope: the
// contribution amount, pre- and post-contribution totals, the enriched
// node tree, and a flat list of names of nodes outside their 5/25 band.
func JSON(w io.Writer, root *portfolio.Node, amount float64) error {
	type out struct {
		Amount       float64  `json:"amount"`
		CurrentTotal float64  `json:"currentTotal"`
		PostTotal    float64  `json:"postTotal"`
		Root         jsonNode `json:"root"`
		Breaches     []string `json:"breaches"`
	}
	postTotal := root.Current + amount
	breaches := collectBreachNames(root, amount)
	if breaches == nil {
		breaches = []string{} // emit `[]` rather than `null`
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out{
		Amount:       amount,
		CurrentTotal: root.Current,
		PostTotal:    postTotal,
		Root:         toJSONNode(root, postTotal, 0),
		Breaches:     breaches,
	})
}

// padRunes right-pads s with spaces so its rune count is at least n.
func padRunes(s string, n int) string {
	c := utf8.RuneCountInString(s)
	if c >= n {
		return s
	}
	return s + strings.Repeat(" ", n-c)
}

// truncRunes truncates s to at most n runes, adding an ellipsis if cut.
func truncRunes(s string, n int) string {
	c := utf8.RuneCountInString(s)
	if c <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-1]) + "…"
}

// bindingBand returns the inclusive lower/upper bound of the 5/25 rule's
// binding band for a target weight (target in [0,1]). The binding band is
// the tighter of: absolute ±5pp, or relative ±25% of the target — whichever
// is smaller, per Swedroe. Lower bound is clamped at 0.
func bindingBand(target float64) (lo, hi float64) {
	bound := 0.05
	if rel := 0.25 * target; rel < bound {
		bound = rel
	}
	lo = target - bound
	if lo < 0 {
		lo = 0
	}
	hi = target + bound
	return
}

// nodeBreaches reports whether n's post-contribution weight is outside its
// 5/25 binding band. postTotal is the post-contribution sum of all leaves
// (== root.Current + amount). Returns false for nodes without a target or
// when the portfolio is empty.
func nodeBreaches(n *portfolio.Node, postTotal float64) bool {
	if n.Target <= 0 || postTotal <= 0 {
		return false
	}
	postPct := (n.Current + n.Investment) / postTotal * 100
	lo, hi := bindingBand(n.Target)
	return postPct < lo*100 || postPct > hi*100
}

// collectBreachNames walks the tree (skipping the root) and returns the
// names of nodes that breach their 5/25 band, in DFS order.
func collectBreachNames(root *portfolio.Node, amount float64) []string {
	postTotal := root.Current + amount
	var names []string
	var walk func(n *portfolio.Node, depth int)
	walk = func(n *portfolio.Node, depth int) {
		if depth > 0 && nodeBreaches(n, postTotal) {
			names = append(names, n.Name)
		}
		for _, c := range n.Children {
			walk(c, depth+1)
		}
	}
	walk(root, 0)
	return names
}
