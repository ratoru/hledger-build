# Track a mortgage

Mortgage payments appear in your bank statement as a single lump sum, but they
consist of two parts: an **interest charge** (the lender's fee) and a **capital
repayment** (reducing the loan principal). Getting the split right matters for
accurate net-worth tracking.

This guide uses [`hledger-interest`](https://github.com/peti/hledger-interest)
to compute the interest portion automatically.

## Recording the initial mortgage

When you take out the mortgage, record the house purchase, your downpayment,
any fees rolled into the loan, and the liability itself:

```ledger
; sources/_manual/2014/mortgage.journal

2014-01-02 Taking out mortgage
    assets:bank:checking          £-150.00   ; downpayment
    expenses:mortgage fees           £5.00   ; opening fee rolled into principal
    liabilities:mortgage                     ; inferred: £-855.00
    assets:house                  £1000.00
```

`liabilities:mortgage` carries a negative balance representing what you owe
(£1000 − £150 + £5 = £855 principal).

## Recording payments

Import or manually record each payment as a transfer into `liabilities:mortgage`.
Do **not** split out interest at this stage — that is `hledger-interest`'s job:

```ledger
; sources/_manual/2014/mortgage.journal

2014-03-31 (BGC) HSBC mortgage payment
    assets:bank:checking          £-100.00
    liabilities:mortgage
```

Without further processing this overstates how much principal you have repaid.
The next step corrects it.

## Computing the interest breakdown

`hledger-interest` reads a journal and emits transactions that move the interest
portion from `liabilities:mortgage` into an expense account:

```sh
hledger-interest \
  -f 2014.journal \
  --source='expenses:mortgage interest' \
  --target=liabilities:mortgage \
  --annual=0.02 \
  --act \
  liabilities:mortgage \
  -q
```

For the example above (£855 principal at 2%), the first payment on 2014-03-31
covers 88 days of interest:

```
2014-03-31 2% interest for £-855.00 over 88 days
    liabilities:mortgage                £-4.12
    expenses:mortgage interest           £4.12
```

After this entry lands, the £100 payment correctly reduces the principal by
£95.88 rather than the full £100.

## Automating with hledger-build

There is a circular dependency: `hledger-interest` reads your year journal,
but the year journal includes the interest file that `hledger-interest`
generates. hledger-build solves this with `dyngen = true`, which breaks the
cycle in the dependency graph.

The pattern requires:

1. The generated interest file lives in `reports/` (where hledger-build writes
   step outputs).
2. Your year journal includes `reports/{year}-mortgage-interest.journal`.
3. Empty stub files bootstrap the first run.

**Step 1 — add the include to each year journal:**

```ledger
; 2014.journal
include commodities.journal
include sources/_manual/2014/*.journal
include sources/2014-imports.journal
include reports/2014-mortgage-interest.journal
```

**Step 2 — create empty stubs** (once, to satisfy hledger's include parser on
the first build):

```sh
touch reports/2014-mortgage-interest.journal
touch reports/2015-mortgage-interest.journal
# one per year
```

**Step 3 — add a script** at your project root that filters out previously
generated interest before recomputing (prevents double-counting on re-runs):

```sh
# mortgage-interest.sh
#!/bin/sh
year=$1
echo ";; This is an auto-generated file, do not edit"
hledger print -f "${year}.journal" 'not:desc: interest for' \
  | hledger-interest \
      --source='expenses:mortgage interest' \
      --target=liabilities:mortgage \
      --annual=0.02 \
      --act liabilities:mortgage \
      -q
```

Make it executable: `chmod +x mortgage-interest.sh`.

**Step 4 — configure `hledger-build.toml`:**

```toml
[[custom_reports]]
name   = "mortgage-interest"
output = "{year}-mortgage-interest.journal"
script = "./mortgage-interest.sh"
years  = "all"
dyngen = true
args   = ["{year}"]
```

`dyngen = true` tells hledger-build that `reports/{year}-mortgage-interest.journal`
is both an input (included by the year journal) and the output of this step,
and removes it from its own dependency list. The topological sort ensures the
interest file for each year is regenerated before any report or closing-balance
step that reads from that year's journal.

Run `hledger-build run` whenever your data changes.

## Remortgage: changing the interest rate

If your rate changes mid-mortgage, use `hledger-interest` 1.5.4+ with
`--annual-schedule` instead of `--annual`. Provide the effective date of each
rate as a list of `(date, rate)` pairs:

```sh
hledger-interest \
  --source='expenses:mortgage interest' \
  --target=liabilities:mortgage \
  --annual-schedule='[(2014-01-01,0.02),(2016-01-01,0.018)]' \
  --act liabilities:mortgage \
  -q
```

The initial date does not need to be exact — any date on or before the first
payment works. `hledger-interest` automatically applies each rate from its
effective date:

```
2015-12-31 2% interest for £-694.96 over 245 days
    ...
2016-03-31 1.8% interest for £-713.52 over 90 days
    ...
```

Update `--annual-schedule` in `mortgage-interest.sh` whenever you remortgage
and re-run `hledger-build run` to regenerate the interest journals for all
affected years.
