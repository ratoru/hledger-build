package passes

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ratoru/hledger-build/internal/config"
)

// helpers

func mkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdirAll %q: %v", path, err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %q: %v", path, err)
	}
}

func containsStr(slice []string, s string) bool {
	return slices.Contains(slice, s)
}

// makeConfig returns a minimal Config rooted at dir with the default pipeline
// and no discovered sources. Tests set DiscoveredSources themselves.
func makeConfig(dir string) *config.Config {
	return &config.Config{
		ProjectRoot:   dir,
		HledgerBinary: "hledger",
		FirstYear:     2023,
		CurrentYear:   2024,
		Directories: config.Directories{
			Sources: "sources",
			Raw:     "raw",
			Cleaned: "cleaned",
			Journal: "journal",
			Build:   ".build",
			Prices:  "prices",
			Manual:  "_manual_",
			Reports: "reports",
		},
		Pipeline: []config.Pipeline{
			{
				Name:      "preprocess",
				Script:    "./preprocess",
				InputDir:  "raw",
				InputGlob: "*.csv",
				OutputDir: "cleaned",
				OutputExt: ".csv",
				Optional:  true,
			},
			{
				Name:      "convert",
				Script:    "hledger",
				InputDir:  "cleaned",
				InputGlob: "*.csv",
				OutputDir: "journal",
				OutputExt: ".journal",
				Optional:  false,
			},
		},
		Sources: map[string]config.SourceOverride{},
	}
}

// TestNoSources returns no steps when DiscoveredSources is empty.
func TestNoSources(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	// cfg.DiscoveredSources is nil (zero value) — no sources.

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 0 {
		t.Errorf("expected 0 steps, got %d", len(steps))
	}
}

// TestPreprocessAbsent verifies that when the optional preprocess script is
// absent, the convert stage reads from raw/ instead of cleaned/.
func TestPreprocessAbsent(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank"}
	cfg.FirstYear = 2024
	cfg.CurrentYear = 2024

	writeFile(t, filepath.Join(dir, "sources/bank/raw/2024/stmt.csv"), "date,amount\n2024-01-01,100\n")
	// No preprocess script.

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only one step: convert reading from raw/.
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d: %v", len(steps), steps)
	}

	s := steps[0]
	if s.Pass != 1 {
		t.Errorf("Pass = %d, want 1", s.Pass)
	}
	if s.Output != "sources/bank/journal/2024/stmt.journal" {
		t.Errorf("Output = %q, want sources/bank/journal/2024/stmt.journal", s.Output)
	}
	if s.Command != "hledger" {
		t.Errorf("Command = %q, want hledger", s.Command)
	}
	if !s.CaptureStdout {
		t.Error("CaptureStdout should be true for hledger convert")
	}
	// Deps must include the raw input file.
	if !containsStr(s.Deps, "sources/bank/raw/2024/stmt.csv") {
		t.Errorf("Deps %v does not contain raw input", s.Deps)
	}
	// Args must reference the raw input file.
	if !containsStr(s.Args, "sources/bank/raw/2024/stmt.csv") {
		t.Errorf("Args %v does not contain raw input path", s.Args)
	}
}

// TestPreprocessPresent verifies that when the preprocess script exists, two
// steps are generated per input file: preprocess then convert.
func TestPreprocessPresent(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank"}
	cfg.FirstYear = 2024
	cfg.CurrentYear = 2024

	writeFile(t, filepath.Join(dir, "sources/bank/raw/2024/stmt.csv"), "date,amount\n2024-01-01,100\n")
	writeFile(t, filepath.Join(dir, "sources/bank/preprocess"), "#!/bin/sh\ncat \"$1\"\n")

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(steps) != 2 {
		t.Fatalf("expected 2 steps (preprocess + convert), got %d", len(steps))
	}

	pre := steps[0]
	if pre.Output != "sources/bank/cleaned/2024/stmt.csv" {
		t.Errorf("preprocess Output = %q, want sources/bank/cleaned/2024/stmt.csv", pre.Output)
	}
	if pre.Command != "./preprocess" {
		t.Errorf("preprocess Command = %q, want ./preprocess", pre.Command)
	}
	if pre.Cwd != "sources/bank" {
		t.Errorf("preprocess Cwd = %q, want sources/bank", pre.Cwd)
	}
	// The input arg must be relative to the source dir (cwd).
	if len(pre.Args) == 0 || pre.Args[0] != "raw/2024/stmt.csv" {
		t.Errorf("preprocess Args = %v, want [raw/2024/stmt.csv]", pre.Args)
	}
	if pre.Pass != 1 {
		t.Errorf("preprocess Pass = %d, want 1", pre.Pass)
	}

	conv := steps[1]
	if conv.Output != "sources/bank/journal/2024/stmt.journal" {
		t.Errorf("convert Output = %q", conv.Output)
	}
	if conv.Command != "hledger" {
		t.Errorf("convert Command = %q, want hledger", conv.Command)
	}
	if conv.Cwd != "" {
		t.Errorf("convert Cwd = %q, want empty (project root)", conv.Cwd)
	}
	// Convert dep must be the cleaned file.
	if !containsStr(conv.Deps, "sources/bank/cleaned/2024/stmt.csv") {
		t.Errorf("convert Deps %v missing cleaned input", conv.Deps)
	}
}

