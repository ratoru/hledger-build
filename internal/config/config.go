package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/viper"
)

// Directories holds configurable directory name overrides.
type Directories struct {
	Sources string `mapstructure:"sources"`
	Reports string `mapstructure:"reports"`
	Raw     string `mapstructure:"raw"`
	Cleaned string `mapstructure:"cleaned"`
	Journal string `mapstructure:"journal"`
	Build   string `mapstructure:"build"`
	Prices  string `mapstructure:"prices"`
	Manual  string `mapstructure:"manual"`
}

// Pipeline describes one stage of the ingest pipeline.
type Pipeline struct {
	Name      string `mapstructure:"name"`
	Script    string `mapstructure:"script"`
	InputDir  string `mapstructure:"input_dir"`
	InputGlob string `mapstructure:"input_glob"`
	OutputDir string `mapstructure:"output_dir"`
	OutputExt string `mapstructure:"output_ext"`
	Optional  bool   `mapstructure:"optional"`
}

// ExtraStage is an additional pipeline stage spliced into the default pipeline
// for a specific source.
type ExtraStage struct {
	Name      string `mapstructure:"name"`
	Script    string `mapstructure:"script"`
	After     string `mapstructure:"after"`
	InputDir  string `mapstructure:"input_dir"`
	InputGlob string `mapstructure:"input_glob"`
	OutputDir string `mapstructure:"output_dir"`
	OutputExt string `mapstructure:"output_ext"`
}

// SourceOverride holds per-source deviations from the default pipeline.
type SourceOverride struct {
	Pipeline    []Pipeline   `mapstructure:"pipeline"`
	ExtraStages []ExtraStage `mapstructure:"extra_stages"`
	ExtraDeps   []string     `mapstructure:"extra_deps"`
}

// BuiltinReport describes a standard hledger report generated per year.
type BuiltinReport struct {
	Args    []string `mapstructure:"args"`
	Enabled bool     `mapstructure:"enabled"`
}

// MetricsAccounts holds account query strings used by the metrics report.
type MetricsAccounts struct {
	ExcludeExpenses []string `mapstructure:"exclude_expenses"`
	ExcludeIncome   []string `mapstructure:"exclude_income"`
	CashAssets      string   `mapstructure:"cash_assets"`
}

// MetricsReport configures the built-in monthly financial metrics report.
type MetricsReport struct {
	Enabled    bool            `mapstructure:"enabled"`
	FireFactor int             `mapstructure:"fire_factor"`
	Age        int             `mapstructure:"age"`
	Currency   string          `mapstructure:"currency"`
	Accounts   MetricsAccounts `mapstructure:"accounts"`
}

// CustomReport describes a user-defined report with an arbitrary script.
type CustomReport struct {
	Name      string            `mapstructure:"name"`
	Output    string            `mapstructure:"output"`
	Script    string            `mapstructure:"script"`
	Years     string            `mapstructure:"years"`
	Args      []string          `mapstructure:"args"`
	DynGen    bool              `mapstructure:"dyngen"`
	YearRange map[string]string `mapstructure:"year_range"`
	DependsOn []string          `mapstructure:"depends_on"`
}

// Reports holds the built-in report configurations.
type Reports struct {
	Transactions   BuiltinReport `mapstructure:"transactions"`
	Accounts       BuiltinReport `mapstructure:"accounts"`
	IncomeExpenses BuiltinReport `mapstructure:"income_expenses"`
	BalanceSheet   BuiltinReport `mapstructure:"balance_sheet"`
	Cashflow       BuiltinReport `mapstructure:"cashflow"`
	Unknown        BuiltinReport `mapstructure:"unknown"`
	Metrics        MetricsReport `mapstructure:"metrics"`
}

// Config is the root configuration struct.
type Config struct {
	// Overrideable via config file or CLI flags
	FirstYear     int    `mapstructure:"first_year"`
	CurrentYear   int    `mapstructure:"current_year"`
	HledgerBinary string `mapstructure:"hledger_binary"`
	EquityQuery   string `mapstructure:"equity_query"`
	Jobs          int    `mapstructure:"jobs"`

	// CLI-only flags (not stored in config file)
	Force    bool
	DryRun   bool
	Verbose  bool
	Quiet    bool
	FailFast bool

	Directories   Directories               `mapstructure:"directories"`
	Pipeline      []Pipeline                `mapstructure:"pipeline"`
	Sources       map[string]SourceOverride `mapstructure:"sources"`
	Reports       Reports                   `mapstructure:"reports"`
	CustomReports []CustomReport            `mapstructure:"custom_reports"`

	// Computed at load time (not from config file)
	ProjectRoot       string
	DiscoveredSources []string
	SelfBinary        string
}

