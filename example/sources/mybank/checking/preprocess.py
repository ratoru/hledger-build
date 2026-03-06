#!/usr/bin/env python3
"""preprocess – optional CSV pre-processing script for MyBank checking.

hledger-build runs this script before importing each raw CSV file.
It receives the raw CSV path as sys.argv[1] and must write the cleaned CSV
to stdout.

This script adds three extra columns for foreign-currency transactions:
  fxcurrency – the foreign currency symbol (e.g. "€"), empty for native txns
  fxamount   – the bare foreign amount (e.g. "8.50"), empty for native txns
  fxcost     – the absolute native amount (e.g. "10.00"), empty for native txns

The rules file uses `currency2 %fxcurrency` to override the global `currency $`
for the second posting. This supports any currency without touching the rules.
"""

import csv
import re
import sys

# Maps ISO currency codes found in bank descriptions to their display symbols.
CURRENCY_SYMBOLS = {
    "EUR": "€",
    "GBP": "£",
}

# Amounts are always written with dot as decimal separator, matching the
# `decimal-mark .` directive in main.rules. hledger will format them for
# display according to each commodity's declaration in commodities.journal.
FX_PATTERN = re.compile(r"\b([A-Z]{3}) (\d+\.\d+)\b")

with open(sys.argv[1], newline="") as f:
    reader = csv.reader(f)
    writer = csv.writer(sys.stdout)
    for i, row in enumerate(reader):
        if i == 0:
            writer.writerow(row + ["fxcurrency", "fxamount", "fxcost"])
            continue
        description = row[1] if len(row) > 1 else ""
        m = FX_PATTERN.search(description)
        if m and m.group(1) in CURRENCY_SYMBOLS:
            fxcurrency = CURRENCY_SYMBOLS[m.group(1)]
            fxamount = m.group(2)
            fxcost = row[2].lstrip("-") if len(row) > 2 else ""
        else:
            fxcurrency = fxamount = fxcost = ""
        writer.writerow(row + [fxcurrency, fxamount, fxcost])
