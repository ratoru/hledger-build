package passes

import (
	"path/filepath"
	"testing"

	"github.com/ratoru/hledger-build/internal/config"
	"github.com/ratoru/hledger-build/internal/runner"
)

// makePass2Config returns a Config ready for pass2 testing: all built-in
// reports enabled, equity query set, and years overridden from makeConfig's
// defaults. It reuses makeConfig (from pass1_test.go) for directory/pipeline
// settings.
func makePass2Config(dir string, firstYear, currentYear int) *config.Config {
	cfg := makeConfig(dir)
	cfg.FirstYear = firstYear
	cfg.CurrentYear = currentYear
	cfg.EquityQuery = "assets|liabilities|debts"
	cfg.SelfBinary = "hledger-build"
	cfg.Reports = config.Reports{
		Transactions:    config.BuiltinReport{Args: []string{"print"}, Enabled: true},
		Accounts:        config.BuiltinReport{Args: []string{"accounts"}, Enabled: true},
		IncomeStatement: config.BuiltinReport{Args: []string{"is", "--flat", "--no-elide", "--cost"}, Enabled: true},
		BalanceSheet:    config.BuiltinReport{Args: []string{"balancesheet", "--no-elide"}, Enabled: true},
		Cashflow:        config.BuiltinReport{Args: []string{"cashflow", "not:desc:(opening balances)", "--no-elide"}, Enabled: true},
		Unknown:         config.BuiltinReport{Args: []string{"print", "unknown"}, Enabled: true},
		Metrics:         config.MetricsReport{Enabled: true, FireFactor: 25},
	}
	return cfg
}

// findStep returns the first step whose Output matches target, or (zero, false).
func findStep(steps []runner.Step, target string) (runner.Step, bool) {
	for _, s := range steps {
		if s.Output == target {
			return s, true
		}
	}
	return runner.Step{}, false
}

// stepOutputs returns a set of all step output paths.
func stepOutputs(steps []runner.Step) map[string]bool {
	m := make(map[string]bool, len(steps))
	for _, s := range steps {
		m[s.Output] = true
	}
	return m
}

// ── Basic step counts ─────────────────────────────────────────────────────────

// TestPass2NoYears verifies that zero years → zero steps.
func TestPass2NoYears(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 0, 0)
	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(steps))
	}
}

// TestPass2SingleYear: one year → 6 built-in steps + 1 metrics, no opening/closing.
func TestPass2SingleYear(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 6 built-in + 1 metrics; year == first AND current, so no opening/closing.
	if len(steps) != 7 {
		t.Errorf("expected 7 steps, got %d", len(steps))
	}
}

// TestPass2TwoYears: two years → 7×2 builtin+metrics + 1 closing + 1 opening = 16.
func TestPass2TwoYears(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 16 {
		t.Errorf("expected 16 steps, got %d", len(steps))
	}
}

// TestPass2ThreeYears: three years → 7×3 builtin+metrics + 2 closing + 2 opening = 25.
func TestPass2ThreeYears(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2022, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 25 {
		t.Errorf("expected 25 steps, got %d", len(steps))
	}
}

// ── Output paths ──────────────────────────────────────────────────────────────

// TestPass2OutputPaths verifies the exact file paths for all six built-in reports.
func TestPass2OutputPaths(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outputs := stepOutputs(steps)

	wantOutputs := []string{
		"reports/2024-all.journal",
		"reports/2024-accounts.txt",
		"reports/2024-income-statement.txt",
		"reports/2024-balance-sheet.txt",
		"reports/2024-cashflow.txt",
		"reports/2024-unknown.journal",
	}
	for _, w := range wantOutputs {
		if !outputs[w] {
			t.Errorf("missing expected output %q", w)
		}
	}
}

// TestPass2OpeningClosingOutputs checks which years get opening/closing steps.
func TestPass2OpeningClosingOutputs(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	outputs := stepOutputs(steps)

	// Closing for first year (2023), no closing for current (2024).
	if !outputs["reports/2023-closing.journal"] {
		t.Error("missing closing for 2023")
	}
	if outputs["reports/2024-closing.journal"] {
		t.Error("unexpected closing for 2024 (current year)")
	}

	// Opening for current year (2024), no opening for first (2023).
	if !outputs["reports/2024-opening.journal"] {
		t.Error("missing opening for 2024")
	}
	if outputs["reports/2023-opening.journal"] {
		t.Error("unexpected opening for 2023 (first year)")
	}
}

// ── Arguments ─────────────────────────────────────────────────────────────────

