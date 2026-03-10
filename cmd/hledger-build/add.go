package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "add",
		Short:        "Scaffold new project components",
		SilenceUsage: true,
	}
	cmd.AddCommand(newAddYearCmd())
	cmd.AddCommand(newAddSourceCmd())
	return cmd
}

func newAddYearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "year <YEAR>",
		Short: "Scaffold a new year in an existing hledger-build project",
		Long: `Scaffold a new year in an existing hledger-build project.

Creates:
  - sources/_manual/<year>/budget.journal      periodic budget rules
  - sources/_manual/<year>/valuations.journal  end-of-year investment valuations
  - sources/<source>/raw/<year>/               raw import directory for each source
  - <year>.journal                             hledger entry point for the year

Updates:
  - all.journal    appends 'include reports/<prev>-closing.journal'
                   and 'include <year>.journal'`,
		Args:         cobra.ExactArgs(1),
		RunE:         func(cmd *cobra.Command, args []string) error { return runAddYear(cmd.Context(), args[0]) },
		SilenceUsage: true,
	}
}

func newAddSourceCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "source <name>",
		Short:        "Scaffold a new source in an existing hledger-build project",
		Args:         cobra.ExactArgs(1),
		RunE:         func(cmd *cobra.Command, args []string) error { return runAddSource(cmd.Context(), args[0]) },
		SilenceUsage: true,
	}
}

func runAddYear(_ context.Context, yearArg string) error {
	year, err := parseYear(yearArg)
	if err != nil {
		return err
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	root := cfg.ProjectRoot
	dirs := cfg.Directories
	prevYearStr := strconv.Itoa(year - 1)

	// Guard against overwriting an existing year.
	yearJournalPath := filepath.Join(root, yearArg+".journal")
	if _, err := os.Stat(yearJournalPath); err == nil {
		return fmt.Errorf("%s.journal already exists; aborting to avoid overwriting", yearArg)
	}

	// Create sources/_manual/{year}/
	manualYearDir := filepath.Join(root, dirs.Sources, dirs.Manual, yearArg)
	if err := os.MkdirAll(manualYearDir, 0o750); err != nil {
		return fmt.Errorf("creating directory %s: %w", relOrAbs(root, manualYearDir), err)
	}
	fmt.Printf("created  %s/\n", relOrAbs(root, manualYearDir))

	// Write budget.journal — periodic transaction rules for the budget report.
	budgetPath := filepath.Join(manualYearDir, "budget.journal")
	if _, err := os.Stat(budgetPath); os.IsNotExist(err) {
		if err := os.WriteFile(budgetPath, []byte(manualBudgetContent(year)), 0o600); err != nil {
			return fmt.Errorf("writing budget.journal: %w", err)
		}
		fmt.Printf("created  %s\n", relOrAbs(root, budgetPath))
	}

	// Write valuations.journal — end-of-year investment valuation entries.
	valuationsPath := filepath.Join(manualYearDir, "valuations.journal")
	if _, err := os.Stat(valuationsPath); os.IsNotExist(err) {
		if err := os.WriteFile(valuationsPath, []byte(manualValuationsContent(year)), 0o600); err != nil {
			return fmt.Errorf("writing valuations.journal: %w", err)
		}
		fmt.Printf("created  %s\n", relOrAbs(root, valuationsPath))
	}

	// Create raw/{year}/ under each existing source.
	for _, src := range cfg.DiscoveredSources {
		rawYearDir := filepath.Join(root, dirs.Sources, filepath.FromSlash(src), dirs.Raw, yearArg)
		if err := os.MkdirAll(rawYearDir, 0o750); err != nil {
			return fmt.Errorf("creating directory %s: %w", relOrAbs(root, rawYearDir), err)
		}
		fmt.Printf("created  %s/\n", relOrAbs(root, rawYearDir))
	}

	// Create {year}.journal.
	if err := os.WriteFile(yearJournalPath, []byte(addYearJournalContent(year)), 0o600); err != nil {
		return fmt.Errorf("writing %s.journal: %w", yearArg, err)
	}
	fmt.Printf("created  %s.journal\n", yearArg)

	// Append closing + new year includes to all.journal.
	allJournalPath := filepath.Join(root, "all.journal")
	anyUpdated := false
	for _, line := range []string{
		fmt.Sprintf("\ninclude reports/%s-closing.journal", prevYearStr),
		fmt.Sprintf("include %s.journal", yearArg),
	} {
		added, err := appendLineIfAbsent(allJournalPath, line)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update all.journal: %v\n", err)
		} else if added {
			anyUpdated = true
		}
	}
	if anyUpdated {
		fmt.Println("updated  all.journal")
	}

	_, _ = color.New(color.FgGreen, color.Bold).Printf("\nYear %s added. Next steps:\n", yearArg)
	fmt.Printf("  1. Drop %s CSVs into the raw/%s/ dir of each source\n", yearArg, yearArg)
	fmt.Println("  2. Run: hledger-build run")
	return nil
}

