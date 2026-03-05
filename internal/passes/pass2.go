package passes

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/ratoru/hledger-build/internal/config"
	"github.com/ratoru/hledger-build/internal/discovery"
	"github.com/ratoru/hledger-build/internal/runner"
	"github.com/ratoru/hledger-build/internal/varsubst"
)

// GeneratePass2Steps generates all report and balance steps (Pass 2).
//
// For each year in [FirstYear, CurrentYear] it produces:
//   - One step per enabled built-in hledger report.
//   - A closing-balance step for every year except the current one.
//   - An opening-balance step for every year except the first one.
//
// Custom reports are appended last. GetIncludes is called on each
// {year}.journal to build transitive file dependency lists; results are
// cached so each journal is only parsed once.
func GeneratePass2Steps(cfg *config.Config) ([]runner.Step, error) {
	if cfg.FirstYear == 0 || cfg.CurrentYear == 0 {
		return nil, nil
	}

	// yearDepsCache avoids redundant GetIncludes walks for the same journal.
	yearDepsCache := make(map[int][]string)

	getYearDeps := func(year int) ([]string, error) {
		if deps, ok := yearDepsCache[year]; ok {
			return deps, nil
		}
		journalPath := strconv.Itoa(year) + ".journal"
		deps, err := discovery.GetIncludes(cfg.ProjectRoot, journalPath)
		if err != nil {
			return nil, fmt.Errorf("getting includes for %d.journal: %w", year, err)
		}
		for i, d := range deps {
			deps[i] = filepath.ToSlash(d)
		}
		yearDepsCache[year] = deps
		return deps, nil
	}

	var steps []runner.Step

	for year := cfg.FirstYear; year <= cfg.CurrentYear; year++ {
		yearStr := strconv.Itoa(year)
		journalFile := yearStr + ".journal"

		yearDeps, err := getYearDeps(year)
		if err != nil {
			return nil, err
		}

		// reportDeps is yearDeps plus the generated opening journal for years
		// after the first. {year}.journal includes reports/{year}-opening.journal,
		// but that file doesn't exist on the first run so GetIncludes can't
		// discover it. We add it explicitly so all report steps wait for the
		// opening step before running.
		reportDeps := yearDeps
		if year > cfg.FirstYear {
			openingPath := cfg.Directories.Reports + "/" + yearStr + "-opening.journal"
			if !slices.Contains(yearDeps, openingPath) {
				reportDeps = append(append([]string(nil), yearDeps...), openingPath)
			}
		}

		// ── Built-in reports ──────────────────────────────────────────────

		type builtinDef struct {
			report config.BuiltinReport
			output string
		}
		builtins := []builtinDef{
			{cfg.Reports.Transactions, cfg.Directories.Reports + "/" + yearStr + "-all.journal"},
			{cfg.Reports.Accounts, cfg.Directories.Reports + "/" + yearStr + "-accounts.txt"},
			{cfg.Reports.IncomeStatement, cfg.Directories.Reports + "/" + yearStr + "-income-statement.txt"},
			{cfg.Reports.BalanceSheet, cfg.Directories.Reports + "/" + yearStr + "-balance-sheet.txt"},
			{cfg.Reports.Cashflow, cfg.Directories.Reports + "/" + yearStr + "-cashflow.txt"},
			{cfg.Reports.Unknown, cfg.Directories.Reports + "/" + yearStr + "-unknown.journal"},
		}

		for _, b := range builtins {
			if !b.report.Enabled {
				continue
			}
			args := make([]string, 0, 2+len(b.report.Args))
			args = append(args, "-f", journalFile)
			args = append(args, b.report.Args...)

			steps = append(steps, runner.Step{
				ID:            b.output,
				Output:        b.output,
				Deps:          reportDeps,
				Command:       cfg.HledgerBinary,
				Args:          args,
				CaptureStdout: true,
				Pass:          2,
			})
		}

		// ── Metrics report ────────────────────────────────────────────────
		if cfg.Reports.Metrics.Enabled && cfg.SelfBinary != "" {
			mc := cfg.Reports.Metrics
			metricsOutput := cfg.Directories.Reports + "/" + yearStr + "-metrics.txt"
			steps = append(steps, runner.Step{
				ID:      metricsOutput,
				Output:  metricsOutput,
				Deps:    reportDeps,
				Command: cfg.SelfBinary,
				Args: []string{
					"metrics",
					"--file", journalFile,
					"--year", yearStr,
					"--fire-factor", strconv.Itoa(mc.FireFactor),
					"--exclude-expenses", strings.Join(mc.Accounts.ExcludeExpenses, ","),
					"--exclude-revenue", strings.Join(mc.Accounts.ExcludeRevenue, ","),
					"--cash-assets", mc.Accounts.CashAssets,
					"--currency", mc.Currency,
					"--age", strconv.Itoa(mc.Age),
				},
				CaptureStdout: true,
				Pass:          2,
			})
		}

		// ── Closing balances (all years except current) ───────────────────

		if year < cfg.CurrentYear {
			closingOutput := cfg.Directories.Reports + "/" + yearStr + "-closing.journal"
			args := []string{
				"-f", journalFile,
				"equity", cfg.EquityQuery,
				"-e", strconv.Itoa(year + 1),
				"-I", "--closing",
			}
			steps = append(steps, runner.Step{
				ID:            closingOutput,
				Output:        closingOutput,
				Deps:          reportDeps,
				Command:       cfg.HledgerBinary,
				Args:          args,
				CaptureStdout: true,
				Pass:          2,
			})
		}

		// ── Opening balances (all years except first) ─────────────────────
		//
		// Opening for year Y reads from {Y-1}.journal, so its deps are the
		// prior year's includes plus the prior year's closing journal (which
		// must be built first, establishing the correct ordering in the DAG).

		if year > cfg.FirstYear {
			prevYearStr := strconv.Itoa(year - 1)
			prevJournalFile := prevYearStr + ".journal"

			prevYearDeps, err := getYearDeps(year - 1)
			if err != nil {
				return nil, err
			}

			openingOutput := cfg.Directories.Reports + "/" + yearStr + "-opening.journal"
			closingDep := cfg.Directories.Reports + "/" + prevYearStr + "-closing.journal"

			// Build opening deps: prior-year includes + explicit closing dep.
			openingDeps := make([]string, len(prevYearDeps), len(prevYearDeps)+1)
			copy(openingDeps, prevYearDeps)
			openingDeps = append(openingDeps, closingDep)

			args := []string{
				"-f", prevJournalFile,
				"equity", cfg.EquityQuery,
				"-e", yearStr,
				"--opening",
			}
			steps = append(steps, runner.Step{
				ID:            openingOutput,
				Output:        openingOutput,
				Deps:          openingDeps,
				Command:       cfg.HledgerBinary,
				Args:          args,
				CaptureStdout: true,
				Pass:          2,
			})
		}
	}

	// ── Custom reports ────────────────────────────────────────────────────

	customSteps, err := generateCustomReportSteps(cfg, getYearDeps)
	if err != nil {
		return nil, err
	}
	steps = append(steps, customSteps...)

	return steps, nil
}

