package main

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ratoru/hledger-build/internal/config"
)

// txn represents a single unknown transaction discovered from a report.
type txn struct {
	date        string
	description string
	amount      string
}

func newCategorizeCmd() *cobra.Command {
	var unknownAccount string
	var yearsFlag string

	cmd := &cobra.Command{
		Use:   "categorize",
		Short: "Interactively categorize unknown transactions using fzf",
		Long: `categorize reads unknown transactions from reports/{year}-unknown.journal
and guides you through creating hledger classification rules in each source's
auto.rules file.

Each newly created auto.rules file is automatically included from the source's
main .rules file via "include auto.rules".

Requires fzf in PATH. Uses ripgrep (rg) when available, falling back to grep.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCategorize(unknownAccount, yearsFlag)
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&unknownAccount, "unknown-account", "unknown",
		"account query pattern for unknown transactions")
	cmd.Flags().StringVar(&yearsFlag, "years", "",
		"comma-separated years to process (default: all configured years)")
	return cmd
}

func runCategorize(unknownAccount, yearsFlag string) error {
	if _, err := exec.LookPath("fzf"); err != nil {
		return fmt.Errorf("fzf not found in PATH — install fzf to use 'categorize'\n  https://github.com/junegunn/fzf")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	if cfg.FirstYear == 0 || cfg.CurrentYear == 0 {
		return fmt.Errorf("no years configured or discovered; add CSV files under sources/ first")
	}

	years := buildCategorizeYearList(cfg, yearsFlag)
	if len(years) == 0 {
		return fmt.Errorf("no years to process")
	}

	// Collect hledger accounts for the account picker.
	accounts, err := collectHledgerAccounts(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not list hledger accounts: %v\n", err)
		accounts = []string{}
	}

	// Collect unknown transactions from all requested years.
	var allTxns []txn
	for _, year := range years {
		journalPath := filepath.Join(cfg.ProjectRoot, cfg.Directories.Reports,
			fmt.Sprintf("%d-unknown.journal", year))
		yt, err := collectUnknownTxns(cfg, journalPath, unknownAccount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %d-unknown.journal: %v\n", year, err)
			continue
		}
		allTxns = append(allTxns, yt...)
	}
	allTxns = deduplicateTxns(allTxns)

	// Load existing auto.rules patterns and filter already-handled transactions.
	patterns, err := loadAllAutoRulesPatterns(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: loading auto.rules patterns: %v\n", err)
	}
	allTxns = filterByPatterns(allTxns, patterns)

	if len(allTxns) == 0 {
		fmt.Println("No unknown transactions left!")
		return nil
	}
	fmt.Printf("Found %d unknown transaction(s) to categorize.\n", len(allTxns))

	for len(allTxns) > 0 {
		// Step 1: pick a transaction.
		sel, err := fzfPickTransaction(allTxns)
		if err != nil {
			return err
		}

		// Step 2: find which source owns this transaction.
		sourceDir, err := findSourceDir(cfg, sel.description)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: %v — skipping transaction\n", err)
			allTxns = removeTxn(allTxns, sel)
			continue
		}

		// Step 3: refine the matching regexp.
		pattern, err := fzfRefineRegexp(cfg, sourceDir, sel.description)
		if err != nil {
			return err
		}
		if strings.TrimSpace(pattern) == "" {
			pattern = sel.description
		}

		// Step 4: pick the target account.
		account, err := fzfPickAccount(accounts)
		if err != nil {
			return err
		}
		// A leading ':' signals a new account to add to the in-memory list.
		if trimmedAccount, found := strings.CutPrefix(account, ":"); found {
			accounts = append(accounts, trimmedAccount)
		}

		// Step 5: enter an optional comment.
		comment, err := fzfPickComment(cfg, sourceDir)
		if err != nil {
			return err
		}
		comment = strings.TrimPrefix(comment, ":")

		// Step 6: write the rule to auto.rules.
		if err := appendRuleHledger(cfg, sourceDir, pattern, account, comment); err != nil {
			return fmt.Errorf("writing rule: %w", err)
		}

		// Remove transactions that now match the new pattern.
		re := compileAutoPattern(pattern)
		if re != nil {
			allTxns = filterByPatterns(allTxns, []*regexp.Regexp{re})
		} else {
			allTxns = removeTxn(allTxns, sel)
		}
		if len(allTxns) == 0 {
			break
		}
		fmt.Printf("Rule saved. %d unknown transaction(s) remaining.\n", len(allTxns))
	}

	fmt.Println("No unknown transactions left!")
	return nil
}

// ── Year helpers ──────────────────────────────────────────────────────────────

func buildCategorizeYearList(cfg *config.Config, yearsFlag string) []int {
	if yearsFlag == "" {
		var years []int
		for y := cfg.FirstYear; y <= cfg.CurrentYear; y++ {
			years = append(years, y)
		}
		return years
	}
	var years []int
	for s := range strings.SplitSeq(yearsFlag, ",") {
		s = strings.TrimSpace(s)
		if y, err := strconv.Atoi(s); err == nil && y > 0 {
			years = append(years, y)
		}
	}
	return years
}

// ── hledger helpers ───────────────────────────────────────────────────────────

// collectHledgerAccounts runs "hledger accounts -f all.journal" and returns
// the account list.
func collectHledgerAccounts(cfg *config.Config) ([]string, error) {
	allJournal := filepath.Join(cfg.ProjectRoot, "all.journal")
	out, err := exec.Command(cfg.HledgerBinary, "accounts", "-f", allJournal).Output()
	if err != nil {
		return nil, err
	}
	var accounts []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			accounts = append(accounts, line)
		}
	}
	return accounts, nil
}

// collectUnknownTxns runs "hledger register -I -O csv" on journalPath filtered
// by unknownAccount and returns the resulting transactions.
func collectUnknownTxns(cfg *config.Config, journalPath, unknownAccount string) ([]txn, error) {
	if _, err := os.Stat(journalPath); err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	out, err := exec.Command(cfg.HledgerBinary,
		"-f", journalPath, "register", "-I", "-O", "csv", unknownAccount).Output()
	if err != nil {
		// hledger exits 1 when there are no matching postings — treat as empty.
		if _, ok := err.(*exec.ExitError); ok {
			return nil, nil
		}
		return nil, err
	}
	r := csv.NewReader(bytes.NewReader(out))
	// Skip header row.
	if _, err := r.Read(); err != nil {
		if err == io.EOF {
			return nil, nil
		}
		return nil, err
	}
	var txns []txn
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		// CSV columns: txnidx, date, date2, description, account, amount, total
		if len(rec) < 7 {
			continue
		}
		txns = append(txns, txn{
			date:        rec[1],
			description: rec[3],
			amount:      rec[5],
		})
	}
	return txns, nil
}

// ── Transaction filtering ─────────────────────────────────────────────────────

// deduplicateTxns removes transactions with identical (date, description) pairs.
func deduplicateTxns(txns []txn) []txn {
	seen := map[string]struct{}{}
	var result []txn
	for _, t := range txns {
		key := t.date + "\x00" + t.description
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, t)
	}
	return result
}

// filterByPatterns removes transactions whose description matches any pattern.
func filterByPatterns(txns []txn, patterns []*regexp.Regexp) []txn {
	if len(patterns) == 0 {
		return txns
	}
	var result []txn
	for _, t := range txns {
		matched := false
		for _, re := range patterns {
			if re.MatchString(t.description) {
				matched = true
				break
			}
		}
		if !matched {
			result = append(result, t)
		}
	}
	return result
}

// removeTxn removes the first txn equal to target.
func removeTxn(txns []txn, target txn) []txn {
	var result []txn
	for _, t := range txns {
		if t.date != target.date || t.description != target.description || t.amount != target.amount {
			result = append(result, t)
		}
	}
	return result
}

// ── Source lookup ─────────────────────────────────────────────────────────────

// findSourceDir returns the relative path (e.g. "sources/mybank/checking") for
// the source whose raw or cleaned CSV files contain description.
func findSourceDir(cfg *config.Config, description string) (string, error) {
	sourcesDir := filepath.Join(cfg.ProjectRoot, cfg.Directories.Sources)
	for _, src := range cfg.DiscoveredSources {
		for _, subdir := range []string{cfg.Directories.Raw, cfg.Directories.Cleaned} {
			searchDir := filepath.Join(sourcesDir, src, subdir)
			if _, err := os.Stat(searchDir); err != nil {
				continue
			}
			found, err := dirContainsString(searchDir, description)
			if err != nil {
				continue
			}
			if found {
				return filepath.Join(cfg.Directories.Sources, filepath.FromSlash(src)), nil
			}
		}
	}
	return "", fmt.Errorf("could not find source for transaction %q (searched raw/ and cleaned/ under all sources)", description)
}

// dirContainsString walks dir for *.csv files and returns true if any contains s.
func dirContainsString(dir, s string) (bool, error) {
	found := false
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".csv") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil // skip unreadable files
		}
		if strings.Contains(string(data), s) {
			found = true
			return io.EOF // sentinel: stop walking early
		}
		return nil
	})
	if err == io.EOF {
		return true, nil
	}
	return found, err
}

// ── auto.rules pattern loading ────────────────────────────────────────────────

// loadAllAutoRulesPatterns reads every source's auto.rules and returns a
// compiled regexp for each "if PATTERN" line.
func loadAllAutoRulesPatterns(cfg *config.Config) ([]*regexp.Regexp, error) {
	var patterns []*regexp.Regexp
	for _, src := range cfg.DiscoveredSources {
		autoRulesPath := filepath.Join(cfg.ProjectRoot, cfg.Directories.Sources, src, "auto.rules")
		data, err := os.ReadFile(autoRulesPath)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if after, ok := strings.CutPrefix(line, "if "); ok {
				re := compileAutoPattern(after)
				if re != nil {
					patterns = append(patterns, re)
				}
			}
		}
	}
	return patterns, nil
}

// compileAutoPattern compiles a case-insensitive regexp for an "if" pattern.
// Returns nil if the pattern is not a valid regexp.
func compileAutoPattern(pattern string) *regexp.Regexp {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return nil
	}
	return re
}

// ── auto.rules writer ─────────────────────────────────────────────────────────

// appendRuleHledger creates auto.rules if absent, ensures it is included from
// the source's primary .rules file, then appends the new rule block.
func appendRuleHledger(cfg *config.Config, sourceDir, pattern, account, comment string) error {
	absSourceDir := filepath.Join(cfg.ProjectRoot, sourceDir)
	autoRulesPath := filepath.Join(absSourceDir, "auto.rules")

	// Create auto.rules on first use.
	isNew := false
	if _, err := os.Stat(autoRulesPath); os.IsNotExist(err) {
		header := "# auto.rules — generated by hledger-build categorize\n"
		if err := os.WriteFile(autoRulesPath, []byte(header), 0o644); err != nil {
			return fmt.Errorf("creating auto.rules: %w", err)
		}
		isNew = true
	}

	// On first creation, wire "include auto.rules" into the primary rules file.
	if isNew {
		if err := addAutoRulesInclude(absSourceDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not add 'include auto.rules': %v\n", err)
		}
	}

	// Build rule block.
	var sb strings.Builder
	if comment != "" {
		fmt.Fprintf(&sb, "\n; %s\n", comment)
	} else {
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "if %s\n  account2 %s\n", pattern, account)

	f, err := os.OpenFile(autoRulesPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening auto.rules for append: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = f.WriteString(sb.String())
	return err
}

// addAutoRulesInclude appends "include auto.rules" to main.rules in sourceDir,
// unless already present. Returns an error if main.rules does not exist.
func addAutoRulesInclude(sourceDir string) error {
	mainRules := filepath.Join(sourceDir, "main.rules")
	if _, err := os.Stat(mainRules); err != nil {
		return fmt.Errorf("main.rules not found in %s: add 'include auto.rules' manually", sourceDir)
	}
	return appendIfAbsent(mainRules, "include auto.rules")
}

// collectAutoRulesComments returns unique comment strings found in auto.rules.
// It treats any line starting with "; " as a comment.
func collectAutoRulesComments(autoRulesPath string) []string {
	data, err := os.ReadFile(autoRulesPath)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var comments []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if after, ok := strings.CutPrefix(line, "; "); ok && after != "" {
			if _, dup := seen[after]; !dup {
				seen[after] = struct{}{}
				comments = append(comments, after)
			}
		}
	}
	return comments
}

// ── fzf helpers ───────────────────────────────────────────────────────────────

// fzfRun runs fzf with the given args, piping input to its stdin, and returns
// the trimmed stdout.
//
// Exit code 1 (no match) returns the output buffer as-is, which is useful when
// --print-query is active. Exit code 130 (Ctrl-C) returns an error.
func fzfRun(args []string, input string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr

	runErr := cmd.Run()
	output := strings.TrimRight(buf.String(), "\n")

	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1: // no match — still return whatever fzf wrote (query if --print-query)
				return output, nil
			case 130: // SIGINT / Ctrl-C
				return "", fmt.Errorf("categorize: cancelled by user")
			}
		}
		return "", runErr
	}
	return output, nil
}

// fzfPickTransaction presents the list and returns the selected txn.
func fzfPickTransaction(txns []txn) (txn, error) {
	var sb strings.Builder
	sb.WriteString("DATE\tDESCRIPTION\tAMOUNT\n")
	for _, t := range txns {
		fmt.Fprintf(&sb, "%s\t%s\t%s\n", t.date, t.description, t.amount)
	}

	label := fmt.Sprintf("Resolving Unknowns (%d) — 1/4", len(txns))
	result, err := fzfRun([]string{
		"--header-lines=1",
		"--delimiter=\t",
		"--nth=2",
		"--tac",
		"--border-label=" + label,
	}, sb.String())
	if err != nil {
		return txn{}, err
	}
	if result == "" {
		return txn{}, fmt.Errorf("no transaction selected")
	}
	parts := strings.SplitN(result, "\t", 3)
	if len(parts) < 3 {
		return txn{}, fmt.Errorf("unexpected fzf output: %q", result)
	}
	return txn{
		date:        strings.TrimSpace(parts[0]),
		description: strings.TrimSpace(parts[1]),
		amount:      strings.TrimSpace(parts[2]),
	}, nil
}

// fzfRefineRegexp opens fzf in interactive-grep mode over the source's rules
// files, pre-filled with description, and returns the refined query.
func fzfRefineRegexp(cfg *config.Config, sourceDir, description string) (string, error) {
	absSourceDir := filepath.Join(cfg.ProjectRoot, sourceDir)

	// Collect all .rules files in the source directory.
	entries, _ := os.ReadDir(absSourceDir)
	var rulesFiles []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".rules") {
			rulesFiles = append(rulesFiles, shellEscape(filepath.Join(absSourceDir, e.Name())))
		}
	}
	sort.Strings(rulesFiles)

	var reloadCmd string
	if len(rulesFiles) == 0 {
		if runtime.GOOS == "windows" {
			reloadCmd = "echo (no rules files found)"
		} else {
			reloadCmd = "echo '(no rules files found)'"
		}
	} else {
		filesStr := strings.Join(rulesFiles, " ")
		var nullDev, orTrue string
		if runtime.GOOS == "windows" {
			nullDev = "2>nul"
			orTrue = "|| echo."
		} else {
			nullDev = "2>/dev/null"
			orTrue = "|| true"
		}
		if _, err := exec.LookPath("rg"); err == nil {
			reloadCmd = fmt.Sprintf("rg -n -- {q} %s %s %s", filesStr, nullDev, orTrue)
		} else {
			reloadCmd = fmt.Sprintf("grep -n -- {q} %s %s %s", filesStr, nullDev, orTrue)
		}
	}

	result, err := fzfRun([]string{
		"--disabled",
		"--print-query",
		"--query=" + description,
		"--bind=start:reload:" + reloadCmd,
		"--bind=change:reload:" + reloadCmd,
		"--border-label=Refine regexp (matches shown from rules files) — 2/4",
		"--header=Edit regexp above; Enter to confirm",
	}, "")
	if err != nil {
		return "", err
	}
	// --print-query always outputs the query as the first line.
	lines := strings.SplitN(result, "\n", 2)
	return strings.TrimSpace(lines[0]), nil
}

// fzfPickAccount lets the user pick an existing account or type a new one
// (prefix with ':' to create a new account).
func fzfPickAccount(accounts []string) (string, error) {
	sorted := make([]string, len(accounts))
	copy(sorted, accounts)
	sort.Strings(sorted)

	result, err := fzfRun([]string{
		"--print-query",
		"--border-label=Pick account — 3/4",
		"--header=Select existing or type ':new:account:name' to add",
	}, strings.Join(sorted, "\n"))
	if err != nil {
		return "", err
	}
	if result == "" {
		return "", fmt.Errorf("no account entered")
	}
	// --print-query: first line = query, second = selection (if any).
	lines := strings.SplitN(result, "\n", 2)
	if len(lines) >= 2 && strings.TrimSpace(lines[1]) != "" {
		return strings.TrimSpace(lines[1]), nil
	}
	return strings.TrimSpace(lines[0]), nil
}

// fzfPickComment lets the user type an optional comment or reuse an existing one
// from auto.rules.
func fzfPickComment(cfg *config.Config, sourceDir string) (string, error) {
	autoRulesPath := filepath.Join(cfg.ProjectRoot, sourceDir, "auto.rules")
	comments := collectAutoRulesComments(autoRulesPath)

	result, err := fzfRun([]string{
		"--print-query",
		"--bind=enter:accept-or-print-query",
		"--border-label=Enter comment (optional) — 4/4",
		"--header=Type a comment or press Enter to skip",
	}, strings.Join(comments, "\n"))
	if err != nil {
		return "", err
	}
	// --print-query: first line = query, second = selection (if any).
	lines := strings.SplitN(result, "\n", 2)
	if len(lines) >= 2 && strings.TrimSpace(lines[1]) != "" {
		return strings.TrimSpace(lines[1]), nil
	}
	return strings.TrimSpace(lines[0]), nil
}

// shellEscape wraps s in single quotes, escaping embedded single quotes.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
