package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const defaultToml = `# hledger-build.toml
# Everything below is optional. An empty file works
# if your directory layout follows conventions.

# --- Override discovered year range ---
# first_year = 2014           # Default: earliest year found in raw/{year}/ dirs
# current_year = 2026         # Default: latest year found in raw/{year}/ dirs

# --- Optional settings (showing defaults) ---
# hledger_binary = "hledger"
# equity_query = "assets|liabilities|debts"
# jobs = 0                    # 0 = runtime.NumCPU()

# --- Optional directory name overrides (showing defaults) ---
# [directories]
# sources = "sources"
# reports = "reports"
# raw = "raw"
# cleaned = "cleaned"
# journal = "journal"
# build = ".build"
# prices = "prices"
# manual = "_manual_"

# --- Built-in Reports ---
# Standard hledger reports generated for each year. All enabled by default.
# Only add this section to customise args or disable specific reports.
# [reports]
# transactions     = { args = ["print"] }
# accounts         = { args = ["accounts"] }
# income_expenses  = { args = ["is", "--flat", "--no-elide", "--cost"] }
# balance_sheet    = { args = ["balancesheet", "--no-elide"] }
# cashflow         = { args = ["cashflow", "not:desc:(opening balances)", "--no-elide"] }
# unknown          = { args = ["print", "unknown"] }
#
# Monthly financial metrics (daily spending, savings rate, net worth, FIRE#).
# Enabled by default. Set your age to unlock AAW/PAW wealth benchmarks.
# [reports.metrics]
# enabled     = true
# fire_factor = 25          # FIRE multiplier (default 25 = 4% rule)
# age         = 0           # Set your age to enable AAW/PAW wealth benchmarks (0 = skip)
# currency    = ""          # e.g. "USD" to convert all values via --value=end
# [reports.metrics.accounts]
# exclude_expenses = ["expenses:gross"]   # payroll deductions excluded from daily avg
# exclude_income   = ["income:gift"]      # windfalls excluded from daily avg
# cash_assets      = "assets:cash"        # liquid account for short runway

# --- Custom Reports ---
# Investment ROI: uncomment and adjust the account queries to match your setup.
# Record contributions to assets:pension:* (or similar) and periodic valuations
# against equity:unrealized_pnl, then rebuild to get IRR and TWR.
# See the docs for a full walkthrough.
#
# [[custom_reports]]
# name   = "investments"
# output = "investments.txt"
# script = "hledger"
# years  = "all"
# args   = [
#   "roi",
#   "-f", "all.journal",
#   "--investment", "acct:assets:pension",
#   "--pnl", "acct:equity:unrealized_pnl",
#   "-Y",
# ]
`

// commoditiesJournal is the content of commodities.journal written at the project root.
const commoditiesJournal = `; commodities.journal – commodity format declarations.
;
; These directives tell hledger the canonical display format for each commodity.
; Include this file in every year journal to ensure consistent formatting
; regardless of which transactions hledger encounters first.
;
; Adjust the format and add more commodities or assets as needed.

; US Dollar — two decimal places, comma thousands separator
commodity $1,000.00

; Uncomment and adjust for other currencies or assets you track:
; commodity £1_000.00
; commodity €1.000,00
; commodity AAPL 1.0000
`

// exampleRules is the hledger CSV rules file written to sources/mybank/checking/.
const exampleRules = `# mybank.rules – hledger CSV import rules for MyBank checking account.
# Documentation: https://hledger.org/hledger.html#csv-format

# ── CSV format ────────────────────────────────────────────────────────────────

# Skip the header row (the first line of the CSV export).
skip 1

# Map the three CSV columns to hledger field names by position (1-based).
# Adjust these to match your bank's actual column order and headers.
fields date, description, amount

# Date format used by this bank's CSV export.
date-format %Y-%m-%d

# ── Accounts ──────────────────────────────────────────────────────────────────

# account1 is the hledger account that holds these funds (the imported account).
account1 assets:mybank:checking

# Default counterpart account for transactions that don't match any rule below.
# These land in expenses:unknown for manual review.
account2 expenses:unknown

# Base Currency
currency1 $

# ── Classification rules ──────────────────────────────────────────────────────
# Rules are applied top-to-bottom; later matches override earlier ones.
# Each pattern is a case-insensitive regex matched against the whole CSV record
# (all columns joined). Use %FIELDNAME to restrict to a specific column, e.g.
#   if %description ^PAYROLL
#     account2 income:salary

if PAYROLL
  account2 income:salary

if GROCERY
  account2 expenses:food:groceries

# Add more rules as you encounter new payees, for example:
# if NETFLIX|SPOTIFY
#   account2 expenses:subscriptions
# if ATM|CASH WITHDRAWAL
#   account2 expenses:cash
`