// defaultPipeline returns the built-in default ingest pipeline.
func defaultPipeline() []Pipeline {
	return []Pipeline{
		{
			Name:      "preprocess",
			Script:    "./preprocess",
			InputDir:  "raw",
			InputGlob: "*.csv",
			OutputDir: "cleaned",
			OutputExt: ".csv",
			Optional:  true,
		},
		{
			Name:      "convert",
			Script:    "hledger",
			InputDir:  "cleaned",
			InputGlob: "*.csv",
			OutputDir: "journal",
			OutputExt: ".journal",
			Optional:  false,
		},
	}
}

// defaultReports returns built-in report definitions with their default args.
func defaultReports() Reports {
	return Reports{
		Transactions:   BuiltinReport{Args: []string{"print"}, Enabled: true},
		Accounts:       BuiltinReport{Args: []string{"accounts"}, Enabled: true},
		IncomeExpenses: BuiltinReport{Args: []string{"is", "--flat", "--no-elide", "--cost"}, Enabled: true},
		BalanceSheet:   BuiltinReport{Args: []string{"balancesheet", "--no-elide"}, Enabled: true},
		Cashflow:       BuiltinReport{Args: []string{"cashflow", "not:desc:(opening balances)", "--no-elide"}, Enabled: true},
		Unknown:        BuiltinReport{Args: []string{"print", "unknown"}, Enabled: true},
	}
}

// Load reads configuration from path (or searches for hledger-build.toml in cwd
// if path is empty), applies defaults, and discovers sources/years/rules.
func Load(path string) (*Config, error) {
	v := viper.New()

	// Set defaults
	v.SetDefault("hledger_binary", "hledger")
	v.SetDefault("equity_query", "assets|liabilities|debts")
	v.SetDefault("jobs", 0)
	v.SetDefault("directories.sources", "sources")
	v.SetDefault("directories.reports", "reports")
	v.SetDefault("directories.raw", "raw")
	v.SetDefault("directories.cleaned", "cleaned")
	v.SetDefault("directories.journal", "journal")
	v.SetDefault("directories.build", ".build")
	v.SetDefault("directories.prices", "sources/prices")
	v.SetDefault("directories.manual", "_manual_")
	// Reports are enabled by default; users must explicitly set enabled=false to disable.
	v.SetDefault("reports.transactions.enabled", true)
	v.SetDefault("reports.accounts.enabled", true)
	v.SetDefault("reports.income_expenses.enabled", true)
	v.SetDefault("reports.balance_sheet.enabled", true)
	v.SetDefault("reports.cashflow.enabled", true)
	v.SetDefault("reports.unknown.enabled", true)
	v.SetDefault("reports.metrics.enabled", true)
	v.SetDefault("reports.metrics.fire_factor", 25)
	v.SetDefault("reports.metrics.accounts.exclude_expenses", []string{"expenses:gross"})
	v.SetDefault("reports.metrics.accounts.exclude_income", []string{"income:gift"})
	v.SetDefault("reports.metrics.accounts.cash_assets", "assets:cash")

	var projectRoot string
	if path != "" {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolving config path: %w", err)
		}
		projectRoot = filepath.Dir(abs)
		v.SetConfigFile(abs)
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getting working directory: %w", err)
		}
		projectRoot = cwd
		v.AddConfigPath(cwd)
		v.SetConfigName("hledger-build")
		v.SetConfigType("toml")
	}

	// Read config — missing file is OK (zero-config happy path)
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading config: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	cfg.ProjectRoot = projectRoot

	// Apply default pipeline if none specified
	if len(cfg.Pipeline) == 0 {
		cfg.Pipeline = defaultPipeline()
	}

	// Apply default args for any report that doesn't have custom args.
	// Only the Args field is updated so that an explicit enabled=false in the
	// config is never silently overwritten.
	defaults := defaultReports()
	if len(cfg.Reports.Transactions.Args) == 0 {
		cfg.Reports.Transactions.Args = defaults.Transactions.Args
	}
	if len(cfg.Reports.Accounts.Args) == 0 {
		cfg.Reports.Accounts.Args = defaults.Accounts.Args
	}
	if len(cfg.Reports.IncomeExpenses.Args) == 0 {
		cfg.Reports.IncomeExpenses.Args = defaults.IncomeExpenses.Args
	}
	if len(cfg.Reports.BalanceSheet.Args) == 0 {
		cfg.Reports.BalanceSheet.Args = defaults.BalanceSheet.Args
	}
	if len(cfg.Reports.Cashflow.Args) == 0 {
		cfg.Reports.Cashflow.Args = defaults.Cashflow.Args
	}
	if len(cfg.Reports.Unknown.Args) == 0 {
		cfg.Reports.Unknown.Args = defaults.Unknown.Args
	}

	// Resolve jobs default
	if cfg.Jobs <= 0 {
		cfg.Jobs = runtime.NumCPU()
	}

	// Discover sources
	sources, err := discoverSources(projectRoot, cfg.Directories)
	if err != nil {
		return nil, fmt.Errorf("discovering sources: %w", err)
	}
	cfg.DiscoveredSources = sources

	// Discover year range if not set in config
	if cfg.FirstYear == 0 || cfg.CurrentYear == 0 {
		first, current, err := discoverYears(projectRoot, cfg.Directories, sources)
		if err != nil {
			return nil, fmt.Errorf("discovering years: %w", err)
		}
		if cfg.FirstYear == 0 {
			cfg.FirstYear = first
		}
		if cfg.CurrentYear == 0 {
			cfg.CurrentYear = current
		}
	}

	return &cfg, nil
}