// generateCustomReportSteps generates steps for all configured custom reports.
func generateCustomReportSteps(
	cfg *config.Config,
	getYearDeps func(int) ([]string, error),
) ([]runner.Step, error) {
	var steps []runner.Step
	for _, cr := range cfg.CustomReports {
		crSteps, err := customReportToSteps(cfg, cr, getYearDeps)
		if err != nil {
			return nil, fmt.Errorf("custom report %q: %w", cr.Name, err)
		}
		steps = append(steps, crSteps...)
	}
	return steps, nil
}

// customReportToSteps converts one CustomReport into one or more runner Steps.
//
// Four dispatch modes (checked in order):
//  1. years="all" + "{year}" in output  → one step per year.
//  2. years="all"                        → single step over all year deps.
//  3. year_range specified               → single step with from/to vars.
//  4. default                            → single step with depends_on only.
func customReportToSteps(
	cfg *config.Config,
	cr config.CustomReport,
	getYearDeps func(int) ([]string, error),
) ([]runner.Step, error) {
	reportsDir := cfg.Directories.Reports

	switch {
	// ── Mode 1: years="all" with per-year output ──────────────────────────
	case cr.Years == "all" && strings.Contains(cr.Output, "{year}"):
		var steps []runner.Step
		for year := cfg.FirstYear; year <= cfg.CurrentYear; year++ {
			yearStr := strconv.Itoa(year)
			vars := map[string]string{"year": yearStr}

			output := resolveCustomOutput(reportsDir, varsubst.Apply(cr.Output, vars))
			args := applyTemplateSlice(cr.Args, vars)

			deps, err := getYearDeps(year)
			if err != nil {
				return nil, err
			}
			deps = cloneSlice(deps)

			// dyngen: the output is included by the year journal but is
			// itself generated — exclude it from its own dep list.
			if cr.DynGen {
				deps = filterOut(deps, output)
			}

			steps = append(steps, runner.Step{
				ID:            output,
				Output:        output,
				Deps:          deps,
				Command:       cr.Script,
				Args:          args,
				CaptureStdout: true,
				Pass:          2,
			})
		}
		return steps, nil

	// ── Mode 2: years="all" with single output ───────────────────────────
	case cr.Years == "all":
		allDeps, err := unionYearDeps(cfg, getYearDeps)
		if err != nil {
			return nil, err
		}
		output := resolveCustomOutput(reportsDir, cr.Output)
		return []runner.Step{{
			ID:            output,
			Output:        output,
			Deps:          allDeps,
			Command:       cr.Script,
			Args:          cr.Args,
			CaptureStdout: true,
			Pass:          2,
		}}, nil

	// ── Mode 3: year_range ────────────────────────────────────────────────
	case len(cr.YearRange) > 0:
		fromYear, toYear, err := resolveYearRange(cr.YearRange, cfg.CurrentYear)
		if err != nil {
			return nil, err
		}

		fromStr := strconv.Itoa(fromYear)
		toStr := strconv.Itoa(toYear)
		vars := map[string]string{"from_year": fromStr, "to_year": toStr}

		output := resolveCustomOutput(reportsDir, varsubst.Apply(cr.Output, vars))
		args := applyTemplateSlice(cr.Args, vars)

		// Deps: union of all year includes in [fromYear, toYear].
		seen := make(map[string]bool)
		var deps []string
		for year := fromYear; year <= toYear; year++ {
			yDeps, err := getYearDeps(year)
			if err != nil {
				return nil, err
			}
			for _, d := range yDeps {
				if !seen[d] {
					seen[d] = true
					deps = append(deps, d)
				}
			}
		}

		// Explicit depends_on (template-expanded, resolved to project-root path).
		for _, dep := range cr.DependsOn {
			depPath := resolveReportDep(reportsDir, varsubst.Apply(dep, vars))
			if !seen[depPath] {
				seen[depPath] = true
				deps = append(deps, depPath)
			}
		}

		return []runner.Step{{
			ID:            output,
			Output:        output,
			Deps:          deps,
			Command:       cr.Script,
			Args:          args,
			CaptureStdout: true,
			Pass:          2,
		}}, nil

	// ── Mode 4: single step with depends_on only ─────────────────────────
	default:
		output := resolveCustomOutput(reportsDir, cr.Output)

		seen := make(map[string]bool)
		var deps []string
		for _, dep := range cr.DependsOn {
			depPath := resolveReportDep(reportsDir, dep)
			if !seen[depPath] {
				seen[depPath] = true
				deps = append(deps, depPath)
			}
		}

		return []runner.Step{{
			ID:            output,
			Output:        output,
			Deps:          deps,
			Command:       cr.Script,
			Args:          cr.Args,
			CaptureStdout: true,
			Pass:          2,
		}}, nil
	}
}

