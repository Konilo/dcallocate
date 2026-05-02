package portfolio

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// PortfolioPerformance fixed-point scales.
const (
	amountScale = 100     // <amount> stored × 1e2 (cents)
	sharesScale = 1e8     // <shares> stored × 1e8
	priceScale  = 1e8     // <price v> stored × 1e8 per unit
	weightScale = 10000.0 // classification/assignment <weight> stored as basis-points-of-parent
)

// StalePriceWarnDays is the threshold (in days) beyond which we emit a stale-price
// warning to stderr for a held security.
const StalePriceWarnDays = 7

// LeafTargetTolerance bounds how far the sum of leaf target weights may
// deviate from 1.0. Exceeding this aborts parsing with a clear error.
const LeafTargetTolerance = 1e-4

// TaxonomyNotFoundError is returned when the requested taxonomy is absent.
type TaxonomyNotFoundError struct {
	Want      string
	Available []string
}

func (e *TaxonomyNotFoundError) Error() string {
	return fmt.Sprintf("taxonomy %q not found; available: %v", e.Want, e.Available)
}

// Parse reads a PortfolioPerformance XML file at xmlPath and returns the
// rendered tree for the named taxonomy. Warnings (stale prices, etc.) are
// written to stderr; only fatal problems return an error.
//
// After this returns, caller must:
//  1. tree.Leaves() to get leaves in DFS order
//  2. build []allocator.Asset from each leaf's Name/Current/Target
//  3. allocator.Allocate(...)
//  4. write Investment and Stuck back onto leaves
//  5. tree.Rollup() to propagate to inner nodes
func Parse(xmlPath, taxonomyName string) (*Node, error) {
	f, err := os.Open(xmlPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", xmlPath, err)
	}
	defer f.Close()
	return ParseReader(f, taxonomyName)
}

// ParseReader is Parse but reading from an io.Reader.
func ParseReader(r io.Reader, taxonomyName string) (*Node, error) {
	return ParseReaderAt(r, taxonomyName, time.Now())
}

// ParseReaderAt is ParseReader with an explicit "today" — used by tests to
// pin the price-selection cutoff for reproducible results.
func ParseReaderAt(r io.Reader, taxonomyName string, today time.Time) (*Node, error) {
	var c clientXML
	if err := xml.NewDecoder(r).Decode(&c); err != nil {
		return nil, fmt.Errorf("decode XML: %w", err)
	}
	return buildTree(&c, taxonomyName, today)
}

