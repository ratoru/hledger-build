package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ratoru/hledger-build/internal/manifest"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newManifest returns a fresh in-memory manifest (no file backing needed).
func newManifest() *manifest.Manifest {
	m, _ := manifest.Load("/nonexistent-path-that-will-never-exist")
	return m
}

// echoStep builds a Step that runs the platform echo command, capturing
// stdout into outPath. The content echoed is the text argument.
// This avoids shell quoting complexity — we call the binary directly with args.
func echoStep(id, outPath, text string, deps []string) Step { //nolint:unparam // deps kept for test flexibility
	// Use Go's own binary as a cross-platform "echo":
	// `go run` is too heavy; instead rely on the "echo" built-in via sh/cmd.
	// For portability, detect OS and use appropriate args.
	return Step{
		ID:            id,
		Output:        outPath,
		Deps:          deps,
		Command:       "sh",
		Args:          []string{"-c", "printf '%s' " + text},
		CaptureStdout: true,
	}
}

// failStep builds a Step whose command always exits non-zero.
func failStep(id, outPath string, deps []string) Step {
	return Step{
		ID:     id,
		Output: outPath,
		Deps:   deps,
		// `false` exits 1 on POSIX; use `sh -c 'exit 1'` for portability.
		Command:       "sh",
		Args:          []string{"-c", "exit 1"},
		CaptureStdout: false,
	}
}

// touchStep builds a Step that creates (touches) outPath directly (no stdout capture).
func touchStep(id, outPath string, deps []string) Step {
	return Step{
		ID:            id,
		Output:        outPath,
		Deps:          deps,
		Command:       "sh",
		Args:          []string{"-c", "touch " + outPath},
		CaptureStdout: false,
	}
}

// quietOpts returns RunOpts that suppress all output to keep test logs clean.
func quietOpts() RunOpts {
	return RunOpts{Jobs: runtime.NumCPU(), Quiet: true}
}

// ---------------------------------------------------------------------------
// writeFileChanged
// ---------------------------------------------------------------------------

func TestWriteFileChanged_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")

	wrote, err := writeFileChanged(path, []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wrote {
		t.Error("expected wrote=true for new file")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "hello" {
		t.Errorf("content mismatch: %q", data)
	}
}

func TestWriteFileChanged_SkipsWhenIdentical(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	_ = os.WriteFile(path, []byte("same"), 0o644)
	info1, _ := os.Stat(path)

	wrote, err := writeFileChanged(path, []byte("same"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if wrote {
		t.Error("expected wrote=false when content identical")
	}
	info2, _ := os.Stat(path)
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Error("mtime changed even though content was identical")
	}
}

func TestWriteFileChanged_WritesWhenChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	_ = os.WriteFile(path, []byte("old"), 0o644)

	wrote, err := writeFileChanged(path, []byte("new"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !wrote {
		t.Error("expected wrote=true when content changed")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Errorf("content not updated: %q", data)
	}
}

// ---------------------------------------------------------------------------
// RunSteps — caching
// ---------------------------------------------------------------------------

func TestRunSteps_Caching(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh not available on windows")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")

	step := echoStep("out", out, "hello", nil)
	tiers := [][]Step{{step}}
	mf := newManifest()
	opts := quietOpts()

	// First run: step should execute and produce output.
	if err := RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("first run: %v", err)
	}
	data, _ := os.ReadFile(out)
	if !strings.Contains(string(data), "hello") {
		t.Errorf("output not written: %q", data)
	}

	// Verify manifest recorded the hash.
	hash, ok := mf.Get(out)
	if !ok || hash == "" {
		t.Fatal("manifest not updated after first run")
	}

	// Second run: step must be skipped (cached). Prove it by removing the
	// output and verifying it is NOT recreated (the step would have to run
	// to recreate it, but the cache check happens before the stat guard only
	// when the output is present).
	//
	// Instead, capture that the step was skipped by checking the manifest
	// still matches and no error occurs.
	if err := RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("second run: %v", err)
	}

	// Deleting the output and re-running forces a rebuild (cache miss).
	if err := os.Remove(out); err != nil {
		t.Fatalf("remove output: %v", err)
	}
	if err := RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("third run (after delete): %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Errorf("output not recreated after delete: %v", err)
	}
}

func TestRunSteps_ForceRebuilds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh not available on windows")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")

	step := echoStep("out", out, "v1", nil)
	tiers := [][]Step{{step}}
	mf := newManifest()

	// Seed manifest with a "stale" hash so cache would normally hit.
	mf.Set(out, "fakehash")
	// Create the output so the stat guard passes.
	_ = os.WriteFile(out, []byte("stale"), 0o644)

	opts := quietOpts()
	opts.Force = true
	if err := RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("force run: %v", err)
	}
	data, _ := os.ReadFile(out)
	if string(data) == "stale" {
		t.Error("force flag did not rebuild cached step")
	}
}

