package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ComputeHash returns a deterministic SHA256 hex string that captures everything
// that affects a step's output: the command, cwd, arguments, and the content
// of every dependency file.
//
// Hash input format (fed line-by-line into SHA256):
//
//	cmd:<command>
//	cwd:<cwd>
//	arg:<arg1>
//	arg:<arg2>
//	...
//	dep:<sortedPath1>:<SHA256(contents1)>
//	dep:<sortedPath2>:<SHA256(contents2)>
//	...
//
// Dependencies are sorted alphabetically for determinism. The script binary
// (Command) is included as an implicit dependency when it is a local file path
// (starts with "./" or "../"). System binaries looked up via PATH are not hashed.
// Environment variables are intentionally excluded (v1 simplification).
//
// If a dependency file is missing (it may be generated later), a fixed zero-hash
// sentinel is used so the step is never incorrectly cached.
//
// All file paths are relative to the project root; callers are expected to run
// from the project root (or pass root-relative paths that resolve correctly).
func ComputeHash(step Step) (string, error) {
	h := sha256.New()

	_, _ = fmt.Fprintf(h, "cmd:%s\n", step.Command)
	_, _ = fmt.Fprintf(h, "cwd:%s\n", step.Cwd)
	for _, arg := range step.Args {
		_, _ = fmt.Fprintf(h, "arg:%s\n", arg)
	}

	// Collect all dep paths, starting with the explicit ones.
	deps := make([]string, 0, len(step.Deps)+1)
	deps = append(deps, step.Deps...)

	// Include the script binary as an implicit dep when it is a local file.
	if isLocalFilePath(step.Command) {
		scriptPath := resolveLocalCommand(step.Command, step.Cwd)
		deps = append(deps, scriptPath)
	}

	// Deduplicate and sort for determinism.
	deps = uniqueSorted(deps)

	// Hash each dep's contents and append to the outer hash.
	for _, dep := range deps {
		contentHash, err := hashFile(dep)
		if err != nil {
			return "", fmt.Errorf("hashing dep %q: %w", dep, err)
		}
		_, _ = fmt.Fprintf(h, "dep:%s:%s\n", dep, contentHash)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// isLocalFilePath reports whether cmd is a relative path to a local file
// (starts with "./" or "../" or their Windows equivalents) rather than a bare
// binary name found via PATH.
func isLocalFilePath(cmd string) bool {
	return strings.HasPrefix(cmd, "./") || strings.HasPrefix(cmd, "../") ||
		strings.HasPrefix(cmd, `.\\`) || strings.HasPrefix(cmd, `..\\`)
}

// resolveLocalCommand resolves a local-file Command to a path suitable for
// opening. If cwd is non-empty the command is joined with cwd (since it is
// relative to the working directory, not the project root). The returned path
// is cleaned with [filepath.Clean].
func resolveLocalCommand(cmd, cwd string) string {
	if cwd != "" {
		return filepath.Clean(filepath.Join(cwd, cmd))
	}
	return filepath.Clean(cmd)
}

// hashFile returns the lowercase hex SHA256 of a file's contents.
// If the file does not exist, a fixed 64-zero sentinel is returned so that
// missing deps do not cause ComputeHash to error out — the build step will
// fail naturally when it executes.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return strings.Repeat("0", 64), nil
		}
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("reading %q: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// uniqueSorted returns a new deduplicated and sorted copy of ss.
// The original slice is not modified.
func uniqueSorted(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
