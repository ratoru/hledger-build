// Package passes implements step generation for the two-pass pipeline.
package passes

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/ratoru/hledger-build/internal/config"
	"github.com/ratoru/hledger-build/internal/runner"
)

// GeneratePass1Steps generates all ingest pipeline steps (Pass 1) for every
// discovered source. Each step transforms input files (raw → cleaned → journal)
// for a specific source+stage+year+file combination.
func GeneratePass1Steps(cfg *config.Config) ([]runner.Step, error) {
	var steps []runner.Step
	for _, sourceName := range cfg.DiscoveredSources {
		srcSteps, err := generateSourceSteps(cfg, sourceName)
		if err != nil {
			return nil, fmt.Errorf("source %q: %w", sourceName, err)
		}
		steps = append(steps, srcSteps...)
	}
	return steps, nil
}

// generateSourceSteps produces all steps for one source directory across all
// pipeline stages and years.
func generateSourceSteps(cfg *config.Config, sourceName string) ([]runner.Step, error) {
	pipeline := effectivePipeline(cfg, sourceName)

	mainRulesFile, allRulesFiles, err := config.DiscoverRulesFiles(cfg.ProjectRoot, cfg.Directories, sourceName)
	if err != nil {
		return nil, fmt.Errorf("discovering rules: %w", err)
	}

	override := cfg.Sources[sourceName]

	// sourceDir is relative to the project root (used as Cwd for local scripts).
	sourceDir := filepath.ToSlash(filepath.Join(cfg.Directories.Sources, sourceName))
	// absSourceDir is used for on-disk operations (globbing, script existence checks).
	absSourceDir := filepath.Join(cfg.ProjectRoot, cfg.Directories.Sources, filepath.FromSlash(sourceName))

	var steps []runner.Step

	// overrideInputDir carries the effective input directory of a skipped stage
	// forward to the next stage so the pipeline stays connected when an optional
	// stage is absent.
	overrideInputDir := ""

	// latestStepOutputs holds the Output paths (project-root-relative) produced
	// by the most recent stage that actually generated steps. Passed to the next
	// stage so it can derive its inputs without requiring the output directory to
	// already exist on disk.
	var latestStepOutputs []string

	for _, stage := range pipeline {
		effectiveInputDir := stage.InputDir
		if overrideInputDir != "" {
			effectiveInputDir = overrideInputDir
			overrideInputDir = ""
		}

		resolvedScript, scriptExists := resolveStageScript(stage.Script, absSourceDir)

		if !scriptExists {
			if stage.Optional {
				// Stage skipped; next stage should read from where this stage
				// would have read from (not from its non-existent output).
				overrideInputDir = effectiveInputDir
				continue
			}
			return nil, fmt.Errorf("stage %q: required script %q not found in %s",
				stage.Name, stage.Script, absSourceDir)
		}
		// Use the resolved script command (may include a Windows file extension).
		stage.Script = resolvedScript

		stageSteps, err := generateStageSteps(
			cfg, sourceName, sourceDir, absSourceDir,
			stage, effectiveInputDir, mainRulesFile, allRulesFiles, override.ExtraDeps,
			latestStepOutputs,
		)
		if err != nil {
			return nil, fmt.Errorf("stage %q: %w", stage.Name, err)
		}
		steps = append(steps, stageSteps...)

		// Record this stage's outputs so the next stage can use them directly
		// instead of globbing the filesystem.
		latestStepOutputs = nil
		for _, s := range stageSteps {
			latestStepOutputs = append(latestStepOutputs, s.Output)
		}
	}

	return steps, nil
}

