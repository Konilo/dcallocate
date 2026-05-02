// Command dcallocate reads a PortfolioPerformance XML file, takes an amount of
// new money to contribute, and prints how to split it across the assets so the
// portfolio converges toward its target allocation, never selling.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/Konilo/dcallocate/internal/allocator"
	"github.com/Konilo/dcallocate/internal/config"
	"github.com/Konilo/dcallocate/internal/portfolio"
	"github.com/Konilo/dcallocate/internal/render"
)

const usageTmpl = `dcallocate — PortfolioPerformance rebalance-by-investing helper.

Usage:
  dcallocate [flags] <AMOUNT_EUR>

Examples:
  dcallocate 500                                          # uses saved config
  dcallocate --amount 500                                 # same
  dcallocate --xml ./pp.xml --taxonomy "Asset Classes" 500
  dcallocate 500 --save-config                            # remember --xml + --taxonomy
  dcallocate --json 500                                   # machine-readable output

Configuration is loaded from / written to:
  %s

Flags:
`

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	fs := flag.NewFlagSet("dcallocate", flag.ContinueOnError)
	var (
		xmlPath   = fs.String("xml", "", "path to the PortfolioPerformance XML file")
		taxName   = fs.String("taxonomy", "", "taxonomy name to rebalance against (e.g. \"Asset Classes\")")
		amountF   = fs.Float64("amount", 0, "amount to contribute, in EUR")
		asJSON    = fs.Bool("json", false, "emit JSON instead of the pretty tree")
		saveCfg   = fs.Bool("save-config", false, "save --xml and --taxonomy to the user config")
		colorMode = fs.String("color", "auto", "when to emit ANSI colors: auto (default; honors NO_COLOR and TTY), always, or never")
		allowSell = fs.Bool("allow-selling", false, "permit negative per-asset deltas (sells) so weights land exactly on target; default is buy-only")
	)

	cfgDir, _ := config.DefaultDir()
	cfgPath := config.Path(cfgDir)
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), usageTmpl, cfgPath)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		// flag already prints; ContinueOnError returns the error.
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	switch *colorMode {
	case "auto", "always", "never":
	default:
		fmt.Fprintf(os.Stderr, "error: invalid --color %q (must be auto, always, or never)\n", *colorMode)
		fs.Usage()
		return 2
	}

	// Positional amount: `dcallocate 500`. Trailing positional wins over --amount.
	// Track whether amount was explicitly provided so we can distinguish
	// "user passed 0" (valid, especially with --allow-selling) from "user forgot".
	amount := *amountF
	amountProvided := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "amount" {
			amountProvided = true
		}
	})
	if fs.NArg() == 1 {
		a, err := strconv.ParseFloat(fs.Arg(0), 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid AMOUNT %q: %v\n", fs.Arg(0), err)
			return 2
		}
		amount = a
		amountProvided = true
	} else if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "error: too many positional arguments")
		fs.Usage()
		return 2
	}
	if !amountProvided {
		fmt.Fprintln(os.Stderr, "error: amount is required (use AMOUNT positional or --amount)")
		fs.Usage()
		return 2
	}
	if amount < 0 {
		fmt.Fprintln(os.Stderr, "error: amount must be >= 0")
		return 2
	}

	// Load saved config; flags override.
	cfg, err := config.Load(cfgDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error loading config:", err)
		return 1
	}
	if *xmlPath == "" {
		*xmlPath = cfg.XMLPath
	}
	if *taxName == "" {
		*taxName = cfg.TaxonomyName
	}

	// Prompt interactively for missing values when stdin is a TTY.
	stdinIsTTY := isCharDevice(os.Stdin)
	if *xmlPath == "" || *taxName == "" {
		if !stdinIsTTY {
			fmt.Fprintln(os.Stderr, "error: --xml and --taxonomy must be provided (no saved config and no TTY for prompting)")
			return 2
		}
		fmt.Fprintln(os.Stderr, "First run: please provide xml path and taxonomy. Use --save-config to remember.")
		if *xmlPath == "" {
			*xmlPath = promptLine("Path to PortfolioPerformance XML: ")
		}
		if *taxName == "" {
			*taxName = promptLine("Taxonomy name (e.g. Asset Classes): ")
		}
		if *xmlPath == "" || *taxName == "" {
			fmt.Fprintln(os.Stderr, "error: empty input")
			return 2
		}
	}

	if *saveCfg {
		if err := config.Save(config.Config{XMLPath: *xmlPath, TaxonomyName: *taxName}, cfgDir); err != nil {
			fmt.Fprintln(os.Stderr, "error saving config:", err)
			return 1
		}
		fmt.Fprintln(os.Stderr, "config saved to", cfgPath)
	}

	// Parse XML → tree of classifications with current / target on leaves.
	tree, err := portfolio.Parse(*xmlPath, *taxName)
	if err != nil {
		var tnf *portfolio.TaxonomyNotFoundError
		if errors.As(err, &tnf) {
			fmt.Fprintf(os.Stderr, "error: taxonomy %q not found.\nAvailable taxonomies in %s:\n",
				tnf.Want, *xmlPath)
			for _, name := range tnf.Available {
				fmt.Fprintln(os.Stderr, "  -", name)
			}
			return 1
		}
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}

	// Run water-filling at the leaf-classification level.
	leaves := tree.Leaves()
	assets := make([]allocator.Asset, len(leaves))
	for i, l := range leaves {
		assets[i] = allocator.Asset{Name: l.Name, Current: l.Current, Target: l.Target}
	}
	allocFn := allocator.Allocate
	if *allowSell {
		allocFn = allocator.AllocateWithSelling
	}
	allocs, err := allocFn(assets, amount)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	for i, a := range allocs {
		leaves[i].Investment = a.Amount
		leaves[i].Stuck = a.Stuck
	}
	tree.Rollup()

	// Emit.
	if *asJSON {
		if err := render.JSON(os.Stdout, tree, amount); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			return 1
		}
		return 0
	}
	color := wantColor(*colorMode, os.Stdout)
	render.Tree(os.Stdout, tree, amount, color)
	return 0
}

func promptLine(msg string) string {
	fmt.Fprint(os.Stderr, msg)
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() {
		return sc.Text()
	}
	return ""
}

// isCharDevice reports whether f is connected to a character device
// (typically a terminal). Native consoles on Linux, macOS, and Windows return
// true; pipes, files, and MSYS / Git-Bash / Cygwin pseudo-terminals (which
// are named pipes under the hood) return false.
func isCharDevice(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// wantColor decides whether to emit ANSI color codes. An explicit
// --color=always|never wins; --color=auto consults the NO_COLOR convention
// (https://no-color.org) and falls back to a TTY check.
func wantColor(mode string, w *os.File) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	default:
		if os.Getenv("NO_COLOR") != "" {
			return false
		}
		return isCharDevice(w)
	}
}
