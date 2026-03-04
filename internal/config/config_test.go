package config

import (
	"os"
	"path/filepath"
	"testing"
)

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdirAll %s: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}

// TestDefaults verifies that Load returns correct defaults with no config file.
func TestDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "hledger-build.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HledgerBinary != "hledger" {
		t.Errorf("HledgerBinary = %q, want %q", cfg.HledgerBinary, "hledger")
	}
	if cfg.EquityQuery != "assets|liabilities|debts" {
		t.Errorf("EquityQuery = %q", cfg.EquityQuery)
	}
	if cfg.Directories.Sources != "sources" {
		t.Errorf("Directories.Sources = %q", cfg.Directories.Sources)
	}
	if cfg.Directories.Build != ".build" {
		t.Errorf("Directories.Build = %q", cfg.Directories.Build)
	}
	if cfg.Directories.Manual != "_manual_" {
		t.Errorf("Directories.Manual = %q", cfg.Directories.Manual)
	}
	if len(cfg.Pipeline) == 0 {
		t.Error("Pipeline should have default stages")
	}
	if len(cfg.Reports.Transactions.Args) == 0 {
		t.Error("Reports.Transactions should have default args")
	}
	if cfg.Jobs <= 0 {
		t.Error("Jobs should be > 0 (resolved from NumCPU)")
	}
	if !cfg.Reports.Metrics.Enabled {
		t.Error("Reports.Metrics.Enabled should default to true")
	}
	if cfg.Reports.Metrics.FireFactor != 25 {
		t.Errorf("Reports.Metrics.FireFactor = %d, want 25", cfg.Reports.Metrics.FireFactor)
	}
	if len(cfg.Reports.Metrics.Accounts.ExcludeExpenses) != 1 || cfg.Reports.Metrics.Accounts.ExcludeExpenses[0] != "expenses:gross" {
		t.Errorf("Reports.Metrics.Accounts.ExcludeExpenses = %v, want [expenses:gross]",
			cfg.Reports.Metrics.Accounts.ExcludeExpenses)
	}
	if len(cfg.Reports.Metrics.Accounts.ExcludeRevenue) != 1 || cfg.Reports.Metrics.Accounts.ExcludeRevenue[0] != "revenue:gift" {
		t.Errorf("Reports.Metrics.Accounts.ExcludeRevenue = %v, want [revenue:gift]",
			cfg.Reports.Metrics.Accounts.ExcludeRevenue)
	}
	if cfg.Reports.Metrics.Accounts.CashAssets != "assets:cash" {
		t.Errorf("Reports.Metrics.Accounts.CashAssets = %q, want %q",
			cfg.Reports.Metrics.Accounts.CashAssets, "assets:cash")
	}
}