func buildTree(c *clientXML, taxonomyName string, today time.Time) (*Node, error) {
	baseCurrency := c.BaseCurrency
	if baseCurrency == "" {
		return nil, fmt.Errorf("portfolio file is missing <baseCurrency>")
	}

	// Index securities and accounts by their id-attribute.
	secByID := map[string]*securityXML{}
	for i := range c.Securities.Items {
		s := &c.Securities.Items[i]
		if s.ID != "" {
			secByID[s.ID] = s
		}
	}
	acctByID := map[string]*accountXML{}
	for i := range c.Accounts.Items {
		a := &c.Accounts.Items[i]
		if a.ID != "" {
			acctByID[a.ID] = a
		}
	}

	// Walk the entire tree to collect:
	//   - every account-transaction with id-attribute (regardless of nesting)
	//   - every portfolio-transaction with id-attribute
	// PP/XStream declares each tx exactly once and uses references thereafter.
	acctTxByID := map[string]*accountTxXML{}
	var portfolioTxs []*portfolioTxXML
	for i := range c.Accounts.Items {
		a := &c.Accounts.Items[i]
		for j := range a.Transactions.Items {
			collectFromAcctTx(&a.Transactions.Items[j], acctTxByID, &portfolioTxs)
		}
	}

	// Aggregate signed shares per security from all portfolio-transactions.
	sharesBySec := map[string]float64{}
	for _, ptx := range portfolioTxs {
		sign, ok := portfolioTxSign[ptx.Type]
		if !ok {
			return nil, fmt.Errorf("unknown portfolio-transaction type %q (id=%s)", ptx.Type, ptx.ID)
		}
		if ptx.Security == nil || ptx.Security.Reference == "" {
			return nil, fmt.Errorf("portfolio-transaction id=%s has no security reference", ptx.ID)
		}
		if ptx.CurrencyCode != "" && ptx.CurrencyCode != baseCurrency {
			return nil, fmt.Errorf("portfolio-transaction id=%s is in %s, not the portfolio base currency %s (FX out of scope)",
				ptx.ID, ptx.CurrencyCode, baseCurrency)
		}
		sharesBySec[ptx.Security.Reference] += float64(sign) * float64(ptx.Shares) / sharesScale
	}

	// Compute current EUR value per security.
	valBySec := map[string]float64{}
	for id, s := range secByID {
		if s.CurrencyCode != "" && s.CurrencyCode != baseCurrency {
			return nil, fmt.Errorf("security id=%s (%s) is in %s, not the portfolio base currency %s (FX out of scope)",
				id, s.Name, s.CurrencyCode, baseCurrency)
		}
		held := sharesBySec[id]
		if held == 0 {
			valBySec[id] = 0
			continue
		}
		price, priceDate, err := latestPrice(s, today)
		if err != nil {
			return nil, fmt.Errorf("security id=%s (%s): %w", id, s.Name, err)
		}
		if today.Sub(priceDate) > StalePriceWarnDays*24*time.Hour {
			fmt.Fprintf(os.Stderr, "warning: latest price for %s is from %s (>%dd old)\n",
				s.Name, priceDate.Format("2006-01-02"), StalePriceWarnDays)
		}
		valBySec[id] = held * price
	}

	// Compute current cash balance per account by walking each account's
	// transaction list, resolving references against acctTxByID.
	cashByAcct := map[string]float64{}
	for id, a := range acctByID {
		if a.CurrencyCode != "" && a.CurrencyCode != baseCurrency {
			return nil, fmt.Errorf("account id=%s (%s) is in %s, not the portfolio base currency %s (FX out of scope)",
				id, a.Name, a.CurrencyCode, baseCurrency)
		}
		var bal float64
		for j := range a.Transactions.Items {
			tx := &a.Transactions.Items[j]
			actual := tx
			if tx.Reference != "" {
				actual = acctTxByID[tx.Reference]
				if actual == nil {
					return nil, fmt.Errorf("account id=%s (%s): dangling reference to account-transaction id=%s",
						id, a.Name, tx.Reference)
				}
			} else if tx.ID == "" {
				continue
			}
			sign, ok := acctTxSign[actual.Type]
			if !ok {
				return nil, fmt.Errorf("unknown account-transaction type %q (id=%s, account %s)",
					actual.Type, actual.ID, a.Name)
			}
			bal += float64(sign) * float64(actual.Amount) / amountScale
		}
		cashByAcct[id] = bal
	}

	// Find the named taxonomy.
	var available []string
	var tax *taxonomyXML
	for i := range c.Taxonomies.Items {
		t := &c.Taxonomies.Items[i]
		available = append(available, t.Name)
		if t.Name == taxonomyName {
			tax = t
		}
	}
	if tax == nil {
		return nil, &TaxonomyNotFoundError{Want: taxonomyName, Available: available}
	}

	// Build the rendered tree.
	root := buildNode(&tax.Root, 1.0, valBySec, cashByAcct, secByID, acctByID)
	root.BaseCurrency = baseCurrency

	// Sanity-check: leaf targets sum to 1.0 (within tolerance).
	var sumTargets float64
	for _, leaf := range root.Leaves() {
		sumTargets += leaf.Target
	}
	if absFloat(sumTargets-1.0) > LeafTargetTolerance {
		return nil, fmt.Errorf("leaf target weights sum to %.6f, expected 1.0 (tolerance %g) — check your taxonomy weights",
			sumTargets, LeafTargetTolerance)
	}

	// Pre-populate inner-node Current as sum-of-children (Rollup will overwrite once Investment lands).
	root.Rollup()

	return root, nil
}

