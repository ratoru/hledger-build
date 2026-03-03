package runner

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTemp creates a temporary file with the given contents and returns its path.
func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeTemp: %v", err)
	}
	return path
}

// TestComputeHash_Determinism verifies that identical steps always produce the
// same hash, and different steps produce different hashes.
func TestComputeHash_Determinism(t *testing.T) {
	dir := t.TempDir()
	dep := writeTemp(t, dir, "input.csv", "a,b,c\n1,2,3\n")

	step := Step{
		ID:      "out",
		Output:  filepath.Join(dir, "out.journal"),
		Deps:    []string{dep},
		Command: "hledger",
		Args:    []string{"-f", dep, "print"},
		Cwd:     dir,
	}

	h1, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}
	h2, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if h1 != h2 {
		t.Errorf("hash is not deterministic: %q != %q", h1, h2)
	}

	// Changing an argument must change the hash.
	step2 := step
	step2.Args = append([]string(nil), step.Args...)
	step2.Args[0] = "-f2"
	h3, err := ComputeHash(step2)
	if err != nil {
		t.Fatalf("third hash: %v", err)
	}
	if h1 == h3 {
		t.Errorf("changed arg did not change hash")
	}
}

// TestComputeHash_DepOrdering verifies that deps are sorted alphabetically so
// the hash is the same regardless of the order they are listed in the Step.
func TestComputeHash_DepOrdering(t *testing.T) {
	dir := t.TempDir()
	a := writeTemp(t, dir, "a.csv", "aaa")
	b := writeTemp(t, dir, "b.csv", "bbb")

	base := Step{
		ID:      "out",
		Output:  filepath.Join(dir, "out.journal"),
		Command: "hledger",
		Cwd:     dir,
	}

	s1 := base
	s1.Deps = []string{a, b}
	s2 := base
	s2.Deps = []string{b, a} // reversed order

	h1, err := ComputeHash(s1)
	if err != nil {
		t.Fatalf("s1 hash: %v", err)
	}
	h2, err := ComputeHash(s2)
	if err != nil {
		t.Fatalf("s2 hash: %v", err)
	}
	if h1 != h2 {
		t.Errorf("dep ordering changed hash: %q != %q", h1, h2)
	}
}

// TestComputeHash_DepContentChange verifies that changing a dependency file's
// contents changes the hash.
func TestComputeHash_DepContentChange(t *testing.T) {
	dir := t.TempDir()
	dep := writeTemp(t, dir, "input.csv", "original content")

	step := Step{
		ID:      "out",
		Output:  filepath.Join(dir, "out.journal"),
		Deps:    []string{dep},
		Command: "hledger",
		Args:    []string{"-f", dep, "print"},
		Cwd:     dir,
	}

	h1, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}

	// Overwrite the dep with different content.
	if err := os.WriteFile(dep, []byte("changed content"), 0o644); err != nil {
		t.Fatalf("overwrite dep: %v", err)
	}

	h2, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if h1 == h2 {
		t.Errorf("dep content change did not change hash")
	}
}

// TestComputeHash_ScriptImplicitDep verifies that a local script (Command
// starting with "./") is hashed as an implicit dependency — changing the
// script changes the hash even when deps are identical.
func TestComputeHash_ScriptImplicitDep(t *testing.T) {
	dir := t.TempDir()
	dep := writeTemp(t, dir, "input.csv", "data")
	// Create the script inside dir so that resolveLocalCommand finds it.
	script := writeTemp(t, dir, "preprocess", "#!/bin/sh\necho v1\n")

	step := Step{
		ID:      "out",
		Output:  filepath.Join(dir, "out.csv"),
		Deps:    []string{dep},
		Command: "./preprocess",
		Args:    []string{dep},
		Cwd:     dir,
	}

	h1, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("first hash: %v", err)
	}

	// Modify the script.
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho v2\n"), 0o755); err != nil {
		t.Fatalf("update script: %v", err)
	}

	h2, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if h1 == h2 {
		t.Errorf("script change did not change hash")
	}
}

// TestComputeHash_SystemBinaryNotHashed verifies that a system binary
// (Command without "./" prefix) is NOT added as an implicit dep — it is still
// part of the hash via the "cmd:" line, but its file contents are not read.
func TestComputeHash_SystemBinaryNotHashed(t *testing.T) {
	// If hledger is treated as a file dep, hashFile would be called for "hledger"
	// and would either fail (if missing) or read /usr/bin/hledger. This test
	// only verifies that ComputeHash completes without error for a system binary.
	dir := t.TempDir()
	dep := writeTemp(t, dir, "input.csv", "data")

	step := Step{
		ID:      "out",
		Output:  filepath.Join(dir, "out.journal"),
		Deps:    []string{dep},
		Command: "hledger", // bare binary name — not a local file
		Args:    []string{"-f", dep, "print"},
		Cwd:     dir,
	}

	if _, err := ComputeHash(step); err != nil {
		t.Fatalf("ComputeHash with system binary: %v", err)
	}
}

// TestComputeHash_MissingDep verifies that a missing dependency does not cause
// an error — it gets a zero-hash sentinel so the step is always treated as stale.
func TestComputeHash_MissingDep(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "does_not_exist.csv")

	step := Step{
		ID:      "out",
		Output:  filepath.Join(dir, "out.journal"),
		Deps:    []string{missing},
		Command: "hledger",
		Cwd:     dir,
	}

	h, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("expected no error for missing dep, got: %v", err)
	}
	if h == "" {
		t.Errorf("expected non-empty hash even with missing dep")
	}

	// A second call with the same missing dep must produce the same hash
	// (sentinel is stable).
	h2, err := ComputeHash(step)
	if err != nil {
		t.Fatalf("second hash: %v", err)
	}
	if h != h2 {
		t.Errorf("missing dep hash not deterministic: %q != %q", h, h2)
	}
}

// TestComputeHash_CwdAffectsHash verifies that changing the Cwd changes the hash.
func TestComputeHash_CwdAffectsHash(t *testing.T) {
	dir := t.TempDir()
	dep := writeTemp(t, dir, "input.csv", "data")

	base := Step{
		ID:      "out",
		Output:  filepath.Join(dir, "out.journal"),
		Deps:    []string{dep},
		Command: "hledger",
		Args:    []string{"-f", dep, "print"},
	}

	s1 := base
	s1.Cwd = "sources/lloyds"
	s2 := base
	s2.Cwd = "sources/chase"

	h1, err := ComputeHash(s1)
	if err != nil {
		t.Fatalf("h1: %v", err)
	}
	h2, err := ComputeHash(s2)
	if err != nil {
		t.Fatalf("h2: %v", err)
	}
	if h1 == h2 {
		t.Errorf("different Cwd produced the same hash")
	}
}

// TestComputeHash_DupDepsDeduped verifies that listing the same dep twice does
// not change the hash (deduplication ensures only one "dep:" line per path).
func TestComputeHash_DupDepsDeduped(t *testing.T) {
	dir := t.TempDir()
	dep := writeTemp(t, dir, "input.csv", "data")

	s1 := Step{
		ID:      "out",
		Deps:    []string{dep},
		Command: "hledger",
		Cwd:     dir,
	}
	s2 := Step{
		ID:      "out",
		Deps:    []string{dep, dep}, // duplicated
		Command: "hledger",
		Cwd:     dir,
	}

	h1, _ := ComputeHash(s1)
	h2, _ := ComputeHash(s2)
	if h1 != h2 {
		t.Errorf("duplicate dep changed hash: %q != %q", h1, h2)
	}
}