// TestPass2BuiltinArgs verifies that a built-in report step uses -f {year}.journal
// followed by the report-specific args.
func TestPass2BuiltinArgs(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := findStep(steps, "reports/2024-balance-sheet.txt")
	if !ok {
		t.Fatal("balance-sheet step not found")
	}
	if len(s.Args) < 2 || s.Args[0] != "-f" || s.Args[1] != "2024.journal" {
		t.Errorf("Args[0:2] = %v, want [-f 2024.journal]", s.Args[:2])
	}
	if !containsStr(s.Args, "balancesheet") {
		t.Errorf("Args %v missing 'balancesheet'", s.Args)
	}
}

// TestPass2ClosingArgs checks the hledger arguments for a closing-balance step.
func TestPass2ClosingArgs(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := findStep(steps, "reports/2023-closing.journal")
	if !ok {
		t.Fatal("closing step for 2023 not found")
	}
	if !containsStr(s.Args, "--closing") {
		t.Errorf("closing Args %v missing --closing", s.Args)
	}
	// Equity query must appear as an argument.
	if !containsStr(s.Args, cfg.EquityQuery) {
		t.Errorf("closing Args %v missing equity query", s.Args)
	}
	// -e must be set to year+1.
	if !containsStr(s.Args, "2024") {
		t.Errorf("closing Args %v missing year boundary '2024'", s.Args)
	}
	if !containsStr(s.Args, "-I") {
		t.Errorf("closing Args %v missing -I", s.Args)
	}
}

// TestPass2OpeningArgs checks the hledger arguments for an opening-balance step.
func TestPass2OpeningArgs(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := findStep(steps, "reports/2024-opening.journal")
	if !ok {
		t.Fatal("opening step for 2024 not found")
	}
	if !containsStr(s.Args, "--opening") {
		t.Errorf("opening Args %v missing --opening", s.Args)
	}
	// Opening reads from the *previous* year's journal.
	if !containsStr(s.Args, "2023.journal") {
		t.Errorf("opening Args %v missing 2023.journal", s.Args)
	}
	// -e must be the target year.
	if !containsStr(s.Args, "2024") {
		t.Errorf("opening Args %v missing year boundary '2024'", s.Args)
	}
}

// TestPass2OpeningDepsIncludeClosing verifies that the opening step for year Y
// explicitly depends on the closing journal of year Y-1.
func TestPass2OpeningDepsIncludeClosing(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := findStep(steps, "reports/2024-opening.journal")
	if !ok {
		t.Fatal("opening step for 2024 not found")
	}
	if !containsStr(s.Deps, "reports/2023-closing.journal") {
		t.Errorf("opening 2024 Deps %v missing reports/2023-closing.journal", s.Deps)
	}
}

// TestPass2ReportsDependOnOpening verifies that for years after the first, all
// built-in report steps explicitly depend on the generated opening journal.
// GetIncludes cannot discover that dep when the file doesn't exist yet (first
// run), so pass2 must inject it directly into reportDeps.
func TestPass2ReportsDependOnOpening(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reportOutputs := []string{
		"reports/2024-all.journal",
		"reports/2024-accounts.txt",
		"reports/2024-income-statement.txt",
		"reports/2024-balance-sheet.txt",
		"reports/2024-cashflow.txt",
		"reports/2024-unknown.journal",
		"reports/2024-metrics.txt",
	}
	for _, out := range reportOutputs {
		s, ok := findStep(steps, out)
		if !ok {
			t.Errorf("step %q not found", out)
			continue
		}
		if !containsStr(s.Deps, "reports/2024-opening.journal") {
			t.Errorf("step %q Deps %v missing reports/2024-opening.journal", out, s.Deps)
		}
	}

	// First-year (2023) report steps must NOT depend on a non-existent 2023-opening.
	s, ok := findStep(steps, "reports/2023-balance-sheet.txt")
	if !ok {
		t.Fatal("2023 balance-sheet step not found")
	}
	if containsStr(s.Deps, "reports/2023-opening.journal") {
		t.Error("2023 report unexpectedly depends on reports/2023-opening.journal")
	}
}

// ── Deps from includes ────────────────────────────────────────────────────────

