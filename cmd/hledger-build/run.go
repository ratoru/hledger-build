package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/fatih/color"
	"github.com/ratoru/hledger-build/internal/manifest"
	"github.com/ratoru/hledger-build/internal/passes"
	"github.com/ratoru/hledger-build/internal/runner"
	"github.com/spf13/cobra"
)

// passMode selects which build passes to execute.
type passMode int

const (
	passAll    passMode = 0
	passIngest passMode = 1
	passReport passMode = 2
)

func newRunCmd() *cobra.Command {
	var passFlag string
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Run the build pipeline (default action)",
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := parsePassFlag(passFlag)
			if err != nil {
				return err
			}
			return runPipeline(cmd.Context(), mode)
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&passFlag, "pass", "all", "which pass to run: all, ingest, report")
	return cmd
}

func parsePassFlag(s string) (passMode, error) {
	switch strings.ToLower(s) {
	case "all", "":
		return passAll, nil
	case "ingest":
		return passIngest, nil
	case "report":
		return passReport, nil
	default:
		return passAll, fmt.Errorf("unknown pass %q: must be all, ingest, or report", s)
	}
}

// runPipeline is the core execution function: loads config, generates Pass 1
// steps and writes import stubs, then runs the requested passes.
func runPipeline(ctx context.Context, mode passMode) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := checkHledgerVersion(ctx, cfg.HledgerBinary); err != nil {
		return err
	}

	// Generate Pass 1 steps upfront so we can write import stubs before any
	// pass runs. The stubs (reports/{year}-imports.journal) tell each year
	// journal which ingest journals to include, enabling GetIncludes to resolve
	// Pass 2 dependencies correctly.
	pass1Steps, err := passes.GeneratePass1Steps(cfg)
	if err != nil {
		return fmt.Errorf("generating ingest steps: %w", err)
	}
	if err := passes.WriteImportStubs(cfg, pass1Steps); err != nil {
		return fmt.Errorf("writing import stubs: %w", err)
	}

	mfPath := manifestPath(cfg)
	mf, err := loadManifest(mfPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	runner.HandleSignals(ctx, cancel, mf, mfPath)

	opts := runOpts(cfg)

	if mode == passAll || mode == passIngest {
		if err := execPass(ctx, mf, opts, "1. Ingesting Files", pass1Steps); err != nil {
			_ = mf.Save(mfPath)
			return err
		}
	}

	if mode == passAll || mode == passReport {
		pass2Steps, err := passes.GeneratePass2Steps(cfg)
		if err != nil {
			return fmt.Errorf("generating report steps: %w", err)
		}
		if err := execPass(ctx, mf, opts, "2. Generating Reports", pass2Steps); err != nil {
			_ = mf.Save(mfPath)
			return err
		}
	}

	return mf.Save(mfPath)
}

// execPass topologically sorts and executes a set of pre-computed steps.
func execPass(
	ctx context.Context,
	mf *manifest.Manifest,
	opts runner.RunOpts,
	label string,
	steps []runner.Step,
) error {
	tiers, err := runner.TopoSort(steps)
	if err != nil {
		return fmt.Errorf("sorting %s steps: %w", label, err)
	}
	if !opts.Quiet {
		_, _ = color.New(color.Bold).Printf("=== %s ===\n", label)
	}
	return runner.RunSteps(ctx, tiers, mf, opts)
}
