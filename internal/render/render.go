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

	nameColWidth = 44
)

// Tree writes a human-readable tree of the portfolio + computed allocations
// to w. If color is true, ANSI escape codes are emitted (header & footer
// bold; tree glyphs dim where they lead only to stuck subtrees). Money
// columns are labelled with the portfolio's base currency, read from
// root.BaseCurrency.
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
func Tree(w io.Writer, root *portfolio.Node, amount float64, color bool) {
	rootCurrent := root.Current
	ccy := root.BaseCurrency

	// Header. Body line column widths are: name (nameColWidth) + 2sp + 14
	// (10.2f " <ccy>") + 2sp + 8 (6.2f " %") + 2sp + 8 + 2sp + 14 invest.
	header := fmt.Sprintf("%s  %14s  %8s  %8s  %14s",
		padRunes("asset", nameColWidth),
		"current",
		"now %",
		"target %",
		"invest")
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
		values := dimIf(
			fmt.Sprintf("  %10.2f %s  %6.2f %%  %6.2f %%  %s",
				n.Current, ccy, nowPct, n.Target*100, investStr),
			n.Stuck,
		)

		fmt.Fprintln(w, prefix+conn+name+values)

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
	footer := fmt.Sprintf("Total contributed: %+.2f %s  (post-contribution portfolio: %.2f %s)",
		amount, ccy, rootCurrent+amount, ccy)
	if color {
		fmt.Fprintln(w, ansiBold+footer+ansiReset)
	} else {
		fmt.Fprintln(w, footer)
	}
}

// JSON writes the tree as a JSON document with a small envelope.
func JSON(w io.Writer, root *portfolio.Node, amount float64) error {
	type out struct {
		Amount       float64         `json:"amount"`
		CurrentTotal float64         `json:"currentTotal"`
		Root         *portfolio.Node `json:"root"`
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out{
		Amount:       amount,
		CurrentTotal: root.Current,
		Root:         root,
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