// TestRulesFilesInDeps verifies that discovered rules files appear in the
// convert step's deps and args.
func TestRulesFilesInDeps(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank"}
	cfg.FirstYear = 2024
	cfg.CurrentYear = 2024

	writeFile(t, filepath.Join(dir, "sources/bank/raw/2024/stmt.csv"), "date,amount\n")
	writeFile(t, filepath.Join(dir, "sources/global.rules"), "# global\n")
	writeFile(t, filepath.Join(dir, "sources/bank/bank.rules"), "# bank\n")

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}

	s := steps[0]
	if !containsStr(s.Deps, "sources/global.rules") {
		t.Errorf("Deps %v missing sources/global.rules", s.Deps)
	}
	if !containsStr(s.Deps, "sources/bank/bank.rules") {
		t.Errorf("Deps %v missing sources/bank/bank.rules", s.Deps)
	}
	if !containsStr(s.Args, "sources/global.rules") {
		t.Errorf("Args %v missing sources/global.rules (--rules-file)", s.Args)
	}
	if !containsStr(s.Args, "sources/bank/bank.rules") {
		t.Errorf("Args %v missing sources/bank/bank.rules (--rules-file)", s.Args)
	}
}

// TestExtraDeps verifies that per-source extra_deps are added to every step.
func TestExtraDeps(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank"}
	cfg.FirstYear = 2024
	cfg.CurrentYear = 2024
	cfg.Sources = map[string]config.SourceOverride{
		"bank": {ExtraDeps: []string{"model.json"}},
	}

	writeFile(t, filepath.Join(dir, "sources/bank/raw/2024/stmt.csv"), "data\n")

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(steps) == 0 {
		t.Fatal("expected at least one step, got none")
	}

	wantDep := "sources/bank/model.json"
	for _, s := range steps {
		if !containsStr(s.Deps, wantDep) {
			t.Errorf("step %q Deps %v missing %q", s.ID, s.Deps, wantDep)
		}
	}
}

// TestMultipleYears verifies that steps are generated for every year in the range.
func TestMultipleYears(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank"}
	cfg.FirstYear = 2022
	cfg.CurrentYear = 2024

	for _, y := range []string{"2022", "2023", "2024"} {
		writeFile(t, filepath.Join(dir, "sources/bank/raw/"+y+"/stmt.csv"), "data\n")
	}

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 3 years × 1 file × 1 stage (convert only, preprocess absent) = 3 steps
	if len(steps) != 3 {
		t.Errorf("expected 3 steps, got %d", len(steps))
	}
}

// TestMultipleInputFiles verifies that each input file in a year gets its own step.
func TestMultipleInputFiles(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank"}
	cfg.FirstYear = 2024
	cfg.CurrentYear = 2024

	writeFile(t, filepath.Join(dir, "sources/bank/raw/2024/jan.csv"), "data\n")
	writeFile(t, filepath.Join(dir, "sources/bank/raw/2024/feb.csv"), "data\n")

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 files × 1 stage = 2 steps
	if len(steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(steps))
	}

	// Each step must have a unique output path.
	outputs := map[string]bool{}
	for _, s := range steps {
		if outputs[s.Output] {
			t.Errorf("duplicate output path %q", s.Output)
		}
		outputs[s.Output] = true
	}
}

