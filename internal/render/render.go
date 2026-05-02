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
// to w. If color is true, ANSI escape codes are emitted (stuck rows dimmed,
// header bold).
func Tree(w io.Writer, root *portfolio.Node, amount float64, color bool) {
	rootCurrent := root.Current

	// Header. Body line column widths are: name (nameColWidth) + 2sp + 14
	// (10.2f " EUR") + 2sp + 8 (6.2f " %") + 2sp + 8 + 2sp + 14 invest.
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

	var walk func(n *portfolio.Node, prefix string, isLast bool, depth int)
	walk = func(n *portfolio.Node, prefix string, isLast bool, depth int) {
		// Compose the name cell with tree connectors.
		var nameCell string
		if depth == 0 {
			nameCell = n.Name
		} else {
			conn := "├── "
			if isLast {
				conn = "└── "
			}
			nameCell = prefix + conn + n.Name
		}

		nowPct := 0.0
		if rootCurrent > 0 {
			nowPct = n.Current / rootCurrent * 100
		}

		investStr := fmt.Sprintf("%+10.2f EUR", n.Investment)
		if n.Stuck {
			investStr = fmt.Sprintf("%14s", "—")
		}
		line := fmt.Sprintf("%s  %10.2f EUR  %6.2f %%  %6.2f %%  %s",
			padRunes(truncRunes(nameCell, nameColWidth), nameColWidth),
			n.Current,
			nowPct,
			n.Target*100,
			investStr,
		)
		if color && n.Stuck {
			fmt.Fprintln(w, ansiDim+line+ansiReset)
		} else {
			fmt.Fprintln(w, line)
		}

		// Build the prefix carried into our descendants.
		var childPrefix string
		switch {
		case depth == 0:
			childPrefix = ""
		case isLast:
			childPrefix = prefix + "    "
		default:
			childPrefix = prefix + "│   "
		}

		// Inner node: descend into child classifications.
		if !n.IsLeaf() {
			for i, c := range n.Children {
				walk(c, childPrefix, i == len(n.Children)-1, depth+1)
			}
			return
		}

		// Leaf classification: render its underlying assignments as visual
		// children, with only Name + Current populated (no target / invest).
		for i, a := range n.Assignments {
			conn := "├── "
			if i == len(n.Assignments)-1 {
				conn = "└── "
			}
			label := fmt.Sprintf("%s%s%s", childPrefix, conn, a.Name)
			asgLine := fmt.Sprintf("%s  %10.2f EUR",
				padRunes(truncRunes(label, nameColWidth), nameColWidth),
				a.Current,
			)
			if color {
				fmt.Fprintln(w, ansiDim+asgLine+ansiReset)
			} else {
				fmt.Fprintln(w, asgLine)
			}
		}
	}
	walk(root, "", true, 0)

	fmt.Fprintln(w, strings.Repeat("─", utf8.RuneCountInString(header)))
	footer := fmt.Sprintf("Total contributed: %+.2f EUR  (post-contribution portfolio: %.2f EUR)",
		amount, rootCurrent+amount)
	if color {
		fmt.Fprintln(w, ansiBold+footer+ansiReset)
	} else {
		fmt.Fprintln(w, footer)
	}
}

// JSON writes the tree as a JSON document with a small envelope.
func JSON(w io.Writer, root *portfolio.Node, amount float64) error {
	type out struct {
		Amount       float64          `json:"amount"`
		CurrentTotal float64          `json:"currentTotal"`
		Root         *portfolio.Node  `json:"root"`
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