// discoverSources walks the sources directory for subdirs containing a raw/ dir.
// Returns source names as paths relative to sources/.
// The special _manual_ directory is skipped.
func discoverSources(projectRoot string, dirs Directories) ([]string, error) {
	sourcesDir := filepath.Join(projectRoot, dirs.Sources)
	manualDir := dirs.Manual

	if _, err := os.Stat(sourcesDir); os.IsNotExist(err) {
		return nil, nil
	}

	var sources []string
	err := filepath.WalkDir(sourcesDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(sourcesDir, path)
		if err != nil {
			return err
		}

		// Skip the sources root itself
		if rel == "." {
			return nil
		}

		// Skip any path containing the manual directory component
		for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
			if part == manualDir {
				return filepath.SkipDir
			}
		}

		// Check if this directory contains a raw/ subdirectory
		rawDir := filepath.Join(path, dirs.Raw)
		if info, err := os.Stat(rawDir); err == nil && info.IsDir() {
			sources = append(sources, filepath.ToSlash(rel))
			return filepath.SkipDir // don't recurse into raw/
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(sources)
	return sources, nil
}

// discoverYears scans raw/{year}/, sources/_manual_/{year}/, and prices/{year}/
// for 4-digit subdirectory names and returns the min and max years found.
func discoverYears(projectRoot string, dirs Directories, sources []string) (first, current int, err error) {
	yearSet := map[int]struct{}{}

	collectYears := func(dir string) error {
		if _, statErr := os.Stat(dir); os.IsNotExist(statErr) {
			return nil
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return readErr
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			y, parseErr := strconv.Atoi(e.Name())
			if parseErr != nil || len(e.Name()) != 4 {
				continue
			}
			yearSet[y] = struct{}{}
		}
		return nil
	}

	// Scan raw/{year}/ for each discovered source
	for _, src := range sources {
		rawDir := filepath.Join(projectRoot, dirs.Sources, src, dirs.Raw)
		if err = collectYears(rawDir); err != nil {
			return
		}
	}

	// Scan sources/_manual_/{year}/
	manualDir := filepath.Join(projectRoot, dirs.Sources, dirs.Manual)
	if err = collectYears(manualDir); err != nil {
		return
	}

	// Scan prices/{year}/
	pricesDir := filepath.Join(projectRoot, dirs.Prices)
	if err = collectYears(pricesDir); err != nil {
		return
	}

	if len(yearSet) == 0 {
		return 0, 0, nil
	}

	for y := range yearSet {
		if first == 0 || y < first {
			first = y
		}
		if y > current {
			current = y
		}
	}
	return
}

// DiscoverRulesFiles returns all *.rules files from sources/ down to the source
// directory, sorted alphabetically within each level (broadest first).
func DiscoverRulesFiles(projectRoot string, dirs Directories, sourceName string) ([]string, error) {
	sourcesDir := filepath.Join(projectRoot, dirs.Sources)

	// Build list of directory levels from sourcesDir down to sourceDir
	levels := []string{sourcesDir}
	rel := filepath.FromSlash(sourceName)
	parts := strings.Split(rel, string(filepath.Separator))
	current := sourcesDir
	for _, part := range parts {
		current = filepath.Join(current, part)
		levels = append(levels, current)
	}
	// Remove duplicate: last level is sourceDir, already appended; remove sourcesDir duplicate
	// Actually levels[0]=sourcesDir, levels[len-1]=sourceDir — that's correct.

	var result []string
	for _, dir := range levels {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var levelFiles []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if strings.HasSuffix(e.Name(), ".rules") {
				abs := filepath.Join(dir, e.Name())
				rel, err := filepath.Rel(projectRoot, abs)
				if err != nil {
					return nil, err
				}
				levelFiles = append(levelFiles, filepath.ToSlash(rel))
			}
		}
		sort.Strings(levelFiles)
		result = append(result, levelFiles...)
	}

	return result, nil
}
