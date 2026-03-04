// Package runner implements the parallel build execution engine.
// It processes topologically sorted tiers of Steps, skipping cached targets,
// running steps in parallel within each tier, and propagating cancellation to
// transitive dependents when a step fails.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/ratoru/hledger-build/internal/manifest"
)

// RunOpts configures the behaviour of RunSteps.
type RunOpts struct {
	Jobs     int  // 0 → runtime.NumCPU()
	Force    bool // Ignore cached hashes; rebuild everything.
	DryRun   bool // Print what would run; don't execute anything.
	Verbose  bool // Print subprocess stdout/stderr inline.
	Quiet    bool // Suppress all output except errors.
	FailFast bool // Cancel all work on first failure.
}

// HandleSignals starts a goroutine that cancels ctx and saves the manifest to
// manifestPath on SIGINT or SIGTERM. The goroutine exits cleanly when ctx is
// done (i.e. when RunSteps returns normally).
//
// Typical usage:
//
//	ctx, cancel := context.WithCancel(context.Background())
//	defer cancel()
//	runner.HandleSignals(ctx, cancel, mf, ".build/manifest.json")
//	err := runner.RunSteps(ctx, tiers, mf, opts)
func HandleSignals(ctx context.Context, cancel context.CancelFunc, mf *manifest.Manifest, manifestPath string) {
	ch := make(chan os.Signal, 1)
	notifySignals(ch)
	go func() {
		defer signal.Stop(ch)
		select {
		case <-ch:
			fmt.Fprintln(os.Stderr, "\ninterrupted — saving manifest")
			cancel()
			if err := mf.Save(manifestPath); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not save manifest: %v\n", err)
			}
		case <-ctx.Done():
		}
	}()
}

// RunSteps executes all steps across the given tiers in dependency order.
// Steps within each tier are independent and run in parallel (up to opts.Jobs
// goroutines). When a step fails its transitive dependents (in later tiers)
// are cancelled; independent siblings continue. Returns an error if any step
// failed or if ctx was cancelled.
func RunSteps(ctx context.Context, tiers [][]Step, mf *manifest.Manifest, opts RunOpts) error {
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}

	// cancelledOutputs records every output path that failed or was cancelled.
	// Later-tier steps whose Deps intersect this set are skipped.
	cancelledOutputs := make(map[string]bool)
	totalFailed := 0

	for _, tier := range tiers {
		if ctx.Err() != nil {
			break
		}

		// Pre-classify steps for this tier before launching any goroutines.
		// Because all deps come from earlier tiers (by topological invariant),
		// cancelledOutputs is fully populated before we inspect this tier.
		var toRun []Step
		for _, step := range tier {
			if hasCancelledDep(step.Deps, cancelledOutputs) {
				cancelledOutputs[step.Output] = true
				if !opts.Quiet {
					fmt.Printf("— %s\n", step.Output)
				}
			} else {
				toRun = append(toRun, step)
			}
		}

		if len(toRun) == 0 {
			continue
		}

		// errgroup context is cancelled when any goroutine returns an error,
		// which only happens in fail-fast mode (see below).
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(jobs)

		var mu sync.Mutex
		var tierFailed []string

		for _, step := range toRun {
			// In fail-fast mode, use the errgroup-derived context so that a
			// failing step cancels all sibling goroutines via context. In normal
			// mode, use the parent context so siblings are unaffected by failure.
			execCtx := ctx
			if opts.FailFast {
				execCtx = gctx
			}

			g.Go(func() error {
				if err := executeStep(execCtx, step, mf, opts); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					mu.Lock()
					tierFailed = append(tierFailed, step.Output)
					mu.Unlock()
					if opts.FailFast {
						return err // triggers gctx cancellation
					}
				}
				return nil // never error in non-fail-fast mode
			})
		}

		// Always drain the group, even on fail-fast.
		_ = g.Wait()

		for _, o := range tierFailed {
			cancelledOutputs[o] = true
		}
		totalFailed += len(tierFailed)
	}

	if ctx.Err() != nil {
		return fmt.Errorf("build interrupted: %w", ctx.Err())
	}
	if totalFailed > 0 {
		return fmt.Errorf("%d step(s) failed", totalFailed)
	}
	return nil
}

// executeStep runs a single step, skipping it when the cached hash matches.
func executeStep(ctx context.Context, step Step, mf *manifest.Manifest, opts RunOpts) error {
	hash, err := ComputeHash(step)
	if err != nil {
		return fmt.Errorf("%s: compute hash: %w", step.ID, err)
	}

	// Cache check: skip if hash matches AND output file exists on disk.
	if !opts.Force {
		if cached, ok := mf.Get(step.Output); ok && cached == hash {
			if _, statErr := os.Stat(step.Output); statErr == nil {
				if !opts.Quiet {
					fmt.Printf("⏭ %s\n", step.Output)
				}
				return nil
			}
		}
	}

	if opts.DryRun {
		if !opts.Quiet {
			fmt.Printf("+ %s (dry-run)\n", step.Output)
		}
		return nil
	}

	// Build and run the subprocess.
	cmd := exec.CommandContext(ctx, step.Command, step.Args...)
	if step.Cwd != "" {
		cmd.Dir = step.Cwd
	}

	var stdoutBuf bytes.Buffer
	if step.CaptureStdout {
		cmd.Stdout = &stdoutBuf
	} else if opts.Verbose {
		cmd.Stdout = os.Stdout
	}

	var stderrBuf bytes.Buffer
	if opts.Verbose {
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stderr = &stderrBuf
	}

	if err := cmd.Run(); err != nil {
		if !opts.Quiet {
			fmt.Printf("✗ %s\n", step.Output)
		}
		if stderrBuf.Len() > 0 {
			fmt.Fprintf(os.Stderr, "--- %s stderr ---\n%s\n", step.ID, stderrBuf.String())
		}
		return fmt.Errorf("%s: %w", step.ID, err)
	}

	// Write captured stdout to the output file (atomically, only if changed).
	if step.CaptureStdout {
		if err := os.MkdirAll(filepath.Dir(step.Output), 0o755); err != nil {
			return fmt.Errorf("%s: mkdir: %w", step.ID, err)
		}
		if _, err := writeFileChanged(step.Output, stdoutBuf.Bytes()); err != nil {
			return fmt.Errorf("%s: write output: %w", step.ID, err)
		}
	}

	// Record success in the manifest.
	mf.Set(step.Output, hash)

	if !opts.Quiet {
		fmt.Printf("✓ %s\n", step.Output)
	}
	return nil
}

// hasCancelledDep reports whether any element of deps is in the cancelled set.
func hasCancelledDep(deps []string, cancelled map[string]bool) bool {
	for _, d := range deps {
		if cancelled[d] {
			return true
		}
	}
	return false
}

// writeFileChanged writes data to path only when the file's existing content
// differs. It uses an atomic temp-file + rename so partial writes never corrupt
// an in-use file. Reports true when the file was actually written.
func writeFileChanged(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read existing %q: %w", path, err)
	}
	if bytes.Equal(existing, data) {
		return false, nil
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".hledger-build-tmp-*")
	if err != nil {
		return false, fmt.Errorf("create temp file in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after successful rename

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return false, fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return false, fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return false, fmt.Errorf("rename to %q: %w", path, err)
	}
	return true, nil
}
