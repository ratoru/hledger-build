// Package integration_test exercises the full hledger-build pipeline
// end-to-end against a small fixture finance repository (2 sources × 2 years).
package integration_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ratoru/hledger-build/internal/config"
	"github.com/ratoru/hledger-build/internal/manifest"
	"github.com/ratoru/hledger-build/internal/passes"
	"github.com/ratoru/hledger-build/internal/runner"
)

// ── Fixture helpers ───────────────────────────────────────────────────────────

// mustMkdir creates a directory tree (equivalent to mkdir -p).
func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdirAll %q: %v", path, err)
	}
}

// mustWrite creates parent directories and writes data to path.
func mustWrite(t *testing.T, path, data string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatalf("writeFile %q: %v", path, err)
	}
}

// mustWriteExec creates an executable script file.
func mustWriteExec(t *testing.T, path, data string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(data), 0o755); err != nil {
		t.Fatalf("writeExec %q: %v", path, err)
	}
}

// setupFixture creates a 2-source × 2-year project in dir:
//
//	sources/bank1/checking/  — has preprocess script (pass-through cat)
//	  raw/2023/jan.csv
//	  raw/2024/jan.csv
//	  preprocess             (optional, present → step runs)
//	  convert                (required, present)
//
//	sources/bank2/savings/   — no preprocess (optional, absent → skipped)
//	  raw/2023/jan.csv
//	  raw/2024/jan.csv
//	  convert                (required, present)
//
// Both "convert" scripts echo the input path and then cat the input file so
// that changing the raw CSV propagates a visible content change to the journal.
func setupFixture(t *testing.T, dir string) {
	t.Helper()

	// bank1/checking — preprocess + convert
	mustWrite(t, filepath.Join(dir, "sources/bank1/checking/raw/2023/jan.csv"),
		"date,description,amount\n2023-01-01,salary,1000\n")
	mustWrite(t, filepath.Join(dir, "sources/bank1/checking/raw/2024/jan.csv"),
		"date,description,amount\n2024-01-01,salary,1100\n")
	mustWriteExec(t, filepath.Join(dir, "sources/bank1/checking/preprocess"),
		"#!/bin/sh\ncat \"$1\"\n")
	mustWriteExec(t, filepath.Join(dir, "sources/bank1/checking/convert"),
		"#!/bin/sh\nprintf '; from %s\\n' \"$1\"; cat \"$1\"\n")

	// bank2/savings — convert only (preprocess absent → optional stage skipped)
	mustWrite(t, filepath.Join(dir, "sources/bank2/savings/raw/2023/jan.csv"),
		"date,description,amount\n2023-01-15,interest,5\n")
	mustWrite(t, filepath.Join(dir, "sources/bank2/savings/raw/2024/jan.csv"),
		"date,description,amount\n2024-01-15,interest,6\n")
	mustWriteExec(t, filepath.Join(dir, "sources/bank2/savings/convert"),
		"#!/bin/sh\nprintf '; from %s\\n' \"$1\"; cat \"$1\"\n")
}

