# Notes for Claude Code sessions

User-facing docs are in `README.md`. This file captures the things that
aren't obvious from reading the code, plus the design decisions that future
sessions should *not* silently undo.

## Project shape

Single-binary Go CLI. Reads a PortfolioPerformance XML file, runs water-filling
allocation under the no-selling constraint, prints a tree.

```
cmd/dcallocate/main.go          # CLI glue (flag parsing, prompts, render selection)
internal/allocator/             # pure water-filling math; no I/O; fully unit-tested
internal/portfolio/             # XML parser → tree of classifications + assignments
internal/render/                # tree pretty-print + JSON
internal/config/                # JSON persistence under os.UserConfigDir()
testdata/portfolio_min.xml      # hand-trimmed PP fixture (~50 lines)
```

## Build, test, run

Don't expect Go to be installed on the host. Use Docker for compile/test cycles:

```sh
docker run --rm -v "C:/path/to/repo:/work" -w /work golang:1.23 go test ./... -v
docker run --rm -v "C:/path/to/repo:/work" -w /work golang:1.23 go vet ./...
```

PowerShell: same idea, just escape the volume path with backticks or use
double-quoted paths. From inside the devcontainer, `make test` / `make build`
work normally.

`make release` cross-compiles 5 binaries (~2.1 MB each) into `dist/`. Tag-based
GitHub Actions workflow does the same on `v*` tag push.

## Invariants — preserve these, don't re-litigate

- **Allocation granularity is the leaf classification, not the assignment.**
  When a leaf classification has multiple assignments (e.g. one classification
  pointing at two ETFs), the classification's target is *not* split among
  them. Assignments are displayed for context only. By design, automatic
  splitting is not done — the operator chooses which security to actually buy.
- **Default never sells.** `x_i ≥ 0` is a hard constraint of the water-filling
  algorithm in `allocator.Allocate`. The opt-in `--allow-selling` flag uses
  `allocator.AllocateWithSelling`, a closed-form rebalance that permits negative
  `x_i`.
- **Allocator stays pure.** `allocator.Asset` is a flat struct with three
  fields. Don't make the allocator import `portfolio` or operate on Trees —
  the boundary keeps it trivially unit-testable.
- **Fail loud, never silently miscount.** Unknown PP transaction types,
  off-base-currency securities, dangling references, weight sums far from 1 —
  all return errors with specific context. No best-effort fallbacks.

## XML quirks (PortfolioPerformance / XStream serialization)

- Each object is declared once with `id="N"` and referenced thereafter as
  `<element reference="N"/>`. The cash-side counterpart of a BUY is declared
  *inside* the security-side `<portfolio-transaction>`'s `<crossEntry>` as
  `<accountTransaction id="...">` (camelCase) — then back-referenced from
  the outer account's `<transactions>` list as `<account-transaction
  reference="..."/>` (kebab-case). The parser handles this via a global
  `acctTxByID` map populated by recursive walk.
- Fixed-point scales (in one named place — `internal/portfolio/parse.go`):
  - `<amount>` × 1e2 (cents)
  - `<shares>` × 1e8
  - `<price v=>` × 1e8
  - `<weight>` × 10000 (basis-point-of-parent)
- Transaction-type allow-lists are in `portfolioTxSign` and `acctTxSign`.
  PP's full vocabulary is covered; if a new type appears in a user's file,
  the parser errors with the offending value — add it to the table.

## Known quirks

- **~0.02 % money-column discrepancy vs PP's UI.** PP prefers the `<latest>`
  element (sometimes intraday) over the last `<prices>/<price>` entry; we use
  the latter. *Now %* and *Target %* match PP exactly; only the absolute
  amounts drift marginally. This is intentional and documented — don't "fix"
  it without asking the user.
- **Devcontainer apt-get fix.** The Microsoft Go base image ships a Yarn
  apt source whose GPG key has rotated. `.devcontainer/Dockerfile` deletes
  `/etc/apt/sources.list.d/yarn.list` before `apt-get update`. If a future
  base-image bump fixes the upstream issue, the `rm -f` becomes a no-op —
  leave it alone.

## Out of scope (don't add unprompted)

- **Mixed-currency portfolios / FX.** Single non-EUR base currency works
  (PP's `<baseCurrency>` is honoured throughout). But a security or account
  in a *different* currency than the portfolio base → fail loud. Adding FX
  conversion would multiply the surface area for marginal benefit.
- **Per-assignment target splitting.** See invariants above.
- **GUI, web UI, scheduled runs.** It's a CLI run once a month.
