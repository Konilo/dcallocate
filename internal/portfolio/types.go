// Package portfolio parses a Portfolio Performance XML file and exposes a tree
// of taxonomy nodes + leaf assignments with their current values and target
// weights.
package portfolio

// Node is one node in the taxonomy. Both inner nodes (Children non-empty)
// and leaves (Children empty) are represented as Node; the granularity of
// allocation is the taxonomy node, not the individual security/account.
// Leaves carry their underlying Assignments for display only; the allocator
// never looks at them.
type Node struct {
	// Name is the node's display name.
	Name string `json:"name"`

	// BaseCurrency is the portfolio's base ISO 4217 code (e.g. "EUR", "USD").
	// Set only on the root node; empty on children. Read by renderers to label
	// money columns. Tracked here rather than on a wrapper struct to keep the
	// Parse signature unchanged.
	BaseCurrency string `json:"baseCurrency,omitempty"`

	// Children is the ordered list of child nodes (empty on leaves).
	Children []*Node `json:"children,omitempty"`

	// Current is the present value of this node (sum of the underlying
	// assignments for leaves; sum of children for inner nodes). Always in the
	// portfolio's base currency.
	Current float64 `json:"current"`

	// Target is the absolute target weight of this node (between 0 and 1).
	// The node's own weight is taken as-is, no per-assignment splitting. Sum
	// of leaf targets = 1.
	Target float64 `json:"target"`

	// Investment is the amount the caller should invest in this node (in the
	// base currency). For inner nodes, equals the sum of Investment of
	// children.
	Investment float64 `json:"investment"`

	// Stuck marks leaves whose target share is already covered (allocator
	// declines to invest). Inner nodes are Stuck iff every descendant leaf is.
	Stuck bool `json:"stuck"`

	// Assignments lists the securities/accounts that underlie this leaf,
	// for context. Empty on inner nodes.
	Assignments []Assignment `json:"assignments,omitempty"`
}

// Assignment is one security or cash account that contributes to a leaf's
// current value. Carried for display only; the allocator works at the
// taxonomy-node level.
type Assignment struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"` // "security" or "account"
	Current float64 `json:"current"`
}

// IsLeaf reports whether this node has no children.
func (n *Node) IsLeaf() bool { return len(n.Children) == 0 }

// Leaves returns all descendant leaves in DFS order (left-to-right).
// The order is stable and matches the order produced by the parser.
func (n *Node) Leaves() []*Node {
	var out []*Node
	var walk func(*Node)
	walk = func(x *Node) {
		if x.IsLeaf() {
			out = append(out, x)
			return
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return out
}

// Rollup recomputes Current, Target, Investment, and Stuck on inner nodes by
// summing or AND-ing across descendants. Call this after writing Investment
// and Stuck onto leaves so inner nodes reflect those updates.
func (n *Node) Rollup() {
	if n.IsLeaf() {
		return
	}
	var c, t, inv float64
	allStuck := true
	for _, child := range n.Children {
		child.Rollup()
		c += child.Current
		t += child.Target
		inv += child.Investment
		if !child.Stuck {
			allStuck = false
		}
	}
	n.Current = c
	n.Target = t
	n.Investment = inv
	n.Stuck = allStuck
}