func collectFromAcctTx(tx *accountTxXML, m map[string]*accountTxXML, ptxs *[]*portfolioTxXML) {
	if tx.ID != "" {
		m[tx.ID] = tx
	}
	if tx.CrossEntry == nil || tx.CrossEntry.Portfolio == nil {
		return
	}
	p := tx.CrossEntry.Portfolio
	if p.ID == "" {
		// Just a reference, no nested data to recurse into.
		return
	}
	for i := range p.Transactions.Items {
		ptx := &p.Transactions.Items[i]
		if ptx.ID != "" {
			*ptxs = append(*ptxs, ptx)
		}
		if ptx.CrossEntry != nil && ptx.CrossEntry.AccountTransaction != nil {
			collectFromAcctTx(ptx.CrossEntry.AccountTransaction, m, ptxs)
		}
	}
}

func buildNode(c *classificationXML, parentTarget float64,
	valBySec, cashByAcct map[string]float64,
	secByID map[string]*securityXML, acctByID map[string]*accountXML) *Node {

	ownTarget := parentTarget * float64(c.Weight) / weightScale
	n := &Node{Name: c.Name, Target: ownTarget}

	// Inner classification: recurse into child classifications. Direct
	// assignments on a non-leaf classification are unusual; we ignore them
	// (PP's UI typically doesn't allow this) but don't error on it.
	if len(c.Children.Items) > 0 {
		for i := range c.Children.Items {
			child := buildNode(&c.Children.Items[i], ownTarget, valBySec, cashByAcct, secByID, acctByID)
			n.Children = append(n.Children, child)
		}
		return n
	}

	// Leaf classification: collect underlying assignments and sum their
	// values into Current. The classification's own target is used as-is —
	// no per-assignment splitting.
	for _, a := range c.Assignments.Items {
		var asg Assignment
		switch a.InvestmentVehicle.Class {
		case "security":
			s := secByID[a.InvestmentVehicle.Reference]
			if s == nil {
				asg = Assignment{Name: "<missing security " + a.InvestmentVehicle.Reference + ">", Kind: "security"}
			} else {
				asg = Assignment{Name: s.Name, Kind: "security", Current: valBySec[s.ID]}
			}
		case "account":
			a2 := acctByID[a.InvestmentVehicle.Reference]
			if a2 == nil {
				asg = Assignment{Name: "<missing account " + a.InvestmentVehicle.Reference + ">", Kind: "account"}
			} else {
				asg = Assignment{Name: a2.Name, Kind: "account", Current: cashByAcct[a2.ID]}
			}
		default:
			continue
		}
		n.Assignments = append(n.Assignments, asg)
		n.Current += asg.Current
	}
	return n
}

// latestPrice returns (price-per-unit-in-EUR, date) for the most recent <price t v/>
// with t <= today.
func latestPrice(s *securityXML, today time.Time) (float64, time.Time, error) {
	if len(s.Prices.Items) == 0 {
		return 0, time.Time{}, errors.New("no prices")
	}
	var best priceXML
	var bestDate time.Time
	found := false
	for _, p := range s.Prices.Items {
		d, err := time.Parse("2006-01-02", p.T)
		if err != nil {
			continue
		}
		if d.After(today) {
			continue
		}
		if !found || d.After(bestDate) {
			best = p
			bestDate = d
			found = true
		}
	}
	if !found {
		return 0, time.Time{}, errors.New("no price with t <= today")
	}
	return float64(best.V) / priceScale, bestDate, nil
}

