package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newCleanCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "clean",
		Short:        "Remove all generated files and the build cache",
		RunE:         func(cmd *cobra.Command, args []string) error { return runClean() },
		SilenceUsage: true,
	}
}

func runClean() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	toRemove := []string{
		filepath.Join(cfg.ProjectRoot, cfg.Directories.Reports),
		filepath.Join(cfg.ProjectRoot, cfg.Directories.Build),
	}

	// Remove cleaned/ and journal/ under each discovered source.
	for _, src := range cfg.DiscoveredSources {
		srcDir := filepath.Join(cfg.ProjectRoot, cfg.Directories.Sources, filepath.FromSlash(src))
		toRemove = append(toRemove,
			filepath.Join(srcDir, cfg.Directories.Cleaned),
			filepath.Join(srcDir, cfg.Directories.Journal),
		)
	}

	for _, path := range toRemove {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("removing %s: %w", path, err)
		}
		fmt.Printf("removed  %s/\n", relOrAbs(cfg.ProjectRoot, path))
	}

	return nil
}
