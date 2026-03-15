package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ratoru/hledger-build/internal/config"
	"github.com/ratoru/hledger-build/internal/manifest"
	"github.com/ratoru/hledger-build/internal/runner"
)

var version = func() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}()

// ── Global flag variables (set by persistent flags on root command) ────────────

var (
	flagConfig   string
	flagForce    bool
	flagDryRun   bool
	flagFailFast bool
	flagJobs     int
	flagVerbose  bool
	flagQuiet    bool
)

// buildRootCmd constructs the root cobra command with all subcommands.
func buildRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "hledger-build",
		Version: version,
		Short:   "Incremental build system for hledger personal finance plain text accounting",
		Long: `hledger-build orchestrates the processing of raw bank data into hledger
journal files and reports.

Running without a subcommand is equivalent to 'hledger-build run'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPipeline(cmd.Context(), passAll)
		},
		SilenceUsage: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true

	// Persistent (global) flags available to all subcommands.
	pf := root.PersistentFlags()
	pf.StringVar(&flagConfig, "config", "", "path to config file (default: hledger-build.toml in cwd)")
	pf.BoolVar(&flagForce, "force", false, "rebuild all targets, ignoring cached hashes")
	pf.BoolVar(&flagDryRun, "dry-run", false, "print what would run without executing")
	pf.BoolVar(&flagFailFast, "fail-fast", false, "stop on first failure")
	pf.IntVarP(&flagJobs, "jobs", "j", 0, "parallel jobs (0 = NumCPU)")
	pf.BoolVarP(&flagVerbose, "verbose", "v", false, "print subprocess stdout/stderr inline")
	pf.BoolVarP(&flagQuiet, "quiet", "q", false, "suppress all output except errors")

	root.AddCommand(newRunCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newAddCmd())
	root.AddCommand(newCleanCmd())
	root.AddCommand(newStatusCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newCategorizeCmd())
	root.AddCommand(newConsolidateCmd())
	root.AddCommand(newMetricsCmd())

	return root
}

// ── Shared helpers ─────────────────────────────────────────────────────────────

// loadConfig reads the config file and applies CLI flag overrides.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(flagConfig)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	// CLI flags override config-file values when explicitly set.
	if flagForce {
		cfg.Force = true
	}
	if flagDryRun {
		cfg.DryRun = true
	}
	if flagVerbose {
		cfg.Verbose = true
	}
	if flagQuiet {
		cfg.Quiet = true
	}
	if flagFailFast {
		cfg.FailFast = true
	}
	if flagJobs > 0 {
		cfg.Jobs = flagJobs
	}
	// Record the path to this binary so pass2 can invoke 'hledger-build metrics'.
	if exe, err := os.Executable(); err == nil {
		cfg.SelfBinary = exe
	}
	return cfg, nil
}

// manifestPath returns the path to the manifest file for this project.
func manifestPath(cfg *config.Config) string {
	return filepath.Join(cfg.ProjectRoot, cfg.Directories.Build, "manifest.json")
}

// loadManifest ensures the build directory exists and loads the manifest.
func loadManifest(path string) (*manifest.Manifest, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating build directory: %w", err)
	}
	mf, err := manifest.Load(path)
	if err != nil {
		return nil, fmt.Errorf("loading manifest: %w", err)
	}
	return mf, nil
}

// runOpts converts the loaded config to RunOpts for the runner.
func runOpts(cfg *config.Config) runner.RunOpts {
	return runner.RunOpts{
		Jobs:     cfg.Jobs,
		Force:    cfg.Force,
		DryRun:   cfg.DryRun,
		Verbose:  cfg.Verbose,
		Quiet:    cfg.Quiet,
		FailFast: cfg.FailFast,
	}
}

// ── hledger version check ──────────────────────────────────────────────────────

const (
	minHledgerMajor = 1
	minHledgerMinor = 51
)

// checkHledgerVersion runs `binary --version` and returns an error if the
// reported version is too old.
func checkHledgerVersion(ctx context.Context, binary string) error {
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return fmt.Errorf("%q not found or failed to run: %w", binary, err)
	}
	// Output: "hledger 1.51.2, mac-aarch64"
	line := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return fmt.Errorf("unexpected %q --version output: %q", binary, line)
	}
	verStr := strings.TrimRight(parts[1], ",")
	segments := strings.SplitN(verStr, ".", 3)
	if len(segments) < 2 {
		return fmt.Errorf("could not parse version from %q --version output: %q", binary, line)
	}
	major, err1 := strconv.Atoi(segments[0])
	minor, err2 := strconv.Atoi(segments[1])
	if err1 != nil || err2 != nil {
		return fmt.Errorf("could not parse version from %q --version output: %q", binary, line)
	}
	if major < minHledgerMajor || (major == minHledgerMajor && minor < minHledgerMinor) {
		return fmt.Errorf("%q is version %s, but >= %d.%d is required (see https://hledger.org/install.html)",
			binary, verStr, minHledgerMajor, minHledgerMinor)
	}
	return nil
}

// relOrAbs returns path relative to base, falling back to absolute on error.
func relOrAbs(base, path string) string {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return path
	}
	return rel
}