// fixtureConfig returns a Config wired to dir that uses local shell scripts
// instead of hledger, making the integration test self-contained.
func fixtureConfig(dir string) *config.Config {
	return &config.Config{
		ProjectRoot:       dir,
		HledgerBinary:     "hledger", // not used: pipeline overridden to local scripts
		FirstYear:         2023,
		CurrentYear:       2024,
		DiscoveredSources: []string{"bank1/checking", "bank2/savings"},
		Directories: config.Directories{
			Sources: "sources",
			Raw:     "raw",
			Cleaned: "cleaned",
			Journal: "journal",
			Build:   ".build",
			Prices:  "sources/prices",
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
				Script:    "./convert",
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

// stepOutputPaths extracts Output paths for diagnostic messages.
func stepOutputPaths(steps []runner.Step) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Output
	}
	return out
}

// ── Integration test ──────────────────────────────────────────────────────────

// TestIntegration_FullPipelineEndToEnd exercises the complete Pass 1 pipeline
// (step generation → topological sort → parallel execution) against a small
// fixture repo (2 sources × 2 years).
//
// Three sequential runs are performed:
//
//  1. First run  — all 6 steps execute and produce output files.
//  2. Second run — all steps are cached (content unchanged).
//  3. Third run  — one raw file is modified; only its two dependents rebuild
//     (preprocess + convert for bank1/checking/2023); the remaining four steps
//     stay cached.
func TestIntegration_Pass1(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("integration test requires POSIX sh")
	}

	dir := t.TempDir()
	setupFixture(t, dir)

	// Change working directory to the project root so that project-root-relative
	// paths inside generated Steps resolve correctly during execution.
	// t.Chdir registers a cleanup that restores the original directory.
	t.Chdir(dir)

	cfg := fixtureConfig(dir)

	// ── Step generation ───────────────────────────────────────────────────────

	steps, err := passes.GeneratePass1Steps(cfg)
	if err != nil {
		t.Fatalf("GeneratePass1Steps: %v", err)
	}

	// Expected step count:
	//   bank1/checking: preprocess(2023) + preprocess(2024) + convert(2023) + convert(2024) = 4
	//   bank2/savings:  convert(2023) + convert(2024) = 2  (preprocess absent → skipped)
	//   Total: 6
	if len(steps) != 6 {
		t.Fatalf("expected 6 steps, got %d: %v", len(steps), stepOutputPaths(steps))
	}

	tiers, err := runner.TopoSort(steps)
	if err != nil {
		t.Fatalf("TopoSort: %v", err)
	}

	// Tier 0: bank1 preprocess×2 + bank2 convert×2 (no step-produced deps).
	// Tier 1: bank1 convert×2 (depend on cleaned CSVs from tier 0).
	if len(tiers) != 2 {
		t.Errorf("expected 2 tiers, got %d", len(tiers))
	}
	if len(tiers) >= 1 && len(tiers[0]) != 4 {
		t.Errorf("tier 0: expected 4 steps, got %d: %v", len(tiers[0]), stepOutputPaths(tiers[0]))
	}
	if len(tiers) >= 2 && len(tiers[1]) != 2 {
		t.Errorf("tier 1: expected 2 steps, got %d: %v", len(tiers[1]), stepOutputPaths(tiers[1]))
	}

	mf, err := manifest.Load(".build/manifest.json")
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	opts := runner.RunOpts{Jobs: 4, Quiet: true}

	wantOutputs := []string{
		"sources/bank1/checking/cleaned/2023/jan.csv",
		"sources/bank1/checking/cleaned/2024/jan.csv",
		"sources/bank1/checking/journal/2023/jan.journal",
		"sources/bank1/checking/journal/2024/jan.journal",
		"sources/bank2/savings/journal/2023/jan.journal",
		"sources/bank2/savings/journal/2024/jan.journal",
	}

	// ── First run: every step must execute and produce an output file ─────────

	if err := runner.RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("first run: %v", err)
	}

	for _, out := range wantOutputs {
		if _, err := os.Stat(out); err != nil {
			t.Errorf("first run: expected output missing %q: %v", out, err)
		}
	}

	// Record mtimes so the second run can verify files were not touched.
	mtimeAfter1 := make(map[string]time.Time, len(wantOutputs))
	for _, out := range wantOutputs {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("stat after first run %q: %v", out, err)
		}
		mtimeAfter1[out] = info.ModTime()
	}

	// Sleep long enough for the filesystem clock to advance so that any
	// re-write would produce a strictly later mtime.
	time.Sleep(50 * time.Millisecond)

	// ── Second run: all steps must be cached (files must not be touched) ──────

	if err := runner.RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("second run: %v", err)
	}

	for _, out := range wantOutputs {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("stat after second run %q: %v", out, err)
		}
		if !info.ModTime().Equal(mtimeAfter1[out]) {
			t.Errorf("second run: file was re-written (not cached): %q", out)
		}
	}

	// ── Third run: modify one raw file; verify selective rebuild ──────────────

	// Overwrite bank1/checking/raw/2023/jan.csv with a new amount (9999).
	newCSV := "date,description,amount\n2023-01-01,salary,9999\n"
	if err := os.WriteFile("sources/bank1/checking/raw/2023/jan.csv", []byte(newCSV), 0o644); err != nil {
		t.Fatalf("overwrite raw file: %v", err)
	}

	// Record mtimes of the unaffected outputs before the third run.
	unaffected := []string{
		"sources/bank1/checking/cleaned/2024/jan.csv",
		"sources/bank1/checking/journal/2024/jan.journal",
		"sources/bank2/savings/journal/2023/jan.journal",
		"sources/bank2/savings/journal/2024/jan.journal",
	}
	mtimeBeforeRun3 := make(map[string]time.Time, len(unaffected))
	for _, out := range unaffected {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("stat before third run %q: %v", out, err)
		}
		mtimeBeforeRun3[out] = info.ModTime()
	}

	time.Sleep(50 * time.Millisecond)

	if err := runner.RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("third run: %v", err)
	}

	// The full dependency chain for bank1/checking/2023 must have rebuilt:
	//   preprocess → sources/bank1/checking/cleaned/2023/jan.csv (new content)
	//   convert    → sources/bank1/checking/journal/2023/jan.journal (contains 9999)
	rebuilt := "sources/bank1/checking/journal/2023/jan.journal"
	data, err := os.ReadFile(rebuilt)
	if err != nil {
		t.Fatalf("read rebuilt journal: %v", err)
	}
	if !strings.Contains(string(data), "9999") {
		t.Errorf("rebuilt journal should contain new amount 9999, got:\n%s", string(data))
	}

	// Every other output must not have been touched (file not re-written).
	for _, out := range unaffected {
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("stat after third run %q: %v", out, err)
		}
		if !info.ModTime().Equal(mtimeBeforeRun3[out]) {
			t.Errorf("third run: unaffected output was re-written: %q", out)
		}
	}
}