// TestPass2YearDepsFromIncludes verifies that includes found in the year journal
// are propagated as deps to all report steps for that year.
func TestPass2YearDepsFromIncludes(t *testing.T) {
	dir := t.TempDir()
	cfg := makePass2Config(dir, 2024, 2024)

	writeFile(t, filepath.Join(dir, "2024.journal"),
		"include sources/bank/journal/2024/stmt.journal\n")
	writeFile(t, filepath.Join(dir, "sources/bank/journal/2024/stmt.journal"),
		"; stub\n")

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range steps {
		if !containsStr(s.Deps, "2024.journal") {
			t.Errorf("step %q: Deps %v missing 2024.journal", s.ID, s.Deps)
		}
		if !containsStr(s.Deps, "sources/bank/journal/2024/stmt.journal") {
			t.Errorf("step %q: Deps %v missing stmt.journal", s.ID, s.Deps)
		}
	}
}

// ── Disabled report ───────────────────────────────────────────────────────────

// TestPass2DisabledReport verifies that a disabled built-in report is omitted.
func TestPass2DisabledReport(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)
	cfg.Reports.Unknown.Enabled = false

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 5 remaining builtins + 1 metrics = 6
	if len(steps) != 6 {
		t.Errorf("expected 6 steps (unknown disabled), got %d", len(steps))
	}
	if _, ok := findStep(steps, "reports/2024-unknown.journal"); ok {
		t.Error("unexpected step for disabled unknown report")
	}
}

// ── All steps are Pass 2 ──────────────────────────────────────────────────────

func TestPass2AllStepsArePass2(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, s := range steps {
		if s.Pass != 2 {
			t.Errorf("step %q: Pass = %d, want 2", s.ID, s.Pass)
		}
	}
}

// ── Custom reports ────────────────────────────────────────────────────────────

// TestPass2CustomReportPerYear: years="all" + {year} in output → one step per year.
func TestPass2CustomReportPerYear(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2023, 2024)
	cfg.CustomReports = []config.CustomReport{
		{
			Name:   "mortgage",
			Output: "{year}-mortgage-interest.journal",
			Script: "./mortgage.sh",
			Args:   []string{"{year}"},
			Years:  "all",
		},
	}

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify a step was generated for each year with correct template expansion.
	for _, year := range []string{"2023", "2024"} {
		want := "reports/" + year + "-mortgage-interest.journal"
		s, ok := findStep(steps, want)
		if !ok {
			t.Errorf("missing step for %s", want)
			continue
		}
		if s.Command != "./mortgage.sh" {
			t.Errorf("mortgage %s Command = %q, want ./mortgage.sh", year, s.Command)
		}
		if len(s.Args) != 1 || s.Args[0] != year {
			t.Errorf("mortgage %s Args = %v, want [%s]", year, s.Args, year)
		}
	}
}

// TestPass2CustomReportAllYearsSingle: years="all" with no {year} → single step.
func TestPass2CustomReportAllYearsSingle(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2022, 2024)
	cfg.CustomReports = []config.CustomReport{
		{
			Name:   "investments",
			Output: "investments.txt",
			Script: "./investments.sh",
			Years:  "all",
		},
	}

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	count := 0
	for _, s := range steps {
		if s.Output == "reports/investments.txt" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 investments step, got %d", count)
	}
}

// TestPass2CustomReportYearRange: year_range → single step with from/to vars.
func TestPass2CustomReportYearRange(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2022, 2024)
	cfg.CustomReports = []config.CustomReport{
		{
			Name:   "tax",
			Output: "{from_year}-{to_year}-tax.txt",
			Script: "./tax.sh",
			Args:   []string{"{from_year}", "{to_year}"},
			YearRange: map[string]string{
				"from_year": "2022",
				"to_year":   "current",
			},
			DependsOn: []string{"{from_year}-closing.journal"},
		},
	}

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := findStep(steps, "reports/2022-2024-tax.txt")
	if !ok {
		t.Fatal("tax step reports/2022-2024-tax.txt not found")
	}
	if s.Command != "./tax.sh" {
		t.Errorf("tax Command = %q, want ./tax.sh", s.Command)
	}
	if len(s.Args) != 2 || s.Args[0] != "2022" || s.Args[1] != "2024" {
		t.Errorf("tax Args = %v, want [2022 2024]", s.Args)
	}
	// depends_on after template expansion → reports/2022-closing.journal
	if !containsStr(s.Deps, "reports/2022-closing.journal") {
		t.Errorf("tax Deps %v missing reports/2022-closing.journal", s.Deps)
	}
}

// TestPass2CustomReportDefault: no years/year_range → single step with depends_on deps.
func TestPass2CustomReportDefault(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)
	cfg.CustomReports = []config.CustomReport{
		{
			Name:      "summary",
			Output:    "summary.txt",
			Script:    "./summary.sh",
			DependsOn: []string{"./reports/2024-balance-sheet.txt"},
		},
	}

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := findStep(steps, "reports/summary.txt")
	if !ok {
		t.Fatal("summary step not found")
	}
	if !containsStr(s.Deps, "reports/2024-balance-sheet.txt") {
		t.Errorf("summary Deps %v missing reports/2024-balance-sheet.txt", s.Deps)
	}
}