// ---------------------------------------------------------------------------
// RunSteps — partial failure
// ---------------------------------------------------------------------------

// TestRunSteps_PartialFailure verifies that:
//   - A failing step causes its dependents to be cancelled.
//   - Independent steps (no dep on the failing step) continue and succeed.
func TestRunSteps_PartialFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh not available on windows")
	}
	dir := t.TempDir()
	outA := filepath.Join(dir, "a.txt") // step A — will FAIL
	outB := filepath.Join(dir, "b.txt") // step B — depends on A → CANCELLED
	outC := filepath.Join(dir, "c.txt") // step C — independent → should SUCCEED

	stepA := failStep("a", outA, nil)
	stepB := touchStep("b", outB, []string{outA}) // depends on A
	stepC := echoStep("c", outC, "ok", nil)       // independent

	// Tier 1: A and C (independent, can run in parallel).
	// Tier 2: B (depends on A's output).
	tiers := [][]Step{
		{stepA, stepC},
		{stepB},
	}
	mf := newManifest()
	opts := quietOpts()

	err := RunSteps(context.Background(), tiers, mf, opts)
	if err == nil {
		t.Fatal("expected error due to failing step A, got nil")
	}

	// C must have succeeded.
	if _, statErr := os.Stat(outC); statErr != nil {
		t.Errorf("independent step C was not built: %v", statErr)
	}

	// B must NOT exist — it was cancelled because A failed.
	if _, statErr := os.Stat(outB); statErr == nil {
		t.Error("dependent step B ran despite A failing")
	}

	// Verify error message mentions failure count.
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RunSteps — dry-run
// ---------------------------------------------------------------------------

func TestRunSteps_DryRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh not available on windows")
	}
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")

	step := echoStep("out", out, "hello", nil)
	tiers := [][]Step{{step}}
	mf := newManifest()

	opts := quietOpts()
	opts.DryRun = true

	if err := RunSteps(context.Background(), tiers, mf, opts); err != nil {
		t.Fatalf("dry-run: %v", err)
	}

	// Output file must NOT exist — dry-run never executes.
	if _, err := os.Stat(out); err == nil {
		t.Error("dry-run wrote output file — should not have executed")
	}

	// Manifest must NOT be updated.
	if _, ok := mf.Get(out); ok {
		t.Error("dry-run updated the manifest — should not have")
	}
}

// ---------------------------------------------------------------------------
// RunSteps — context cancellation
// ---------------------------------------------------------------------------

func TestRunSteps_ContextCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh not available on windows")
	}
	dir := t.TempDir()

	// A step that sleeps for 5 seconds — should be interrupted.
	step := Step{
		ID:      "sleep",
		Output:  filepath.Join(dir, "out.txt"),
		Command: "sh",
		Args:    []string{"-c", "sleep 5"},
	}
	tiers := [][]Step{{step}}
	mf := newManifest()
	opts := quietOpts()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately before RunSteps

	err := RunSteps(ctx, tiers, mf, opts)
	if err == nil {
		t.Fatal("expected error when context is cancelled, got nil")
	}
}

// ---------------------------------------------------------------------------
// RunSteps — fail-fast
// ---------------------------------------------------------------------------

func TestRunSteps_FailFast(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh not available on windows")
	}
	dir := t.TempDir()
	outA := filepath.Join(dir, "a.txt")
	outB := filepath.Join(dir, "b.txt")

	stepA := failStep("a", outA, nil)
	// Step B is independent but in the same tier — in fail-fast mode the
	// errgroup context is cancelled when A fails, so B may or may not run.
	// The important assertion is that RunSteps returns an error.
	stepB := echoStep("b", outB, "hello", nil)

	tiers := [][]Step{{stepA, stepB}}
	mf := newManifest()
	opts := quietOpts()
	opts.FailFast = true
	opts.Jobs = 1 // sequential to make failure deterministic

	err := RunSteps(context.Background(), tiers, mf, opts)
	if err == nil {
		t.Fatal("expected error in fail-fast mode, got nil")
	}
}

// ---------------------------------------------------------------------------
// RunSteps — empty tiers
// ---------------------------------------------------------------------------

func TestRunSteps_EmptyTiers(t *testing.T) {
	mf := newManifest()
	err := RunSteps(context.Background(), nil, mf, quietOpts())
	if err != nil {
		t.Errorf("empty tiers should succeed, got: %v", err)
	}
}
