package integration_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ratoru/hledger-build/internal/config"
	"github.com/ratoru/hledger-build/internal/passes"
	"github.com/ratoru/hledger-build/internal/runner"
)

// update controls whether golden files are regenerated instead of compared.
// Usage: go test ./internal/integration/... -update
var update = flag.Bool("update", false, "regenerate golden files in testdata/golden/")

// ── Serialisation ─────────────────────────────────────────────────────────────

// formatSteps serialises steps into a deterministic, human-readable text block
// suitable for golden file comparison. Steps are sorted by output path and deps
// are sorted alphabetically within each step.
func formatSteps(steps []runner.Step) string {
	sorted := make([]runner.Step, len(steps))
	copy(sorted, steps)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Output < sorted[j].Output
	})

	var buf strings.Builder
	for _, s := range sorted {
		deps := make([]string, len(s.Deps))
		copy(deps, s.Deps)
		sort.Strings(deps)

		fmt.Fprintf(&buf, "output: %s\n", s.Output)
		fmt.Fprintf(&buf, "command: %s\n", s.Command)
		fmt.Fprintf(&buf, "args: %s\n", strings.Join(s.Args, " "))
		fmt.Fprintf(&buf, "deps: %s\n", strings.Join(deps, " "))
		fmt.Fprintln(&buf)
	}
	return buf.String()
}

// ── Golden file helper ────────────────────────────────────────────────────────

// goldenCheck compares got against testdata/golden/<name>.txt.
// With -update it writes the file instead of comparing.
func goldenCheck(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden %q: %v", path, err)
		}
		t.Logf("updated golden file: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %q (run with -update to create/refresh): %v", path, err)
	}
	if string(want) != got {
		t.Errorf("golden mismatch for %q\n"+
			"To regenerate: go test ./internal/integration/... -run %s -update\n\n"+
			"--- got ---\n%s\n--- want ---\n%s",
			name, t.Name(), got, string(want))
	}
}

// ── Pass 1 golden test ────────────────────────────────────────────────────────

// TestGoldenPass1Steps verifies that Pass 1 step generation for the standard
// 2-source × 2-year fixture produces a stable, expected set of shell commands.
//
// This catches regressions in path computation, input-directory propagation
// when an optional stage is skipped, and the overall step structure.
func TestGoldenPass1Steps(t *testing.T) {
	dir := t.TempDir()
	setupFixture(t, dir)

	cfg := fixtureConfig(dir)

	steps, err := passes.GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("GeneratePass1Steps: %v", err)
	}

	goldenCheck(t, "pass1_steps", formatSteps(steps))
}

// ── Pass 2 golden test ────────────────────────────────────────────────────────

// TestGoldenPass2Steps verifies that Pass 2 step generation for a 2-year
// config with realistic year-journal includes produces the exact expected
// hledger command-lines and dependency lists.
//
// This catches regressions in built-in report args, closing/opening balance
// argument construction, and dependency propagation from year journals.
func TestGoldenPass2Steps(t *testing.T) {
	dir := t.TempDir()

	// Create year journals with known includes so GetIncludes returns a full
	// dependency list that appears in the golden file.
	for _, year := range []int{2023, 2024} {
		yearStr := fmt.Sprintf("%d", year)
		content := fmt.Sprintf(
			"include sources/bank1/checking/journal/%s/jan.journal\n"+
				"include sources/bank2/savings/journal/%s/jan.journal\n",
			yearStr, yearStr)
		mustWrite(t, filepath.Join(dir, yearStr+".journal"), content)

		// Create the referenced stub journals so GetIncludes can follow them.
		for _, src := range []string{"bank1/checking", "bank2/savings"} {
			mustWrite(t,
				filepath.Join(dir, "sources", src, "journal", yearStr, "jan.journal"),
				"; stub\n")
		}
	}

	cfg := &config.Config{
		ProjectRoot:   dir,
		HledgerBinary: "hledger",
		FirstYear:     2023,
		CurrentYear:   2024,
		EquityQuery:   "assets|liabilities|debts",
		Directories: config.Directories{
			Sources: "sources",
			Reports: "reports",
			Raw:     "raw",
			Cleaned: "cleaned",
			Journal: "journal",
			Build:   ".build",
			Prices:  "prices",
			Manual:  "_manual_",
		},
		Reports: config.Reports{
			Transactions:   config.BuiltinReport{Args: []string{"print"}, Enabled: true},
			Accounts:       config.BuiltinReport{Args: []string{"accounts"}, Enabled: true},
			IncomeExpenses: config.BuiltinReport{Args: []string{"is", "--flat", "--no-elide", "--cost"}, Enabled: true},
			BalanceSheet:   config.BuiltinReport{Args: []string{"balancesheet", "--no-elide"}, Enabled: true},
			Cashflow:       config.BuiltinReport{Args: []string{"cashflow", "not:desc:(opening balances)", "--no-elide"}, Enabled: true},
			Unknown:        config.BuiltinReport{Args: []string{"print", "unknown"}, Enabled: true},
		},
		Sources: map[string]config.SourceOverride{},
	}

	steps, err := passes.GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("GeneratePass2Steps: %v", err)
	}

	goldenCheck(t, "pass2_steps", formatSteps(steps))
}