func runAddSource(_ context.Context, sourcePath string) error {
	clean := filepath.ToSlash(strings.Trim(sourcePath, "/"))
	parts := strings.Split(clean, "/")
	if slices.Contains(parts, "") {
		return fmt.Errorf("source path must not contain empty components, got %q", sourcePath)
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	root := cfg.ProjectRoot
	dirs := cfg.Directories

	// Guard against overwriting an existing source.
	sourceDir := filepath.Join(root, dirs.Sources, filepath.FromSlash(clean))
	if _, err := os.Stat(sourceDir); err == nil {
		return fmt.Errorf("source %q already exists at %s", clean, relOrAbs(root, sourceDir))
	}

	// Collect years to pre-create raw/{year}/ dirs.
	var years []string
	for y := cfg.FirstYear; y <= cfg.CurrentYear; y++ {
		years = append(years, strconv.Itoa(y))
	}

	if len(years) == 0 {
		// No years discovered; create raw/ only so discovery works after the first run.
		rawDir := filepath.Join(sourceDir, dirs.Raw)
		if err := os.MkdirAll(rawDir, 0o750); err != nil {
			return fmt.Errorf("creating directory %s: %w", relOrAbs(root, rawDir), err)
		}
		fmt.Printf("created  %s/\n", relOrAbs(root, rawDir))
	} else {
		for _, y := range years {
			rawYearDir := filepath.Join(sourceDir, dirs.Raw, y)
			if err := os.MkdirAll(rawYearDir, 0o750); err != nil {
				return fmt.Errorf("creating directory %s: %w", relOrAbs(root, rawYearDir), err)
			}
			fmt.Printf("created  %s/\n", relOrAbs(root, rawYearDir))
		}
	}

	// Write main.rules skeleton (reuses the same template as init).
	rulesPath := filepath.Join(sourceDir, "main.rules")
	if err := os.WriteFile(rulesPath, []byte(exampleRules), 0o600); err != nil {
		return fmt.Errorf("writing main.rules: %w", err)
	}
	fmt.Printf("created  %s\n", relOrAbs(root, rulesPath))

	// Write preprocess stub (reuses the same template as init).
	preprocessPath := filepath.Join(sourceDir, "preprocess")
	if err := os.WriteFile( //nolint:gosec // G306: preprocess script must be executable
		preprocessPath, []byte(examplePreprocess), 0o755,
	); err != nil {
		return fmt.Errorf("writing preprocess: %w", err)
	}
	fmt.Printf("created  %s\n", relOrAbs(root, preprocessPath))

	_, _ = color.New(color.FgGreen, color.Bold).Printf("\nSource %q added. Next steps:\n", clean)
	fmt.Printf("  1. Edit %s to match your bank's CSV format\n", relOrAbs(root, rulesPath))
	fmt.Printf("  2. Drop CSVs into %s/{year}/\n", relOrAbs(root, filepath.Join(sourceDir, dirs.Raw)))
	fmt.Println("  3. Run: hledger-build run")
	return nil
}

// parseYear validates a year string and returns its integer value.
func parseYear(s string) (int, error) {
	if len(s) != 4 {
		return 0, fmt.Errorf("year must be a 4-digit number, got %q", s)
	}
	y, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("year must be a 4-digit number, got %q", s)
	}
	return y, nil
}

// addYearJournalContent returns {year}.journal content for a year added to an
// existing project. Opening balances are pulled from the auto-generated
// reports/{year}-opening.journal produced by the prior-year closing run.
func addYearJournalContent(year int) string {
	return fmt.Sprintf(
		"; %d.journal – hledger entry point for %d.\n"+
			";\n"+
			"; reports/%d-opening.journal is auto-generated from the prior-year closing run.\n"+
			"\n"+
			"include commodities.journal\n"+
			"include accounts.journal\n"+
			"include reports/%d-opening.journal\n\n"+
			"include sources/_manual/%d/*.journal\n"+
			"include sources/%d-imports.journal\n",
		year, year, year, year, year, year,
	)
}

// appendLineIfAbsent appends line to path if no existing line matches it
// (after trimming whitespace). Returns true if the line was appended.
func appendLineIfAbsent(path, line string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	needle := strings.TrimSpace(line)
	for existing := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(existing) == needle {
			return false, nil
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = fmt.Fprintln(f, line)
	return err == nil, err
}
