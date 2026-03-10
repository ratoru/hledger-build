# Track investments

Tracking investments can be as simple or as detailed as you want. This guide
starts simple — recording the cash value of each investment without modelling
individual holdings — and adds more structure only when needed.

We assume:

1. Your investment lives in its own account, somewhere under `assets`.
2. You pay money into it — at least once, likely on a recurring basis.
3. Its value changes over time. You don't get a daily feed, so you record the
   current value manually whenever you receive a statement or check online.
4. You may occasionally withdraw money. This doesn't affect the calculations
   described below.

## Recording contributions

Create a journal file for contributions in `_manual`. For a pension with
quarterly contributions from a checking account:

```ledger
; sources/_manual/2026/pension.journal

2026-01-31 (BP) Vanguard 401k contribution
    assets:mybank:checking         $-1500.00
    assets:pension:vanguard

2026-10-31 (BP) Vanguard 401k contribution
    assets:mybank:checking         $-500.00
    assets:pension:vanguard
```

Include it from your year journal:

```ledger
; 2026.journal
include commodities.journal
include sources/_manual/2026/*.journal
include sources/2026-imports.journal
```

## Recording valuations

When you receive a statement or check the value online, record it with a
balance assertion (`=`). The difference between the asserted value and the
running balance — the unrealized gain or loss — is posted to
`equity:unrealized_pnl`.

```ledger
2026-12-31 pension valuation
    assets:pension:vanguard        = $1087.50
    equity:unrealized_pnl
```

`equity:unrealized_pnl` sits in equity rather than income or expense. This is
accounting-correct: an unrealized gain and its later reversal (when you sell)
net to zero, which wouldn't be the case if you used an income account.

Your `register` for the pension account then shows a clear history:

```
2026-01-31 Vanguard 401k     assets:pension:vanguard  $1500.00   $1500.00
2026-10-31 Vanguard 401k     assets:pension:vanguard   $500.00   $2000.00
2026-12-31 pension valuation  assets:pension:vanguard   $87.50   $2087.50
```

## Computing return on investment

`hledger roi` computes two measures of investment performance:

**Internal rate of return (IRR)** — the single constant growth rate that,
applied to each contribution for the time it was invested, produces your
current portfolio value. It reflects the return _you_ earned, accounting for
the timing and size of contributions.

**Time-weighted return (TWR)** — ignores contribution timing and measures the
performance of the investment itself. This is the standard benchmark used by
fund managers, because it strips out the effect of when you happened to buy in.

If you made well-timed contributions, IRR > TWR. If timing worked against you,
IRR < TWR. A good description of the difference is on
[Investopedia](https://www.investopedia.com/terms/t/time-weightedror.asp).

## Adding the report to `hledger-build.toml`

`hledger roi` reads `all.journal` (which spans all years) and needs two account
queries — one for the investment account(s), one for unrealized PnL:

```toml
[[custom_reports]]
name   = "investments"
output = "investments.txt"
script = "hledger"
years  = "all"
args   = [
  "roi",
  "-f", "all.journal",
  "--investment", "acct:assets:pension",
  "--pnl", "acct:equity:unrealized_pnl",
  "-Y",
]
```

`years = "all"` makes this a single step that re-runs whenever any underlying
data changes. `-Y` adds a per-year breakdown:

```
+-------++------------+------------++---------------+----------+-------------+--------++-------++------------+----------+
|       ||      Begin |        End || Value (begin) | Cashflow | Value (end) |    PnL ||   IRR || TWR/period | TWR/year |
+=======++============+============++===============+==========+=============+========++=======++============+==========+
|     1 || 2026-01-01 | 2026-12-31 ||             0 | $2000.00 |    $2087.50 | $87.50 || 5.24% ||      4.37% |    4.37% |
+-------++------------+------------++---------------+----------+-------------+--------++-------++------------+----------+
| Total || 2026-01-01 | 2026-12-31 ||             0 | $2000.00 |    $2087.50 | $87.50 || 5.24% ||      4.37% |    4.37% |
+-------++------------+------------++---------------+----------+-------------+--------++-------++------------+----------+
```

To track multiple accounts in one report, broaden the `--investment` query:

```toml
"--investment", "acct:assets:pension or acct:assets:brokerage",
```

Or add separate `[[custom_reports]]` blocks for individual per-account reports.

## `equity:unrealized_pnl` vs `virtual:unrealized_pnl`

Some guides use a `virtual:unrealized_pnl` account outside the normal
`assets`/`liabilities`/`equity` hierarchy. The trade-off:

**`equity:unrealized_pnl`** is accounting-correct — unrealized gains belong in
equity. It will appear in your `balancesheet` report, which gives an accurate
picture of net worth including paper gains.

**`virtual:unrealized_pnl`** keeps the balance sheet clean of fluctuating
paper gains, and avoids mixing valuation entries with the
opening/closing-balance transactions that also live under `equity`. If you
prefer this, adjust the `--pnl` query accordingly and add
`not:acct:virtual:unrealized_pnl` to any reports where you don't want it
appearing.