// TestPass2DynGen: dyngen=true removes the step's own output from its deps.
func TestPass2DynGen(t *testing.T) {
	dir := t.TempDir()
	cfg := makePass2Config(dir, 2024, 2024)
	cfg.CustomReports = []config.CustomReport{
		{
			Name:   "mortgage",
			Output: "{year}-mortgage-interest.journal",
			Script: "./mortgage.sh",
			Args:   []string{"{year}"},
			Years:  "all",
			DynGen: true,
		},
	}

	// Create a year journal that includes the dyngen output. Also create the
	// output itself so GetIncludes can follow the include and return it in deps.
	writeFile(t, filepath.Join(dir, "2024.journal"),
		"include reports/2024-mortgage-interest.journal\n")
	writeFile(t, filepath.Join(dir, "reports/2024-mortgage-interest.journal"),
		"; stub for dep discovery\n")

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Find the dyngen step (not the builtin ones that might share the output).
	var dynStep runner.Step
	var found bool
	for _, s := range steps {
		if s.Output == "reports/2024-mortgage-interest.journal" && s.Command == "./mortgage.sh" {
			dynStep = s
			found = true
			break
		}
	}
	if !found {
		t.Fatal("dyngen step for 2024-mortgage-interest.journal not found")
	}

	// The output must NOT appear in its own deps.
	for _, d := range dynStep.Deps {
		if d == "reports/2024-mortgage-interest.journal" {
			t.Error("dyngen step has its own output in Deps (circular dependency)")
		}
	}
}

// ── Metrics step ──────────────────────────────────────────────────────────────

// TestPass2MetricsStep verifies that the metrics step is generated with correct fields,
// including all config-derived flags (so that config changes invalidate the hash).
func TestPass2MetricsStep(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	s, ok := findStep(steps, "reports/2024-metrics.txt")
	if !ok {
		t.Fatal("metrics step not found")
	}
	if s.Command != cfg.SelfBinary {
		t.Errorf("Command = %q, want %q", s.Command, cfg.SelfBinary)
	}
	if !s.CaptureStdout {
		t.Error("CaptureStdout should be true")
	}
	if s.Pass != 2 {
		t.Errorf("Pass = %d, want 2", s.Pass)
	}

	// Verify fixed positional args.
	for _, want := range []string{"metrics", "--file", "2024.journal", "--year", "2024"} {
		if !containsStr(s.Args, want) {
			t.Errorf("Args %v missing %q", s.Args, want)
		}
	}
	// Verify config-derived flags are present so hash covers them.
	for _, want := range []string{"--fire-factor", "25", "--exclude-expenses", "--exclude-revenue", "--cash-assets", "--age", "--currency"} {
		if !containsStr(s.Args, want) {
			t.Errorf("Args %v missing %q", s.Args, want)
		}
	}
}

// TestPass2MetricsDisabled verifies that no metrics step is generated when disabled.
func TestPass2MetricsDisabled(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)
	cfg.Reports.Metrics.Enabled = false

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := findStep(steps, "reports/2024-metrics.txt"); ok {
		t.Error("unexpected metrics step when Metrics.Enabled=false")
	}
	// 6 builtins, no metrics
	if len(steps) != 6 {
		t.Errorf("expected 6 steps, got %d", len(steps))
	}
}

// TestPass2MetricsMissingSelfBinary verifies no metrics step when SelfBinary is empty.
func TestPass2MetricsMissingSelfBinary(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2024, 2024)
	cfg.SelfBinary = ""

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if _, ok := findStep(steps, "reports/2024-metrics.txt"); ok {
		t.Error("unexpected metrics step when SelfBinary is empty")
	}
	// 6 builtins, no metrics
	if len(steps) != 6 {
		t.Errorf("expected 6 steps, got %d", len(steps))
	}
}

// ── Unique IDs ────────────────────────────────────────────────────────────────

// TestPass2UniqueIDs verifies that every generated step has a unique ID/Output.
func TestPass2UniqueIDs(t *testing.T) {
	cfg := makePass2Config(t.TempDir(), 2022, 2024)

	steps, err := GeneratePass2Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := make(map[string]bool)
	for _, s := range steps {
		if seen[s.ID] {
			t.Errorf("duplicate step ID %q", s.ID)
		}
		seen[s.ID] = true
	}
}