// TestDefaultPipeline checks that the default pipeline has preprocess + convert.
func TestDefaultPipeline(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(filepath.Join(dir, "hledger-build.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Pipeline) != 2 {
		t.Fatalf("expected 2 pipeline stages, got %d", len(cfg.Pipeline))
	}
	if cfg.Pipeline[0].Name != "preprocess" {
		t.Errorf("stage 0 name = %q, want preprocess", cfg.Pipeline[0].Name)
	}
	if !cfg.Pipeline[0].Optional {
		t.Error("preprocess stage should be optional")
	}
	if cfg.Pipeline[1].Name != "convert" {
		t.Errorf("stage 1 name = %q, want convert", cfg.Pipeline[1].Name)
	}
}

// TestSourceDiscovery checks that sources with raw/ dirs are found.
func TestSourceDiscovery(t *testing.T) {
	dir := t.TempDir()
	// Create sources with raw/ dirs
	mkdirAll(t, filepath.Join(dir, "sources", "lloyds", "raw"))
	mkdirAll(t, filepath.Join(dir, "sources", "chase-checking", "raw"))
	// Nested source
	mkdirAll(t, filepath.Join(dir, "sources", "john", "revolut", "raw"))
	// Non-source dir (no raw/)
	mkdirAll(t, filepath.Join(dir, "sources", "notasource"))
	// Manual dir — should be skipped
	mkdirAll(t, filepath.Join(dir, "sources", "_manual_", "2023"))

	cfg, err := Load(filepath.Join(dir, "hledger-build.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	found := map[string]bool{}
	for _, s := range cfg.DiscoveredSources {
		found[s] = true
	}

	if !found["lloyds"] {
		t.Error("expected to find source 'lloyds'")
	}
	if !found["chase-checking"] {
		t.Error("expected to find source 'chase-checking'")
	}
	if !found["john/revolut"] {
		t.Error("expected to find source 'john/revolut'")
	}
	if found["notasource"] {
		t.Error("'notasource' has no raw/ dir, should not be discovered")
	}
	for _, s := range cfg.DiscoveredSources {
		if s == "_manual_" || s == "_manual_/2023" {
			t.Errorf("manual dir should not be discovered as source: %q", s)
		}
	}
}

// TestYearDiscovery checks that years are found from raw/ and prices/ dirs.
func TestYearDiscovery(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "sources", "lloyds", "raw", "2021"))
	mkdirAll(t, filepath.Join(dir, "sources", "lloyds", "raw", "2023"))
	mkdirAll(t, filepath.Join(dir, "sources", "chase", "raw", "2022"))
	mkdirAll(t, filepath.Join(dir, "sources", "prices", "2020"))
	mkdirAll(t, filepath.Join(dir, "sources", "_manual_", "2019"))

	cfg, err := Load(filepath.Join(dir, "hledger-build.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.FirstYear != 2019 {
		t.Errorf("FirstYear = %d, want 2019", cfg.FirstYear)
	}
	if cfg.CurrentYear != 2023 {
		t.Errorf("CurrentYear = %d, want 2023", cfg.CurrentYear)
	}
}

// TestYearOverride verifies that config file values override discovery.
func TestYearOverride(t *testing.T) {
	dir := t.TempDir()
	mkdirAll(t, filepath.Join(dir, "sources", "lloyds", "raw", "2019"))
	mkdirAll(t, filepath.Join(dir, "sources", "lloyds", "raw", "2023"))

	writeFile(t, filepath.Join(dir, "hledger-build.toml"),
		"first_year = 2021\ncurrent_year = 2022\n")

	cfg, err := Load(filepath.Join(dir, "hledger-build.toml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FirstYear != 2021 {
		t.Errorf("FirstYear = %d, want 2021", cfg.FirstYear)
	}
	if cfg.CurrentYear != 2022 {
		t.Errorf("CurrentYear = %d, want 2022", cfg.CurrentYear)
	}
}

// TestRulesDiscovery checks that main.rules is discovered level by level and
// the most specific one is returned as rulesFile.
func TestRulesDiscovery(t *testing.T) {
	dir := t.TempDir()
	dirs := Directories{
		Sources: "sources",
		Reports: "reports",
		Raw:     "raw",
		Cleaned: "cleaned",
		Journal: "journal",
		Build:   ".build",
		Prices:  "sources/prices",
		Manual:  "_manual_",
	}

	writeFile(t, filepath.Join(dir, "sources", "main.rules"), "# global\n")
	writeFile(t, filepath.Join(dir, "sources", "lloyds", "main.rules"), "include ../main.rules\n")
	// Non-main rules file and unrelated file should be ignored.
	writeFile(t, filepath.Join(dir, "sources", "lloyds", "other.rules"), "")
	writeFile(t, filepath.Join(dir, "sources", "lloyds", "preprocess"), "")

	rulesFile, allFiles, err := DiscoverRulesFiles(dir, dirs, "lloyds")
	if err != nil {
		t.Fatalf("DiscoverRulesFiles: %v", err)
	}

	// Most specific main.rules wins.
	if rulesFile != "sources/lloyds/main.rules" {
		t.Errorf("rulesFile = %q, want sources/lloyds/main.rules", rulesFile)
	}
	// Both main.rules files are tracked as deps.
	if len(allFiles) != 2 {
		t.Fatalf("expected 2 allFiles, got %d: %v", len(allFiles), allFiles)
	}
	if allFiles[0] != "sources/main.rules" {
		t.Errorf("allFiles[0] = %q, want sources/main.rules", allFiles[0])
	}
	if allFiles[1] != "sources/lloyds/main.rules" {
		t.Errorf("allFiles[1] = %q, want sources/lloyds/main.rules", allFiles[1])
	}
}

// TestRulesDiscoveryFallback checks that a source with no main.rules falls
// back to the nearest parent's main.rules.
func TestRulesDiscoveryFallback(t *testing.T) {
	dir := t.TempDir()
	dirs := Directories{
		Sources: "sources",
		Reports: "reports",
		Raw:     "raw",
		Cleaned: "cleaned",
		Journal: "journal",
		Build:   ".build",
		Prices:  "sources/prices",
		Manual:  "_manual_",
	}

	writeFile(t, filepath.Join(dir, "sources", "main.rules"), "# global\n")
	// No main.rules in sources/lloyds/.

	rulesFile, allFiles, err := DiscoverRulesFiles(dir, dirs, "lloyds")
	if err != nil {
		t.Fatalf("DiscoverRulesFiles: %v", err)
	}

	if rulesFile != "sources/main.rules" {
		t.Errorf("rulesFile = %q, want sources/main.rules", rulesFile)
	}
	if len(allFiles) != 1 || allFiles[0] != "sources/main.rules" {
		t.Errorf("allFiles = %v, want [sources/main.rules]", allFiles)
	}
}

// TestRulesDiscoveryNone checks that no rules files returns empty results.
func TestRulesDiscoveryNone(t *testing.T) {
	dir := t.TempDir()
	dirs := Directories{
		Sources: "sources",
		Reports: "reports",
		Raw:     "raw",
		Cleaned: "cleaned",
		Journal: "journal",
		Build:   ".build",
		Prices:  "sources/prices",
		Manual:  "_manual_",
	}

	rulesFile, allFiles, err := DiscoverRulesFiles(dir, dirs, "lloyds")
	if err != nil {
		t.Fatalf("DiscoverRulesFiles: %v", err)
	}
	if rulesFile != "" {
		t.Errorf("rulesFile = %q, want empty", rulesFile)
	}
	if len(allFiles) != 0 {
		t.Errorf("allFiles = %v, want empty", allFiles)
	}
}

// TestRulesDiscoveryNested checks that the deepest main.rules wins for nested sources.
func TestRulesDiscoveryNested(t *testing.T) {
	dir := t.TempDir()
	dirs := Directories{
		Sources: "sources",
		Reports: "reports",
		Raw:     "raw",
		Cleaned: "cleaned",
		Journal: "journal",
		Build:   ".build",
		Prices:  "sources/prices",
		Manual:  "_manual_",
	}

	writeFile(t, filepath.Join(dir, "sources", "main.rules"), "# global\n")
	writeFile(t, filepath.Join(dir, "sources", "john", "main.rules"), "# john\n")
	writeFile(t, filepath.Join(dir, "sources", "john", "revolut", "main.rules"), "# revolut\n")

	rulesFile, allFiles, err := DiscoverRulesFiles(dir, dirs, "john/revolut")
	if err != nil {
		t.Fatalf("DiscoverRulesFiles: %v", err)
	}

	if rulesFile != "sources/john/revolut/main.rules" {
		t.Errorf("rulesFile = %q, want sources/john/revolut/main.rules", rulesFile)
	}
	if len(allFiles) != 3 {
		t.Fatalf("expected 3 allFiles, got %d: %v", len(allFiles), allFiles)
	}
	if allFiles[0] != "sources/main.rules" {
		t.Errorf("allFiles[0] = %q", allFiles[0])
	}
	if allFiles[1] != "sources/john/main.rules" {
		t.Errorf("allFiles[1] = %q", allFiles[1])
	}
	if allFiles[2] != "sources/john/revolut/main.rules" {
		t.Errorf("allFiles[2] = %q", allFiles[2])
	}
}