// TestEffectivePipelineExtraStages verifies that extra stages are spliced into
// the default pipeline after the named target stage.
func TestEffectivePipelineExtraStages(t *testing.T) {
	cfg := makeConfig(t.TempDir())
	cfg.Sources = map[string]config.SourceOverride{
		"bank": {
			ExtraStages: []config.ExtraStage{
				{
					Name:      "classify",
					Script:    "./classify",
					After:     "preprocess",
					InputDir:  "cleaned",
					InputGlob: "*.csv",
					OutputDir: "cleaned",
					OutputExt: ".ai.rules",
				},
			},
		},
	}

	pipeline := effectivePipeline(cfg, "bank")
	if len(pipeline) != 3 {
		t.Fatalf("expected 3 stages (preprocess, classify, convert), got %d", len(pipeline))
	}
	names := [3]string{pipeline[0].Name, pipeline[1].Name, pipeline[2].Name}
	want := [3]string{"preprocess", "classify", "convert"}
	if names != want {
		t.Errorf("pipeline names = %v, want %v", names, want)
	}
}

// TestEffectivePipelineFullOverride verifies that a per-source full pipeline
// completely replaces the default.
func TestEffectivePipelineFullOverride(t *testing.T) {
	cfg := makeConfig(t.TempDir())
	cfg.Sources = map[string]config.SourceOverride{
		"bank": {
			Pipeline: []config.Pipeline{
				{Name: "custom", Script: "./custom-convert", InputDir: "raw",
					InputGlob: "*.ofx", OutputDir: "journal", OutputExt: ".journal"},
			},
		},
	}

	pipeline := effectivePipeline(cfg, "bank")
	if len(pipeline) != 1 {
		t.Fatalf("expected 1 stage, got %d", len(pipeline))
	}
	if pipeline[0].Name != "custom" {
		t.Errorf("pipeline[0].Name = %q, want custom", pipeline[0].Name)
	}
}

// TestRequiredScriptMissing verifies that a missing non-optional script returns an error.
func TestRequiredScriptMissing(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank"}
	cfg.FirstYear = 2024
	cfg.CurrentYear = 2024
	cfg.Sources = map[string]config.SourceOverride{
		"bank": {
			Pipeline: []config.Pipeline{
				{
					Name:      "custom",
					Script:    "./custom-convert",
					InputDir:  "raw",
					InputGlob: "*.csv",
					OutputDir: "journal",
					OutputExt: ".journal",
					Optional:  false,
				},
			},
		},
	}
	// No custom-convert script in sources/bank/.

	_, err := GeneratePass1Steps(cfg)
	if err == nil {
		t.Fatal("expected error for missing required script, got nil")
	}
}

// TestHledgerArgsOrder verifies the canonical order of hledger arguments.
func TestHledgerArgsOrder(t *testing.T) {
	rules := []string{"sources/global.rules", "sources/bank/bank.rules"}
	args := buildHledgerArgs("sources/bank/raw/2024/stmt.csv", rules)

	// Expected: -f <input> --rules-file r1 --rules-file r2 print
	if len(args) < 6 {
		t.Fatalf("args too short: %v", args)
	}
	if args[0] != "-f" {
		t.Errorf("args[0] = %q, want -f", args[0])
	}
	if args[1] != "sources/bank/raw/2024/stmt.csv" {
		t.Errorf("args[1] = %q, want input path", args[1])
	}
	if args[2] != "--rules-file" {
		t.Errorf("args[2] = %q, want --rules-file", args[2])
	}
	if args[len(args)-1] != "print" {
		t.Errorf("last arg = %q, want print", args[len(args)-1])
	}
}

// TestMultipleSources verifies that steps are generated for all sources
// independently.
func TestMultipleSources(t *testing.T) {
	dir := t.TempDir()
	cfg := makeConfig(dir)
	cfg.DiscoveredSources = []string{"bank-a", "bank-b"}
	cfg.FirstYear = 2024
	cfg.CurrentYear = 2024

	writeFile(t, filepath.Join(dir, "sources/bank-a/raw/2024/stmt.csv"), "data\n")
	writeFile(t, filepath.Join(dir, "sources/bank-b/raw/2024/stmt.csv"), "data\n")

	steps, err := GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 2 sources × 1 file × 1 stage each = 2 steps
	if len(steps) != 2 {
		t.Errorf("expected 2 steps, got %d", len(steps))
	}
	outputs := map[string]bool{}
	for _, s := range steps {
		outputs[s.Output] = true
	}
	if !outputs["sources/bank-a/journal/2024/stmt.journal"] {
		t.Error("missing step for bank-a")
	}
	if !outputs["sources/bank-b/journal/2024/stmt.journal"] {
		t.Error("missing step for bank-b")
	}
}
