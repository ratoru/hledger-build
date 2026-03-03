package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestLoadMissingFile verifies that a missing manifest returns an empty one.
func TestLoadMissingFile(t *testing.T) {
	m, err := Load(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Version != currentVersion {
		t.Errorf("version = %d; want %d", m.Version, currentVersion)
	}
	if len(m.Targets) != 0 {
		t.Errorf("targets = %v; want empty", m.Targets)
	}
}

// TestRoundtrip verifies that a saved manifest can be loaded back intact.
func TestRoundtrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	m := empty()
	m.Set("reports/2024-balance-sheet.txt", "aabbcc")
	m.Set("sources/lloyds/journal/2024/stmt1.journal", "ddeeff")

	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for target, wantHash := range map[string]string{
		"reports/2024-balance-sheet.txt":            "aabbcc",
		"sources/lloyds/journal/2024/stmt1.journal": "ddeeff",
	} {
		if got, ok := loaded.Get(target); !ok || got != wantHash {
			t.Errorf("Get(%q) = %q, %v; want %q, true", target, got, ok, wantHash)
		}
	}
}

// TestVersionMismatch verifies that a stale manifest (wrong version) is discarded.
func TestVersionMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")

	stale := map[string]any{
		"version": 999,
		"targets": map[string]string{"some/file.txt": "hash"},
	}
	data, _ := json.Marshal(stale)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Targets) != 0 {
		t.Errorf("expected empty targets after version mismatch, got %v", m.Targets)
	}
}

// TestCorruptFile verifies that a corrupt manifest file returns an empty manifest.
func TestCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(path, []byte("not json {{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(m.Targets) != 0 {
		t.Errorf("expected empty manifest after corrupt file, got %v", m.Targets)
	}
}

// TestGetMissing verifies Get returns false for unknown targets.
func TestGetMissing(t *testing.T) {
	m := empty()
	if _, ok := m.Get("nonexistent"); ok {
		t.Error("expected ok=false for missing target")
	}
}

// TestConcurrency verifies that concurrent Set calls do not race.
func TestConcurrency(t *testing.T) {
	m := empty()
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		go func(n int) {
			defer wg.Done()
			key := filepath.Join("reports", string(rune('a'+n%26))+".txt")
			m.Set(key, "hash")
			m.Get(key)
		}(i)
	}
	wg.Wait()
}

// TestAtomicSave verifies that Save creates the directory if needed and the
// file is valid JSON after a save.
func TestAtomicSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "subdir", "manifest.json")

	m := empty()
	m.Set("a.txt", "hash1")

	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("saved file is not valid JSON: %v", err)
	}
}
