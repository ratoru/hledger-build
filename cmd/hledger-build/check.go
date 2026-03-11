package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/ratoru/hledger-build/internal/config"
)

func newCheckCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Validate all year journals with --strict",
		Long: `Runs 'hledger --strict check' against every year journal to catch
undeclared accounts/commodities and balance assertion failures.

Set check_query in hledger-build.toml to scope the check to a subset of
transactions (passed as a query argument to 'hledger check').`,
		RunE:         func(cmd *cobra.Command, args []string) error { return runCheck(cmd.Context()) },
		SilenceUsage: true,
	}
}

func runCheck(ctx context.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := checkHledgerVersion(ctx, cfg.HledgerBinary); err != nil {
		return err
	}
	if cfg.FirstYear == 0 || cfg.CurrentYear == 0 {
		if !flagQuiet {
			fmt.Println("No years discovered; nothing to check.")
		}
		return nil
	}

	var failed []string
	for year := cfg.FirstYear; year <= cfg.CurrentYear; year++ {
		yearStr := strconv.Itoa(year)
		journalFile := yearStr + ".journal"

		if err := runHledgerCheck(ctx, cfg, journalFile); err != nil {
			failed = append(failed, yearStr)
		}

		if flagFailFast && len(failed) > 0 {
			break
		}
	}

	// Also check all.journal if it exists.
	if _, err := os.Stat(filepath.Join(cfg.ProjectRoot, "all.journal")); err == nil {
		if err := runHledgerCheck(ctx, cfg, "all.journal"); err != nil {
			failed = append(failed, "all")
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("check failed for: %s", strings.Join(failed, ", "))
	}
	if !flagQuiet {
		_, _ = color.New(color.FgGreen, color.Bold).Println("\nAll checks passed.")
	}

	// Warn about any remaining expenses:unknown transactions.
	var journals []string
	for year := cfg.FirstYear; year <= cfg.CurrentYear; year++ {
		journals = append(journals, strconv.Itoa(year)+".journal")
	}
	if _, err := os.Stat(filepath.Join(cfg.ProjectRoot, "all.journal")); err == nil {
		journals = append(journals, "all.journal")
	}
	warnUnknownExpenses(ctx, cfg, journals)

	return nil
}

// warnUnknownExpenses prints a yellow warning for any journal that contains
// expenses:unknown transactions, suggesting 'hledger-build categorize'.
func warnUnknownExpenses(ctx context.Context, cfg *config.Config, journals []string) {
	var withUnknown []string
	for _, j := range journals {
		cmd := exec.CommandContext(ctx, cfg.HledgerBinary, "-f", j, "print", "expenses:unknown")
		cmd.Dir = cfg.ProjectRoot
		out, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(out))) > 0 {
			withUnknown = append(withUnknown, j)
		}
	}
	if len(withUnknown) == 0 {
		return
	}
	if !flagQuiet {
		_, _ = color.New(color.FgYellow, color.Bold).Printf(
			"\nWarning: expenses:unknown transactions found in: %s\n",
			strings.Join(withUnknown, ", "),
		)
		fmt.Println("Run 'hledger-build categorize' to assign them.")
	}
}

// runHledgerCheck runs `hledger -f journalFile --strict check [check_query]`.
func runHledgerCheck(ctx context.Context, cfg *config.Config, journalFile string) error {
	args := []string{"-f", journalFile, "--strict", "check"}
	if cfg.CheckQuery != "" {
		args = append(args, cfg.CheckQuery)
	}

	cmd := exec.CommandContext(ctx, cfg.HledgerBinary, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if !flagQuiet {
			_, _ = color.New(color.FgRed, color.Bold).Printf("✗ %s\n", journalFile)
			if stderr.Len() > 0 {
				_, _ = fmt.Fprintf(os.Stderr, "%s\n", strings.TrimSpace(stderr.String()))
			}
		}
		return err
	}

	if !flagQuiet {
		_, _ = color.New(color.FgGreen).Printf("✓")
		fmt.Printf(" %s\n", journalFile)
	}
	return nil
}
