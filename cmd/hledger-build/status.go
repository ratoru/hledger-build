package main

import (
	"fmt"
	"os"

	"github.com/fatih/color"
	"github.com/ratoru/hledger-build/internal/manifest"
	"github.com/ratoru/hledger-build/internal/passes"
	"github.com/ratoru/hledger-build/internal/runner"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show what would be rebuilt without executing anything",
		Long: `Loads config, generates all steps, computes hashes and checks the manifest.
Prints a tier-by-tier summary of cached vs. to-build counts for each pass.
Use --verbose to also print the command for each non-cached step.`,
		RunE:         func(cmd *cobra.Command, args []string) error { return runStatus() },
		SilenceUsage: true,
	}
}

// passStats holds aggregated cache statistics for one build pass.
type passStats struct {
	total  int
	cached int
	tiers  []tierStats
}

// tierStats holds statistics for one execution tier within a pass.
type tierStats struct {
	total   int
	cached  int
	toBuild []string // output paths of steps that need rebuilding
}

func runStatus() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	mfPath := manifestPath(cfg)
	mf, err := loadManifest(mfPath)
	if err != nil {
		return err
	}

	pass1Steps, err := passes.GeneratePass1Steps(cfg)
	if err != nil {
		return fmt.Errorf("generating ingest steps: %w", err)
	}
	pass1Tiers, err := runner.TopoSort(pass1Steps)
	if err != nil {
		return fmt.Errorf("sorting ingest steps: %w", err)
	}

	pass2Steps, err := passes.GeneratePass2Steps(cfg)
	if err != nil {
		return fmt.Errorf("generating report steps: %w", err)
	}
	pass2Tiers, err := runner.TopoSort(pass2Steps)
	if err != nil {
		return fmt.Errorf("sorting report steps: %w", err)
	}

	p1 := gatherPassStats(pass1Tiers, mf)
	p2 := gatherPassStats(pass2Tiers, mf)

	printPassStats("Pass 1 (Ingest)", p1, flagVerbose)
	printPassStats("Pass 2 (Reports)", p2, flagVerbose)

	grandTotal := p1.total + p2.total
	grandCached := p1.cached + p2.cached
	toBuild := grandTotal - grandCached
	fmt.Printf("\nTotal: %d steps, %d cached, ", grandTotal, grandCached)
	if toBuild > 0 {
		_, _ = color.New(color.FgYellow).Printf("%d to build", toBuild)
	} else {
		_, _ = color.New(color.FgGreen).Printf("%d to build", toBuild)
	}
	fmt.Println()
	return nil
}

// gatherPassStats computes cache statistics for all tiers in a pass.
func gatherPassStats(tiers [][]runner.Step, mf *manifest.Manifest) passStats {
	var ps passStats
	for _, tier := range tiers {
		ts := tierStats{total: len(tier)}
		for _, step := range tier {
			hash, err := runner.ComputeHash(step)
			if err == nil {
				if stored, ok := mf.Get(step.Output); ok && stored == hash {
					if _, statErr := os.Stat(step.Output); statErr == nil {
						ts.cached++
						continue
					}
				}
			}
			ts.toBuild = append(ts.toBuild, step.Output)
		}
		ps.total += ts.total
		ps.cached += ts.cached
		ps.tiers = append(ps.tiers, ts)
	}
	return ps
}

// printPassStats prints the pass summary followed by per-tier breakdown.
func printPassStats(label string, ps passStats, verbose bool) {
	toBuild := ps.total - ps.cached
	_, _ = color.New(color.Bold).Printf("%s", label)
	fmt.Printf(": %d steps (%d cached, ", ps.total, ps.cached)
	if toBuild > 0 {
		_, _ = color.New(color.FgYellow).Printf("%d to build", toBuild)
	} else {
		_, _ = color.New(color.FgGreen).Printf("%d to build", toBuild)
	}
	fmt.Println(")")
	for i, ts := range ps.tiers {
		fmt.Printf("  Tier %d: %d steps (%d cached)\n", i+1, ts.total, ts.cached)
		if verbose {
			for _, id := range ts.toBuild {
				_, _ = color.New(color.FgYellow).Printf("    + %s\n", id)
			}
		}
	}
}
