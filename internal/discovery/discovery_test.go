package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile creates a file at path with the given content, creating parent
// directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGetIncludes_NoIncludes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "2024.journal"), "2024-01-01 Opening\n  assets:checking  $100\n")

	got, err := GetIncludes(dir, "2024.journal")
	if err != nil {
		t.Fatal(err)
	}
	assertSliceEqual(t, got, []string{"2024.journal"})
}

func TestGetIncludes_SingleInclude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "2024.journal"), "include sources/lloyds/journal/2024/stmt.journal\n")
	writeFile(t, filepath.Join(dir, "sources/lloyds/journal/2024/stmt.journal"), "")

	got, err := GetIncludes(dir, "2024.journal")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"2024.journal",
		filepath.Join("sources", "lloyds", "journal", "2024", "stmt.journal"),
	}
	assertSliceEqual(t, got, want)
}

func TestGetIncludes_BangInclude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.journal"), "!include sub.journal\n")
	writeFile(t, filepath.Join(dir, "sub.journal"), "")

	got, err := GetIncludes(dir, "main.journal")
	if err != nil {
		t.Fatal(err)
	}
	assertSliceEqual(t, got, []string{"main.journal", "sub.journal"})
}

func TestGetIncludes_RecursiveIncludes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.journal"), "include b.journal\n")
	writeFile(t, filepath.Join(dir, "b.journal"), "include c.journal\n")
	writeFile(t, filepath.Join(dir, "c.journal"), "")

	got, err := GetIncludes(dir, "a.journal")
	if err != nil {
		t.Fatal(err)
	}
	assertSliceEqual(t, got, []string{"a.journal", "b.journal", "c.journal"})
}

func TestGetIncludes_CycleDetection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.journal"), "include b.journal\n")
	writeFile(t, filepath.Join(dir, "b.journal"), "include a.journal\n")

	got, err := GetIncludes(dir, "a.journal")
	if err != nil {
		t.Fatal(err)
	}
	// Each file appears exactly once despite the cycle.
	assertSliceEqual(t, got, []string{"a.journal", "b.journal"})
}

func TestGetIncludes_MissingIncludedFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.journal"), "include missing.journal\n")

	got, err := GetIncludes(dir, "main.journal")
	if err != nil {
		t.Fatal(err)
	}
	// Missing included file is warned about but not an error.
	assertSliceEqual(t, got, []string{"main.journal"})
}

func TestGetIncludes_MissingStartFile(t *testing.T) {
	dir := t.TempDir()

	got, err := GetIncludes(dir, "nonexistent.journal")
	if err != nil {
		t.Fatal(err)
	}
	// Missing start file: empty result.
	if len(got) != 0 {
		t.Errorf("got %v, want []", got)
	}
}

func TestGetIncludes_PathNormalisation(t *testing.T) {
	dir := t.TempDir()
	// sub/a.journal uses "../" to refer to a file in the project root.
	writeFile(t, filepath.Join(dir, "main.journal"), "include sub/a.journal\n")
	writeFile(t, filepath.Join(dir, "sub/a.journal"), "include ../b.journal\n")
	writeFile(t, filepath.Join(dir, "b.journal"), "")

	got, err := GetIncludes(dir, "main.journal")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"main.journal",
		filepath.Join("sub", "a.journal"),
		"b.journal",
	}
	assertSliceEqual(t, got, want)
}

func TestGetIncludes_DiamondDependency(t *testing.T) {
	dir := t.TempDir()
	// a → b, a → c, b → d, c → d  (diamond — d must appear exactly once)
	writeFile(t, filepath.Join(dir, "a.journal"), "include b.journal\ninclude c.journal\n")
	writeFile(t, filepath.Join(dir, "b.journal"), "include d.journal\n")
	writeFile(t, filepath.Join(dir, "c.journal"), "include d.journal\n")
	writeFile(t, filepath.Join(dir, "d.journal"), "")

	got, err := GetIncludes(dir, "a.journal")
	if err != nil {
		t.Fatal(err)
	}
	// DFS order: a, b, d, c (d already visited when c tries to include it).
	want := []string{"a.journal", "b.journal", "d.journal", "c.journal"}
	assertSliceEqual(t, got, want)
}

func TestGetIncludes_AbsoluteFilePath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.journal"), "include sub.journal\n")
	writeFile(t, filepath.Join(dir, "sub.journal"), "")

	// Pass an absolute path as filePath.
	got, err := GetIncludes(dir, filepath.Join(dir, "main.journal"))
	if err != nil {
		t.Fatal(err)
	}
	assertSliceEqual(t, got, []string{"main.journal", "sub.journal"})
}

func TestGetIncludes_GlobPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "main.journal"), "include sources/bank/journal/2026/*.journal\n")
	writeFile(t, filepath.Join(dir, "sources/bank/journal/2026/stmt.journal"), "")
	writeFile(t, filepath.Join(dir, "sources/bank/journal/2026/extra.journal"), "")

	got, err := GetIncludes(dir, "main.journal")
	if err != nil {
		t.Fatal(err)
	}
	// main.journal + both glob matches (sorted by filepath.Glob, which is lexical).
	want := []string{
		"main.journal",
		filepath.Join("sources", "bank", "journal", "2026", "extra.journal"),
		filepath.Join("sources", "bank", "journal", "2026", "stmt.journal"),
	}
	assertSliceEqual(t, got, want)
}

func TestGetIncludes_GlobNoMatches(t *testing.T) {
	dir := t.TempDir()
	// Glob that matches nothing — should not warn and should not error.
	writeFile(t, filepath.Join(dir, "main.journal"), "include sources/bank/journal/2026/*.journal\n")

	got, err := GetIncludes(dir, "main.journal")
	if err != nil {
		t.Fatal(err)
	}
	// Only main.journal itself; no warning, no error.
	assertSliceEqual(t, got, []string{"main.journal"})
}

// assertSliceEqual fails if got and want differ element-by-element.
func assertSliceEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("length mismatch: got %d %v, want %d %v", len(got), got, len(want), want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("element[%d]: got %q, want %q\n  full got:  %v\n  full want: %v", i, got[i], want[i], got, want)
		}
	}
}