// generateStageSteps generates one Step per matching input file for a pipeline
// stage, iterating over every year in [cfg.FirstYear, cfg.CurrentYear].
//
// upstreamInputs, when non-nil, provides the project-root-relative output paths
// from the preceding pipeline stage. They are used directly as inputs instead of
// globbing the filesystem, so the pipeline can be fully planned from raw/ files
// alone without requiring intermediate directories to exist on disk.
func generateStageSteps(
	cfg *config.Config,
	sourceName, sourceDir, absSourceDir string,
	stage config.Pipeline,
	effectiveInputDir string,
	mainRulesFile string, allRulesFiles, extraDeps []string,
	upstreamInputs []string,
) ([]runner.Step, error) {
	if cfg.FirstYear == 0 && cfg.CurrentYear == 0 {
		return nil, nil
	}

	var steps []runner.Step

	for year := cfg.FirstYear; year <= cfg.CurrentYear; year++ {
		yearStr := strconv.Itoa(year)

		var absInputs []string
		if upstreamInputs != nil {
			// Derive inputs from the previous stage's outputs for this year.
			// Upstream paths have the form:
			//   {sources}/{sourceName}/{effectiveInputDir}/{year}/{file}
			prefix := cfg.Directories.Sources + "/" + sourceName + "/" + effectiveInputDir + "/" + yearStr + "/"
			for _, out := range upstreamInputs {
				if strings.HasPrefix(out, prefix) {
					absInputs = append(absInputs, filepath.Join(cfg.ProjectRoot, filepath.FromSlash(out)))
				}
			}
		} else {
			globPat := filepath.Join(absSourceDir, effectiveInputDir, yearStr, stage.InputGlob)
			matches, err := filepath.Glob(globPat)
			if err != nil {
				return nil, fmt.Errorf("glob %q: %w", globPat, err)
			}
			absInputs = matches
		}

		for _, absInput := range absInputs {
			inputRel, err := filepath.Rel(cfg.ProjectRoot, absInput)
			if err != nil {
				return nil, err
			}
			inputRel = filepath.ToSlash(inputRel)

			base := strings.TrimSuffix(filepath.Base(absInput), filepath.Ext(absInput))
			outputRel := filepath.ToSlash(filepath.Join(
				cfg.Directories.Sources, sourceName,
				stage.OutputDir, yearStr,
				base+stage.OutputExt,
			))

			step, err := buildStep(
				cfg, sourceDir, absSourceDir,
				stage, inputRel, outputRel,
				mainRulesFile, allRulesFiles, extraDeps,
			)
			if err != nil {
				return nil, err
			}
			steps = append(steps, step)
		}
	}

	return steps, nil
}

// buildStep constructs a runner.Step for a single input file.
func buildStep(
	cfg *config.Config,
	sourceDir, absSourceDir string,
	stage config.Pipeline,
	inputRel, outputRel string,
	mainRulesFile string, allRulesFiles, extraDeps []string,
) (runner.Step, error) {
	var cmd string
	var args []string
	var cwd string

	// The input file is always a dependency.
	deps := []string{inputRel}

	if stage.Script == "hledger" {
		cmd = cfg.HledgerBinary

		// Check for a per-file rules override: <sourceDir>/<basename>.csv.rules.
		base := strings.TrimSuffix(filepath.Base(filepath.FromSlash(inputRel)), filepath.Ext(filepath.FromSlash(inputRel)))
		perFileAbs := filepath.Join(absSourceDir, base+".csv.rules")
		effectiveRules := mainRulesFile
		if _, err := os.Stat(perFileAbs); err == nil {
			perFileRel, err := filepath.Rel(cfg.ProjectRoot, perFileAbs)
			if err != nil {
				return runner.Step{}, err
			}
			effectiveRules = filepath.ToSlash(perFileRel)
			deps = append(deps, effectiveRules)
		}

		args = buildHledgerArgs(inputRel, effectiveRules)
		cwd = "" // run from project root (inherits process working directory)
		// All main.rules files are deps so any change triggers a rebuild.
		deps = append(deps, allRulesFiles...)
	} else {
		cmd = stage.Script
		// Pass the input file relative to the source directory (the cwd).
		absInput := filepath.Join(cfg.ProjectRoot, filepath.FromSlash(inputRel))
		inputRelToSource, err := filepath.Rel(absSourceDir, absInput)
		if err != nil {
			return runner.Step{}, fmt.Errorf("computing input path relative to source dir: %w", err)
		}
		args = []string{filepath.ToSlash(inputRelToSource)}
		cwd = sourceDir
	}

	// Extra deps are specified relative to the source directory.
	for _, dep := range extraDeps {
		depRel := filepath.ToSlash(filepath.Join(sourceDir, dep))
		deps = append(deps, depRel)
	}

	return runner.Step{
		ID:              outputRel,
		Output:          outputRel,
		Deps:            deps,
		Command:         cmd,
		Args:            args,
		Cwd:             cwd,
		CaptureStdout:   true,
		Pass:            1,
		IsIngestJournal: stage.OutputExt == ".journal",
	}, nil
}