// examplePreprocess is the shell script written to sources/mybank/checking/preprocess.
const examplePreprocess = `#!/usr/bin/env sh
# preprocess – optional CSV pre-processing script for MyBank checking.
#
# hledger-build runs this script before importing each raw CSV file.
# It receives the raw CSV path as $1 and must write the cleaned CSV to stdout.
#
# This example passes the file through unchanged. Customise it to suit
# your bank's CSV quirks.
set -eu
cat "$1"
`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "init",
		Short:        "Scaffold a new hledger-build project in the current directory",
		RunE:         func(cmd *cobra.Command, args []string) error { return runInit() },
		SilenceUsage: true,
	}
}

func runInit() error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}

	tomlPath := filepath.Join(cwd, "hledger-build.toml")
	if _, err := os.Stat(tomlPath); err == nil {
		return fmt.Errorf("hledger-build.toml already exists; aborting to avoid overwriting")
	}

	year := time.Now().Year()
	yearStr := strconv.Itoa(year)

	// Create standard directories.
	dirs := []string{
		filepath.Join(cwd, "sources", "mybank", "checking", "raw", yearStr),
		filepath.Join(cwd, "sources", "_manual_", yearStr),
		filepath.Join(cwd, "reports"),
		filepath.Join(cwd, ".build"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", d, err)
		}
		fmt.Printf("created  %s/\n", relOrAbs(cwd, d))
	}

	// Write hledger-build.toml with commented defaults.
	if err := os.WriteFile(tomlPath, []byte(defaultToml), 0o644); err != nil {
		return fmt.Errorf("writing hledger-build.toml: %w", err)
	}
	fmt.Println("created  hledger-build.toml")

	// Write commodities.journal — shared commodity format declarations included
	// by every year journal to ensure consistent amount formatting.
	commoditiesPath := filepath.Join(cwd, "commodities.journal")
	if _, err := os.Stat(commoditiesPath); os.IsNotExist(err) {
		if err := os.WriteFile(commoditiesPath, []byte(commoditiesJournal), 0o644); err != nil {
			return fmt.Errorf("writing commodities.journal: %w", err)
		}
		fmt.Println("created  commodities.journal")
	}

	// Write a sample CSV file with two transactions so "hledger-build run"
	// works out of the box and new users can see a real pipeline in action.
	csvPath := filepath.Join(cwd, "sources", "mybank", "checking", "raw", yearStr, "transactions.csv")
	if err := os.WriteFile(csvPath, []byte(exampleCSVContent(year)), 0o644); err != nil {
		return fmt.Errorf("writing example CSV: %w", err)
	}
	fmt.Printf("created  sources/mybank/checking/raw/%s/transactions.csv\n", yearStr)

	// Write opening.journal in the _manual_ directory for initial account balances.
	openingPath := filepath.Join(cwd, "sources", "_manual_", yearStr, "opening.journal")
	if err := os.WriteFile(openingPath, []byte(openingJournalContent(year)), 0o644); err != nil {
		return fmt.Errorf("writing opening.journal: %w", err)
	}
	fmt.Printf("created  sources/_manual_/%s/opening.journal\n", yearStr)

	// Write the hledger CSV rules file that maps CSV columns to accounts.
	rulesPath := filepath.Join(cwd, "sources", "mybank", "checking", "mybank.rules")
	if err := os.WriteFile(rulesPath, []byte(exampleRules), 0o644); err != nil {
		return fmt.Errorf("writing mybank.rules: %w", err)
	}
	fmt.Println("created  sources/mybank/checking/mybank.rules")

	// Write the optional preprocess script (pass-through by default).
	preprocessPath := filepath.Join(cwd, "sources", "mybank", "checking", "preprocess")
	if err := os.WriteFile(preprocessPath, []byte(examplePreprocess), 0o755); err != nil {
		return fmt.Errorf("writing preprocess: %w", err)
	}
	fmt.Println("created  sources/mybank/checking/preprocess")

	// Write {year}.journal — the per-year entry point that hledger-build uses
	// for Pass 2 reports. It includes the journal files generated by Pass 1.
	yearJournalPath := filepath.Join(cwd, yearStr+".journal")
	if _, err := os.Stat(yearJournalPath); os.IsNotExist(err) {
		if err := os.WriteFile(yearJournalPath, []byte(yearJournalContent(year)), 0o644); err != nil {
			return fmt.Errorf("writing %s.journal: %w", yearStr, err)
		}
		fmt.Printf("created  %s.journal\n", yearStr)
	}

	// Write all.journal — top-level file for ad-hoc multi-year queries.
	allJournalPath := filepath.Join(cwd, "all.journal")
	if _, err := os.Stat(allJournalPath); os.IsNotExist(err) {
		if err := os.WriteFile(allJournalPath, []byte(allJournalContent(year)), 0o644); err != nil {
			return fmt.Errorf("writing all.journal: %w", err)
		}
		fmt.Println("created  all.journal")
	}

	// Append .build/ to .gitignore if not already present.
	gitignorePath := filepath.Join(cwd, ".gitignore")
	if err := appendIfAbsent(gitignorePath, "# hledger-build dependencies\n.build/\n/hledger-build"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	} else {
		fmt.Println("updated  .gitignore")
	}

	// Check for hledger in PATH.
	if _, err := exec.LookPath("hledger"); err != nil {
		fmt.Fprintln(os.Stderr, "\nwarning: hledger not found in PATH — install it before running hledger-build")
		fmt.Fprintln(os.Stderr, "  https://hledger.org/install.html")
	}

	fmt.Println("\nProject initialised. Next steps:")
	fmt.Println("  1. Run: hledger-build run")
	fmt.Printf("  2. Inspect the generated reports in reports/%s-*.txt\n", yearStr)
	fmt.Println("  3. Replace the sample data with your real bank statements")
	fmt.Println("  4. Update sources/mybank/checking/mybank.rules to match your CSV columns")
	return nil
}