// unionYearDeps returns the ordered union of GetIncludes results for every
// year in [FirstYear, CurrentYear], deduplicating by path.
func unionYearDeps(cfg *config.Config, getYearDeps func(int) ([]string, error)) ([]string, error) {
	seen := make(map[string]bool)
	var deps []string
	for year := cfg.FirstYear; year <= cfg.CurrentYear; year++ {
		yDeps, err := getYearDeps(year)
		if err != nil {
			return nil, err
		}
		for _, d := range yDeps {
			if !seen[d] {
				seen[d] = true
				deps = append(deps, d)
			}
		}
	}
	return deps, nil
}

// resolveYearRange parses from_year and to_year from the config map,
// treating the string "current" as cfg.CurrentYear.
func resolveYearRange(yr map[string]string, currentYear int) (int, int, error) {
	fromStr, ok := yr["from_year"]
	if !ok {
		return 0, 0, errors.New("year_range missing from_year")
	}
	toStr, ok := yr["to_year"]
	if !ok {
		return 0, 0, errors.New("year_range missing to_year")
	}

	parse := func(s string) (int, error) {
		if s == "current" {
			return currentYear, nil
		}
		v, e := strconv.Atoi(s)
		if e != nil {
			return 0, fmt.Errorf("invalid year %q: %w", s, e)
		}
		return v, nil
	}

	from, err := parse(fromStr)
	if err != nil {
		return 0, 0, fmt.Errorf("from_year: %w", err)
	}
	to, err := parse(toStr)
	if err != nil {
		return 0, 0, fmt.Errorf("to_year: %w", err)
	}
	return from, to, nil
}

// resolveReportDep normalises a depends_on entry to a project-root-relative path.
// A "./" prefix means project-root-relative (the "./" is stripped).
// Everything else (bare name or subpath) is placed under the reports directory.
func resolveReportDep(reportsDir, dep string) string {
	if strings.HasPrefix(dep, "./") {
		return dep[2:]
	}
	return reportsDir + "/" + dep
}

// resolveCustomOutput normalises a custom report output to a project-root-relative path.
// A "./" prefix means project-root-relative (e.g. "./sources/prices/2026/USD.prices").
// Everything else (bare name or subpath) is placed under the reports directory.
func resolveCustomOutput(reportsDir, output string) string {
	if strings.HasPrefix(output, "./") {
		return output[2:]
	}
	return reportsDir + "/" + output
}

// applyTemplateSlice applies varsubst.Apply to each element of slice.
func applyTemplateSlice(slice []string, vars map[string]string) []string {
	out := make([]string, len(slice))
	for i, s := range slice {
		out[i] = varsubst.Apply(s, vars)
	}
	return out
}

// cloneSlice returns a shallow copy of s.
func cloneSlice(s []string) []string {
	c := make([]string, len(s))
	copy(c, s)
	return c
}

// filterOut returns a new slice with every occurrence of val removed.
func filterOut(slice []string, val string) []string {
	out := make([]string, 0, len(slice))
	for _, s := range slice {
		if s != val {
			out = append(out, s)
		}
	}
	return out
}