func absFloat(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// --- Sign tables for transaction types. Unknown types abort parsing. ---

var portfolioTxSign = map[string]int64{
	"BUY":               +1,
	"TRANSFER_IN":       +1,
	"DELIVERY_INBOUND":  +1,
	"SELL":              -1,
	"TRANSFER_OUT":      -1,
	"DELIVERY_OUTBOUND": -1,
}

var acctTxSign = map[string]int64{
	"DEPOSIT":         +1,
	"INTEREST":        +1,
	"DIVIDENDS":       +1,
	"TAX_REFUND":      +1,
	"FEES_REFUND":     +1,
	"SELL":            +1,
	"TRANSFER_IN":     +1,
	"REMOVAL":         -1,
	"INTEREST_CHARGE": -1,
	"TAXES":           -1,
	"FEES":            -1,
	"BUY":             -1,
	"TRANSFER_OUT":    -1,
}

// --- Internal XML struct types. ---

type clientXML struct {
	XMLName      xml.Name      `xml:"client"`
	BaseCurrency string        `xml:"baseCurrency"`
	Securities   securitiesXML `xml:"securities"`
	Accounts     accountsXML   `xml:"accounts"`
	Taxonomies   taxonomiesXML `xml:"taxonomies"`
}

type securitiesXML struct {
	Items []securityXML `xml:"security"`
}

type securityXML struct {
	ID           string    `xml:"id,attr"`
	Reference    string    `xml:"reference,attr"`
	UUID         string    `xml:"uuid"`
	Name         string    `xml:"name"`
	CurrencyCode string    `xml:"currencyCode"`
	Prices       pricesXML `xml:"prices"`
}

type pricesXML struct {
	Items []priceXML `xml:"price"`
}

type priceXML struct {
	T string `xml:"t,attr"`
	V int64  `xml:"v,attr"`
}

type accountsXML struct {
	Items []accountXML `xml:"account"`
}

type accountXML struct {
	ID           string         `xml:"id,attr"`
	Reference    string         `xml:"reference,attr"`
	UUID         string         `xml:"uuid"`
	Name         string         `xml:"name"`
	CurrencyCode string         `xml:"currencyCode"`
	Transactions accountTxsXML  `xml:"transactions"`
}

type accountTxsXML struct {
	Items []accountTxXML `xml:"account-transaction"`
}

type accountTxXML struct {
	ID           string         `xml:"id,attr"`
	Reference    string         `xml:"reference,attr"`
	UUID         string         `xml:"uuid"`
	Amount       int64          `xml:"amount"`
	Type         string         `xml:"type"`
	CurrencyCode string         `xml:"currencyCode"`
	Shares       int64          `xml:"shares"`
	Security     *refXML        `xml:"security"`
	CrossEntry   *crossEntryXML `xml:"crossEntry"`
}

type refXML struct {
	Class     string `xml:"class,attr"`
	Reference string `xml:"reference,attr"`
}

type crossEntryXML struct {
	Class     string        `xml:"class,attr"`
	ID        string        `xml:"id,attr"`
	Reference string        `xml:"reference,attr"`
	Portfolio *portfolioXML `xml:"portfolio"`
	// AccountTransaction is the camelCase variant nested inside a portfolio-tx
	// crossEntry — same shape as <account-transaction> elsewhere.
	AccountTransaction *accountTxXML `xml:"accountTransaction"`
}

type portfolioXML struct {
	ID           string             `xml:"id,attr"`
	Reference    string             `xml:"reference,attr"`
	UUID         string             `xml:"uuid"`
	Name         string             `xml:"name"`
	Transactions portfolioTxsXML    `xml:"transactions"`
}

type portfolioTxsXML struct {
	Items []portfolioTxXML `xml:"portfolio-transaction"`
}

type portfolioTxXML struct {
	ID           string         `xml:"id,attr"`
	Reference    string         `xml:"reference,attr"`
	UUID         string         `xml:"uuid"`
	Amount       int64          `xml:"amount"`
	Type         string         `xml:"type"`
	CurrencyCode string         `xml:"currencyCode"`
	Shares       int64          `xml:"shares"`
	Security     *refXML        `xml:"security"`
	CrossEntry   *crossEntryXML `xml:"crossEntry"`
}

type taxonomiesXML struct {
	Items []taxonomyXML `xml:"taxonomy"`
}

type taxonomyXML struct {
	Name string            `xml:"name"`
	Root classificationXML `xml:"root"`
}

type classificationXML struct {
	ID          string         `xml:"id,attr"`
	Reference   string         `xml:"reference,attr"`
	Name        string         `xml:"name"`
	Weight      int64          `xml:"weight"` // basis-point-of-parent (10000 = 100%)
	Children    childrenXML    `xml:"children"`
	Assignments assignmentsXML `xml:"assignments"`
}

type childrenXML struct {
	Items []classificationXML `xml:"classification"`
}

type assignmentsXML struct {
	Items []assignmentXML `xml:"assignment"`
}

type assignmentXML struct {
	InvestmentVehicle refXML `xml:"investmentVehicle"`
	Weight            int64  `xml:"weight"`
}