// exampleCSVContent returns three sample transactions for the given year.
// The descriptions are chosen to trigger classification rules in mybank.rules.
func exampleCSVContent(year int) string {
	return fmt.Sprintf(
		"Date,Description,Amount\n"+
			"%d-01-15,ACME CORP PAYROLL,3200.00\n"+
			"%d-01-16,SUBWAY,-20.01\n"+
			"%d-02-03,CORNER GROCERY STORE,-52.40\n",
		year, year, year,
	)
}

// openingJournalContent returns a sample opening-balances journal entry.
// This lives in sources/_manual_/{year}/ and is included by {year}.journal.
func openingJournalContent(year int) string {
	return fmt.Sprintf(
		"; opening.journal – initial account balances as of %d-01-01.\n"+
			";\n"+
			"; Record the real balances of every account on the day you start tracking.\n"+
			"; Use = for balance assertions (hledger verifies the running total matches)\n"+
			"; or plain amounts if you prefer not to assert.\n"+
			"\n"+
			"%d-01-01 opening balances\n"+
			"    assets:mybank:checking         = 1000.00\n"+
			"    assets:cash                    =  200.00\n"+
			"    equity:opening balances\n",
		year, year,
	)
}

// yearJournalContent returns the content of the {year}.journal entry-point file.
// hledger-build auto-generates reports/{year}-imports.journal listing all
// ingest journal includes, so users only need to add their manual entries here.
func yearJournalContent(year int) string {
	return fmt.Sprintf(
		"; %d.journal – hledger entry point for %d.\n"+
			";\n"+
			"; hledger-build auto-generates sources/%d-imports.journal with include\n"+
			"; directives for every source journal produced by the ingest pipeline.\n"+
			"; You only need to maintain manual entries (opening balances, adjustments)\n"+
			"; in this file.\n"+
			";\n"+
			"; For years after the first, also include the generated opening balances:\n"+
			";   include reports/%d-opening.journal\n"+
			"\n"+
			"include commodities.journal\n"+
			"include sources/_manual_/%d/opening.journal\n"+
			"include sources/%d-imports.journal\n",
		year, year, year, year, year, year,
	)
}

// allJournalContent returns the content of all.journal.
// This file is not used by hledger-build itself, but is convenient for
// running ad-hoc hledger queries that span all years.
func allJournalContent(year int) string {
	return fmt.Sprintf(
		"; all.journal – top-level journal spanning all years.\n"+
			";\n"+
			"; Use this for ad-hoc queries across multiple years, e.g.:\n"+
			";   hledger -f all.journal balance\n"+
			";\n"+
			"; Add one include per year as your data grows:\n"+
			"include %d.journal\n",
		year,
	)
}

// appendIfAbsent appends line to the file at path only when not already present.
func appendIfAbsent(path, line string) (err error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for existing := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(existing) == line {
			return nil // already present
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = fmt.Fprintln(f, line)
	return err
}
