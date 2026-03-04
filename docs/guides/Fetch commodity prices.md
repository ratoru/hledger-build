# Fetching commodity prices automatically

When you hold assets in a currency or commodity other than your default one —
foreign currency, stocks, ETFs — hledger needs price data to convert them for
reporting. This guide shows how to automate that with
[pricehist](https://pypi.org/project/pricehist/) and hledger-build's `dyngen`
custom reports.

---

## Prerequisites

Install pricehist:

```bash
pip install pricehist
```

Verify it works:

```bash
pricehist fetch yahoo "GBPUSD=X" -s 2026-01-01 -e 2026-01-31 -o ledger
```

---

## How it fits into hledger-build

Price files (`.prices`) live in `sources/prices/{year}/`. Your year journal
`include`s them, and hledger uses the P-directives inside when you pass
`--value=then,£` to a report.

The generation is a two-step process:

```
{year}.journal  →[price_dates.sh]→  sources/prices/{year}/USD.dates
                                              ↓
                                 [prices.sh]→  sources/prices/{year}/USD.prices
                                              ↓
                            included by {year}.journal
```

Both steps are wired into hledger-build as `dyngen` custom reports. `dyngen`
tells hledger-build that the output is included by the year journal — this
breaks the circular dependency that would otherwise form (the year journal
includes the prices file, but generating the prices file depends on the year
journal).

---

## Step 1: declare your commodity

Add the commodity to `commodities.journal` so hledger formats it consistently:

```ledger
; commodities.journal
commodity $1,000.00
commodity £1,000.00
commodity 1,000.0000 AAPL
```

---

## Step 2: write the scripts

Create `sources/prices/` and add two scripts.

### `sources/prices/price_dates.sh`

Extracts the sorted, unique dates on which your journal has transactions in a
given commodity for a given year.

```bash
#!/usr/bin/env bash
# price_dates.sh — emit sorted unique dates of transactions in COMMODITY for YEAR
# Usage: price_dates.sh YEAR COMMODITY
set -euo pipefail
[ $# -ne 2 ] && { echo "usage: $0 YEAR COMMODITY" >&2; exit 1; }

year="$1"
sym="$2"

# hledger uses '$' internally for USD; escape it in the query
query="cur:${sym}"
[ "${sym}" = "USD" ] && query='cur:\$'

hledger -f "${year}.journal" print "${query}" -Ocsv \
  | awk -F',' 'NR > 1 { gsub(/"/, "", $1); print $1 }' \
  | sort -u
```

Make it executable:

```bash
chmod +x sources/prices/price_dates.sh
```

### `sources/prices/prices.sh`

Reads a `.dates` file and fetches the corresponding price directives from
Yahoo Finance via pricehist.

```bash
#!/usr/bin/env bash
# prices.sh — fetch pricehist prices for dates listed in DATES_FILE
# Usage: prices.sh DATES_FILE COMMODITY BASE_CURRENCY
set -euo pipefail
[ $# -ne 3 ] && { echo "usage: $0 DATES_FILE COMMODITY BASE" >&2; exit 1; }

dates_file="$1"
sym="$2"
base="$3"

# Exit cleanly if there are no dates (no transactions in this commodity)
[ -s "${dates_file}" ] || exit 0

first=$(head -n1 "${dates_file}")
last=$(tail -n1 "${dates_file}")

# Commodity-specific source and symbol configuration.
# Extend this case statement for each commodity you track.
case "${sym}" in
  USD)
    source="yahoo"
    yahoo_sym="USD${base}=X"
    quantize="--quantize 5"
    display_sym="\$"   # rewrite USD → $ in the output
    ;;
  EUR)
    source="yahoo"
    yahoo_sym="EUR${base}=X"
    quantize="--quantize 5"
    display_sym="EUR"
    ;;
  *)
    echo "No price source configured for ${sym}" >&2
    exit 1
    ;;
esac

# Fetch the full date range, then filter to only the dates we actually need.
# This avoids making one API call per date.
pricehist fetch "${source}" "${yahoo_sym}" \
    -s "${first}" -e "${last}" \
    ${quantize} \
    -o ledger \
    --fmt-time '' \
    --fmt-base "${display_sym}" \
    --fmt-quote "£" \
    --fmt-symbol left \
  | grep -Ff "${dates_file}"
```

Make it executable:

```bash
chmod +x sources/prices/prices.sh
```

---

## Step 3: configure hledger-build.toml

Add two `dyngen` custom report entries per commodity — one to generate the
`.dates` file, one to generate the `.prices` file. The prices step depends on
the dates file being built first.

```toml
# ── Commodity price fetching ──────────────────────────────────────────────────
# Generates sources/prices/{year}/USD.prices for each year.
# Requires: pip install pricehist

[[custom_reports]]
name   = "usd-dates"
output = "sources/prices/{year}/USD.dates"
script = "./sources/prices/price_dates.sh"
years  = "all"
args   = ["{year}", "USD"]
dyngen = true

[[custom_reports]]
name       = "usd-prices"
output     = "sources/prices/{year}/USD.prices"
script     = "./sources/prices/prices.sh"
years      = "all"
args       = ["sources/prices/{year}/USD.dates", "USD", "GBP"]
dyngen     = true
depends_on = ["sources/prices/{year}/USD.dates"]
```

Repeat the pair for each additional commodity (EUR, AAPL, etc.), adjusting
the symbol and the `args`.

---

## Step 4: include the prices in your year journals

In each `{year}.journal`, add an include for the price file **before** any
transaction data:

```ledger
; 2026.journal
include commodities.journal
include sources/prices/2026/USD.prices   ← add this
include sources/_manual_/2026/opening.journal
include sources/2026-imports.journal
```

On the first run the file does not exist yet. hledger is lenient about missing
price files when using `include` — but if it isn't, use hledger's
`include?` directive (with a `?`) to make it optional:

```ledger
include? sources/prices/2026/USD.prices
```

---

## Step 5: enable cost-based reporting

Add `--cost --value=then,£` to the reports that should convert commodities.
In `hledger-build.toml`:

```toml
[reports.income_statement]
args = ["is", "--flat", "--no-elide", "--cost", "--value=then,£"]

[reports.balance_sheet]
args = ["balancesheet", "--no-elide", "--cost", "--value=then,£"]
```

Or apply it only to specific custom reports if you prefer to keep native-
currency reports separate.

---

## How it works end-to-end

1. `hledger-build run` runs Pass 1 (ingest), producing `{year}-all.journal`
   and the year's full journal graph.
2. Pass 2 starts. The `usd-dates` step runs `price_dates.sh 2026 USD`,
   which queries your journal for USD transactions and writes their dates to
   `sources/prices/2026/USD.dates`.
3. The `usd-prices` step runs next (it depends on `USD.dates`). It calls
   `prices.sh`, which fetches prices from Yahoo Finance for that date range
   and writes P-directives to `sources/prices/2026/USD.prices`.
4. All subsequent report steps now see the price data via the `include` in
   `2026.journal`.
5. On subsequent runs, hledger-build hashes the script, its arguments, and
   the content of its dependencies. If nothing changed, the step is skipped.

The `dyngen = true` flag on both steps tells hledger-build that the output
is included by the year journal, preventing the circular dependency:
`2026.journal` → `USD.prices` → `usd-prices` step → `2026.journal`.

---

## Adding a new commodity

1. Add a `commodity` declaration to `commodities.journal`.
2. Add a `case` block for it in `prices.sh` with the correct Yahoo Finance
   symbol and formatting options.
3. Add a `usd-dates` + `usd-prices` pair (renamed for the commodity) to
   `hledger-build.toml`.
4. Add `include sources/prices/{year}/SYMBOL.prices` to your year journals.

---

## Tips

**Manual price files:** If you prefer to maintain prices by hand (e.g. for
illiquid assets with no market data), just write the P-directives directly
into `sources/prices/{year}/SYMBOL.prices` and skip the pricehist setup.
hledger-build will track the file's content hash and only regenerate
downstream reports when it changes.

**Checking available sources:** `pricehist sources` lists all supported price
providers (Yahoo Finance, Coinbase, ECB, Bank of Canada, and more).

**Inverting a quote:** Some Yahoo Finance symbols quote the pair in the
opposite direction. Pass `--invert` to pricehist if prices look wrong:

```bash
pricehist fetch yahoo "GBPUSD=X" --invert ...
```

**Date gaps:** pricehist fetches for the full range between first and last
date, then `grep` filters to only the dates present in your journal. Weekend
and holiday prices are excluded automatically.
