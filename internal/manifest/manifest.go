// Package manifest provides a thread-safe content-hash cache stored as JSON.
// The manifest records the hash of each successfully-built target so that
// unchanged targets can be skipped on subsequent runs.
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const currentVersion = 1

// Manifest holds the persisted build cache.
type Manifest struct {
	Version int               `json:"version"`
	Targets map[string]string `json:"targets"`

	mu sync.RWMutex
}

// Load reads the manifest from path. If the file does not exist, or if the
// stored version does not match the current tool version, a fresh empty
// manifest is returned (no error).
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return empty(), nil
		}
		return nil, fmt.Errorf("reading manifest %q: %w", path, err)
	}

	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		// Corrupt file — start fresh.
		return empty(), nil
	}
	if m.Version != currentVersion {
		return empty(), nil
	}
	if m.Targets == nil {
		m.Targets = make(map[string]string)
	}
	return &m, nil
}

// Get returns the stored hash for target and whether it was present.
func (m *Manifest) Get(target string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	h, ok := m.Targets[target]
	return h, ok
}

// Set stores hash for target. It is safe to call concurrently.
func (m *Manifest) Set(target, hash string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Targets[target] = hash
}

// Save writes the manifest to path atomically (write to a temp file, then
// rename) so that a partial write never corrupts the cache.
func (m *Manifest) Save(path string) error {
	m.mu.RLock()
	data, err := json.MarshalIndent(&struct {
		Version int               `json:"version"`
		Targets map[string]string `json:"targets"`
	}{
		Version: currentVersion,
		Targets: m.Targets,
	}, "", "  ")
	m.mu.RUnlock()
	if err != nil {
		return fmt.Errorf("marshalling manifest: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating manifest directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file for manifest: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("writing manifest temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("closing manifest temp file: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("renaming manifest temp file to %q: %w", path, err)
	}
	return nil
}

func empty() *Manifest {
	return &Manifest{
		Version: currentVersion,
		Targets: make(map[string]string),
	}
}