// buildHledgerArgs constructs the hledger arguments for CSV→journal conversion
// via `hledger -f <input> [--rules r] print`.
func buildHledgerArgs(inputRel string, rulesFile string) []string {
	args := []string{"-f", inputRel}
	if rulesFile != "" {
		args = append(args, "--rules", rulesFile)
	}
	args = append(args, "print")
	return args
}

// effectivePipeline returns the ordered pipeline stages for a given source,
// incorporating source-specific full overrides or extra-stage splices.
func effectivePipeline(cfg *config.Config, sourceName string) []config.Pipeline {
	override, hasOverride := cfg.Sources[sourceName]

	// A full pipeline override replaces the default entirely.
	if hasOverride && len(override.Pipeline) > 0 {
		return override.Pipeline
	}

	// Copy the default pipeline so we never mutate cfg.
	result := make([]config.Pipeline, len(cfg.Pipeline))
	copy(result, cfg.Pipeline)

	if !hasOverride || len(override.ExtraStages) == 0 {
		return result
	}

	// Splice each extra stage after its named target stage.
	for _, extra := range override.ExtraStages {
		extraStage := config.Pipeline{
			Name:      extra.Name,
			Script:    extra.Script,
			InputDir:  extra.InputDir,
			InputGlob: extra.InputGlob,
			OutputDir: extra.OutputDir,
			OutputExt: extra.OutputExt,
			Optional:  false, // extra stages are required by default
		}

		insertPos := len(result) // default: append at end
		for i, s := range result {
			if s.Name == extra.After {
				insertPos = i + 1
				break
			}
		}

		// Safe insert (avoids aliasing the backing array).
		newResult := make([]config.Pipeline, 0, len(result)+1)
		newResult = append(newResult, result[:insertPos]...)
		newResult = append(newResult, extraStage)
		newResult = append(newResult, result[insertPos:]...)
		result = newResult
	}

	return result
}

// resolveStageScript returns the command string to use for a pipeline stage.
// Bare binary names (including "hledger") are returned as-is with found=true
// since PATH availability is assumed and checked at runtime.
// Local scripts (starting with "./" etc.) are stat-checked in absSourceDir;
// on Windows, .bat, .cmd, and .ps1 extensions are also tried so that
// Windows-style scripts are discovered when Script is configured without an
// extension (e.g. "./preprocess" finds "./preprocess.bat").
// Returns ("", false) if a local script cannot be found under any tried name.
func resolveStageScript(script, absSourceDir string) (resolved string, found bool) {
	if !isLocalScript(script) {
		// Bare binary name or "hledger" — assumed available via PATH.
		return script, true
	}
	base := filepath.Join(absSourceDir, filepath.FromSlash(script))
	if _, err := os.Stat(base); err == nil {
		return script, true
	}
	// On Windows, try common script extensions so users can name their scripts
	// preprocess.bat / preprocess.cmd / preprocess.ps1 while keeping the
	// config entry as "./preprocess".
	if runtime.GOOS == "windows" {
		for _, ext := range []string{".bat", ".cmd", ".ps1"} {
			if _, err := os.Stat(base + ext); err == nil {
				return script + ext, true
			}
		}
	}
	return "", false
}

// isLocalScript reports whether a script value is a relative path to a local
// file rather than a bare binary name. Accepts both Unix (./…) and Windows
// (.\…) style prefixes.
func isLocalScript(script string) bool {
	return strings.HasPrefix(script, "./") || strings.HasPrefix(script, "../") ||
		strings.HasPrefix(script, `.\\`) || strings.HasPrefix(script, `..\\`)
}
