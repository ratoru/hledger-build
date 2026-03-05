package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/fatih/color"
	"github.com/ratoru/hledger-build/internal/config"
	"github.com/spf13/cobra"
)

// txn represents a single unknown transaction discovered from a report.
type txn struct {
	date        string
	description string
	amount      string
}

// ifBlock represents a single if-block in a categorization rules file.
type ifBlock struct {
	preComments []string // ";" or blank lines immediately before the `if` line
	matchers    []string // index 0: pattern on `if` line; rest: continuation lines
	assignments []string // indented lines: "  account2 ...", "  comment  ..."
}

func newCategorizeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "categorize",
		Short: "Interactively categorize expenses:unknown transactions using fzf.",
		Long: `Interactively categorize expenses:unknown transactions using fzf.

For each round, you will be prompted three times:

  1. Pattern  — type a grep regex that matches one or more raw CSV lines
               from the transactions you want to categorize (e.g. "amazon",
               "PAYROLL", "grocery|restaurant"). The preview updates live
               as you type so you can see which lines match.

  2. Account  — pick the target account from accounts.journal. Type to
               filter, then press Enter to confirm.

  3. Comment  — optionally attach a comment to the rule. Press Enter to
               skip.

After each round the rule is written to categorization.rules (which is
included in main.rules automatically), and the remaining unknown
transactions are recounted. The loop repeats until none remain.

Requires fzf to be installed and available in PATH.`,
		RunE:         func(cmd *cobra.Command, args []string) error { return runCategorize(cmd.Context()) },
		SilenceUsage: true,
	}
}

func runCategorize(ctx context.Context) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if _, err := exec.LookPath("fzf"); err != nil {
		return errors.New("fzf not found in PATH — install fzf to use 'categorize'\n  https://github.com/junegunn/fzf")
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if err := checkHledgerVersion(ctx, cfg.HledgerBinary); err != nil {
		return err
	}

	src, err := selectSource(ctx, cfg)
	if err != nil {
		return err
	}

	absSourceDir := filepath.Join(cfg.ProjectRoot, cfg.Directories.Sources, filepath.FromSlash(src))
	absMainRules := filepath.Join(absSourceDir, "main.rules")
	absCleanedDir := filepath.Join(absSourceDir, cfg.Directories.Cleaned)

	accounts, err := loadDeclaredAccounts(cfg.ProjectRoot)
	if err != nil {
		_, _ = color.New(color.FgYellow).Fprintf(os.Stderr, "warning: could not load accounts.journal: %v\n", err)
		accounts = []string{}
	}

	unknownTxns, err := collectSourceUnknownTxns(ctx, cfg, absCleanedDir, absMainRules)
	if err != nil {
		return err
	}

	dateFormat, err := parseDateFormat(absMainRules)
	if err != nil {
		return err
	}

	displayRows, err := findCSVRowsForTxns(unknownTxns, absCleanedDir, dateFormat)
	if err != nil {
		return err
	}

	tmpDir, err := os.MkdirTemp("", "hledger-build-*")
	if err != nil {
		return fmt.Errorf("creating temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if len(displayRows) == 0 {
		_, _ = color.New(color.FgGreen).Fprint(os.Stderr, "✓")
		fmt.Println(" No unknown transactions found — nothing to categorize.")
		return nil
	}

	categorizationRules := filepath.Join(absSourceDir, "categorization.rules")

	displayFile := filepath.Join(tmpDir, "display.csv")
	if err := writeLines(displayFile, displayRows); err != nil {
		return err
	}

	for {
		plural := "s"
		if len(displayRows) == 1 {
			plural = ""
		}
		patternHeader := fmt.Sprintf(
			"%d unknown transaction%s remaining\n%s\nWrite an hledger matcher regex. Example: ama?zo?n",
			len(displayRows),
			plural,
			strings.Repeat("─", 3),
		)

		grepReload := "reload(grep --color=always -iE -- {q} " + shellEscape(displayFile) + " || echo '(no matches)')"

		result, fzfErr := fzfRun(ctx, []string{
			"--disabled",
			"--ansi",
			"--print-query",
			"--bind=change:" + grepReload,
			"--border-label= Type a pattern to match these transactions (grep regex, preview updates live) ",
			"--header=" + patternHeader,
		}, strings.Join(displayRows, "\n"))
		if fzfErr != nil {
			return fzfErr
		}

		matcher := strings.TrimSpace(strings.SplitN(result, "\n", 2)[0])
		if matcher == "" {
			return errors.New("no matcher entered")
		}
		account, err := fzfPickDeclaredAccount(ctx, accounts, matcher)
		if err != nil {
			return err
		}

		comment, err := fzfPickCategorizationComment(ctx, absSourceDir, matcher, account)
		if err != nil {
			return err
		}

		if err := writeCategorizationRule(categorizationRules, matcher, account, comment); err != nil {
			return fmt.Errorf("writing categorization rule: %w", err)
		}
		if err := appendIfAbsent(absMainRules, "include categorization.rules"); err != nil {
			_, _ = color.New(color.FgYellow).
				Fprintf(os.Stderr, "warning: could not add 'include categorization.rules': %v\n", err)
		}
		_, _ = color.New(color.FgGreen).Fprintln(os.Stderr, "✓ Rule saved. Recounting…")

		count, err := recountUnknowns(ctx, cfg, absCleanedDir, absMainRules)
		if err != nil {
			_, _ = color.New(color.FgYellow).Fprintf(os.Stderr, "warning: recounting unknowns: %v\n", err)
		}
		if count == 0 {
			break
		}

		unknownTxns, err = collectSourceUnknownTxns(ctx, cfg, absCleanedDir, absMainRules)
		if err != nil {
			return err
		}
		displayRows, err = findCSVRowsForTxns(unknownTxns, absCleanedDir, dateFormat)
		if err != nil {
			return err
		}
		if err := writeLines(displayFile, displayRows); err != nil {
			return err
		}
	}

	_, _ = color.New(color.FgGreen).Fprint(os.Stderr, "✓")
	fmt.Printf(" All transactions categorized!\n  Rules saved to: %s\n", categorizationRules)
	return nil
}

// ── Source selection ───────────────────────────────────────────────────────────

// selectSource returns the sole discovered source or lets the user pick via fzf.
func selectSource(ctx context.Context, cfg *config.Config) (string, error) {
	if len(cfg.DiscoveredSources) == 0 {
		return "", errors.New("no sources discovered; add CSV files under sources/ first")
	}
	if len(cfg.DiscoveredSources) == 1 {
		return cfg.DiscoveredSources[0], nil
	}
	result, err := fzfRun(ctx, []string{
		"--border-label= Select source ",
		"--header=Multiple sources found — pick one to categorize",
	}, strings.Join(cfg.DiscoveredSources, "\n"))
	if err != nil {
		return "", err
	}
	result = strings.TrimSpace(result)
	if result == "" {
		return "", errors.New("no source selected")
	}
	return result, nil
}

// ── Account loading ────────────────────────────────────────────────────────────

// loadDeclaredAccounts reads projectRoot/accounts.journal and returns the
// account names declared with "account <name>" directives.
func loadDeclaredAccounts(projectRoot string) ([]string, error) {
	accountsPath := filepath.Join(projectRoot, "accounts.journal")
	data, err := os.ReadFile(accountsPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var accounts []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if i := strings.Index(line, " ;"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		name, ok := strings.CutPrefix(line, "account ")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name != "" {
			accounts = append(accounts, name)
		}
	}
	return accounts, nil
}

// ── Unknown transaction collection ────────────────────────────────────────────

// collectSourceUnknownTxns runs hledger print on every cleaned CSV file with
// the current main rules and returns deduplicated unknown transactions.
// Using the cleaned CSV + rules (rather than pre-generated journal files) ensures
// that newly written categorization rules are reflected immediately.
func collectSourceUnknownTxns(ctx context.Context, cfg *config.Config, cleanedDir, mainRules string) ([]txn, error) {
	csvFiles, err := walkGlob(cleanedDir, "*.csv")
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	var txns []txn
	for _, f := range csvFiles {
		ft, runErr := hledgerPrintUnknown(ctx, cfg.HledgerBinary, f, mainRules)
		if runErr != nil {
			_, _ = color.New(color.FgYellow).Fprintf(os.Stderr, "warning: %s: %v\n", f, runErr)
			continue
		}
		for _, t := range ft {
			key := t.date + "\x00" + t.description + "\x00" + t.amount
			if _, ok := seen[key]; !ok {
				seen[key] = struct{}{}
				txns = append(txns, t)
			}
		}
	}
	return txns, nil
}

// hledgerPrintUnknown runs "hledger print expenses:unknown -O csv" on a cleaned
// CSV file with the given rules file and returns the parsed transactions.
func hledgerPrintUnknown(ctx context.Context, binary, csvPath, mainRules string) ([]txn, error) {
	out, err := exec.CommandContext(ctx, binary,
		"-f", csvPath, "--rules", mainRules, "print", "expenses:unknown", "-O", "csv",
	).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, nil // no matches is not an error
		}
		return nil, err
	}
	return parsePrintCSV(out)
}

// parsePrintCSV parses the CSV output of "hledger print -O csv" and returns
// the transactions. Column positions are discovered from the header row so the
// code stays correct if hledger ever reorders its output columns.
func parsePrintCSV(data []byte) ([]txn, error) {
	r := csv.NewReader(bytes.NewReader(data))
	header, err := r.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, err
	}
	cols := csvColIndex(header)
	dateCol, hasDate := cols["date"]
	descCol, hasDesc := cols["description"]
	amtCol, hasAmt := cols["amount"]
	if !hasDate || !hasDesc || !hasAmt {
		return nil, fmt.Errorf("hledger print CSV missing expected columns (got: %v)", header)
	}
	need := max(dateCol, descCol, amtCol) + 1

	var txns []txn
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if len(rec) < need {
			continue
		}
		txns = append(txns, txn{date: rec[dateCol], description: rec[descCol], amount: rec[amtCol]})
	}
	return txns, nil
}

// csvColIndex returns a map from column name to 0-based index for a CSV header row.
func csvColIndex(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, name := range header {
		m[name] = i
	}
	return m
}

// ── Date format helpers ────────────────────────────────────────────────────────

// parseDateFormat reads the main.rules file and extracts the date-format
// directive. Returns "%Y-%m-%d" if the directive is absent.
func parseDateFormat(mainRulesPath string) (string, error) {
	data, err := os.ReadFile(mainRulesPath)
	if os.IsNotExist(err) {
		return "%Y-%m-%d", nil
	}
	if err != nil {
		return "", err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if fmtStr, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "date-format "); ok {
			return strings.TrimSpace(fmtStr), nil
		}
	}
	return "%Y-%m-%d", nil
}

// convertDateToFormat converts an ISO date string (2006-01-02) to the given
// strftime-style format.
func convertDateToFormat(isoDate, format string) (string, error) {
	t, err := time.Parse("2006-01-02", isoDate)
	if err != nil {
		return "", err
	}
	return t.Format(strftimeToGo(format)), nil
}

// strftimeToGo converts a strftime format string to a Go time layout.
func strftimeToGo(sfmt string) string {
	return strings.NewReplacer(
		"%Y", "2006",
		"%m", "01",
		"%d", "02",
		"%H", "15",
		"%M", "04",
		"%S", "05",
	).Replace(sfmt)
}

// ── CSV row lookup ─────────────────────────────────────────────────────────────

// findCSVRowsForTxns matches each unknown transaction to a raw CSV line in the
// cleaned directory and returns the matching lines deduplicated.
func findCSVRowsForTxns(txns []txn, cleanedDir, dateFormat string) ([]string, error) {
	csvFiles, err := walkGlob(cleanedDir, "*.csv")
	if err != nil {
		return nil, err
	}

	seen := map[string]struct{}{}
	var rows []string
	for _, t := range txns {
		csvDate, convErr := convertDateToFormat(t.date, dateFormat)
		if convErr != nil {
			continue
		}
		for _, f := range csvFiles {
			line, found, readErr := findCSVLineForTxn(f, csvDate, t.description, t.amount)
			if readErr != nil || !found {
				continue
			}
			if _, ok := seen[line]; !ok {
				seen[line] = struct{}{}
				rows = append(rows, line)
			}
			break
		}
	}
	return rows, nil
}

// findCSVLineForTxn scans a CSV file for a line containing all three values.
func findCSVLineForTxn(csvPath, date, description, amount string) (string, bool, error) {
	data, err := os.ReadFile(csvPath)
	if err != nil {
		return "", false, err
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, date) &&
			strings.Contains(line, description) &&
			strings.Contains(line, amount) {
			return line, true, nil
		}
	}
	return "", false, nil
}

// ── categorization.rules parser/writer ────────────────────────────────────────

// parseCategorizationRules parses a categorization.rules file and returns its
// preamble lines and if-blocks. Returns nil, nil, nil if the file does not exist.
func parseCategorizationRules(path string) ([]string, []ifBlock, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}

	var preamble []string
	var blocks []ifBlock
	var pending []string
	seenIf := false
	inAssignments := false
	var cur ifBlock

	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		isIndented := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
		isBlankorComment := trimmed == "" || strings.HasPrefix(trimmed, ";")

		if trimmed == "if" || strings.HasPrefix(trimmed, "if ") {
			if seenIf && len(cur.matchers) > 0 {
				blocks = append(blocks, cur)
			}
			seenIf = true
			inAssignments = false
			cur = ifBlock{preComments: pending}
			if pattern, ok := strings.CutPrefix(trimmed, "if "); ok {
				cur.matchers = []string{pattern}
			}
			pending = nil
			continue
		}

		if !seenIf {
			preamble = append(preamble, line)
			continue
		}

		if isIndented {
			cur.assignments = append(cur.assignments, line)
			inAssignments = true
			continue
		}

		if isBlankorComment {
			pending = append(pending, line)
			if inAssignments {
				blocks = append(blocks, cur)
				cur = ifBlock{}
				inAssignments = false
			}
			continue
		}

		if !inAssignments {
			// Continuation matcher line.
			cur.matchers = append(cur.matchers, trimmed)
		}
	}

	if seenIf && len(cur.matchers) > 0 {
		blocks = append(blocks, cur)
	}
	return preamble, blocks, nil
}

// writeCategorizationRules serialises preamble and blocks back to a rules file.
func writeCategorizationRules(path string, preamble []string, blocks []ifBlock) error {
	var sb strings.Builder
	for _, line := range preamble {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	for i, b := range blocks {
		if i > 0 && len(b.preComments) == 0 {
			sb.WriteByte('\n')
		}
		for _, line := range b.preComments {
			sb.WriteString(line)
			sb.WriteByte('\n')
		}
		sb.WriteString("if\n")
		for _, m := range b.matchers {
			sb.WriteString(m)
			sb.WriteByte('\n')
		}
		for _, a := range b.assignments {
			sb.WriteString(a)
			sb.WriteByte('\n')
		}
	}
	return os.WriteFile(path, []byte(sb.String()), 0o600)
}

// writeCategorizationRule appends or merges a new rule into the categorization
// rules file at path. If a block with identical assignments already exists, the
// matcher is appended to it; otherwise a new block is created.
func writeCategorizationRule(path, matcher, account, comment string) error {
	assignments := []string{"  account2 " + account}
	if comment != "" {
		assignments = append(assignments, "  comment  "+comment)
	}

	preamble, blocks, err := parseCategorizationRules(path)
	if err != nil {
		return err
	}

	if preamble == nil && blocks == nil {
		preamble = []string{"; This file was created by hledger-build. Feel free to edit.", ""}
	}

	for i, b := range blocks {
		if slices.Equal(b.assignments, assignments) {
			blocks[i].matchers = append(blocks[i].matchers, matcher)
			return writeCategorizationRules(path, preamble, blocks)
		}
	}

	blocks = append(blocks, ifBlock{matchers: []string{matcher}, assignments: assignments})
	return writeCategorizationRules(path, preamble, blocks)
}

// ── Unknown recount ────────────────────────────────────────────────────────────

// recountUnknowns runs hledger against each cleaned CSV file with the current
// main rules and returns the number of unique unknown (date, description) pairs.
func recountUnknowns(ctx context.Context, cfg *config.Config, cleanedDir, mainRules string) (int, error) {
	csvFiles, err := walkGlob(cleanedDir, "*.csv")
	if err != nil {
		return 0, err
	}

	seen := map[string]struct{}{}
	for _, csvFile := range csvFiles {
		out, runErr := exec.CommandContext(ctx, cfg.HledgerBinary,
			"-f", csvFile,
			"--rules", mainRules,
			"print", "expenses:unknown",
			"-O", "csv",
		).Output()
		if runErr != nil {
			continue
		}
		r := csv.NewReader(bytes.NewReader(out))
		header, err := r.Read()
		if err != nil {
			continue
		}
		cols := csvColIndex(header)
		dateCol, hasDate := cols["date"]
		descCol, hasDesc := cols["description"]
		if !hasDate || !hasDesc {
			continue
		}
		need := max(dateCol, descCol) + 1
		for {
			rec, err := r.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil || len(rec) < need {
				continue
			}
			seen[rec[dateCol]+"\x00"+rec[descCol]] = struct{}{} // date + description
		}
	}
	return len(seen), nil
}

// ── fzf pickers ───────────────────────────────────────────────────────────────

// fzfPickDeclaredAccount presents accounts from accounts.journal and returns the
// user's selection. It re-prompts if the typed query is not a known account.
func fzfPickDeclaredAccount(ctx context.Context, accounts []string, matcher string) (string, error) {
	input := strings.Join(accounts, "\n")
	header := "Matched pattern: " + matcher + "\n" +
		strings.Repeat("─", 3) + "\n" +
		"Select an account from accounts.journal (type to filter)"
	for {
		result, err := fzfRun(ctx, []string{
			"--print-query",
			"--border-label= Pick target account ",
			"--header=" + header,
		}, input)
		if err != nil {
			return "", err
		}
		lines := strings.SplitN(result, "\n", 2)
		query := strings.TrimSpace(lines[0])
		if len(lines) >= 2 && strings.TrimSpace(lines[1]) != "" {
			return strings.TrimSpace(lines[1]), nil
		}
		if slices.Contains(accounts, query) {
			return query, nil
		}
		_, _ = color.New(color.FgYellow).
			Fprintf(os.Stderr, "Account %q not found in accounts.journal; add it there first, then retry.\n", query)
	}
}

// fzfPickCategorizationComment presents existing comments for reuse and lets the
// user type a new one. Returns an empty string if no comment is desired.
func fzfPickCategorizationComment(ctx context.Context, absSourceDir, matcher, account string) (string, error) {
	comments := collectCategorizationComments(filepath.Join(absSourceDir, "categorization.rules"))
	header := "Matched pattern: " + matcher + "  →  Account: " + account + "\n" +
		strings.Repeat("─", 3) + "\n" +
		"Select an existing comment, type a new one, or press Enter to skip"
	result, err := fzfRun(ctx, []string{
		"--print-query",
		"--bind=enter:accept-or-print-query",
		"--border-label= Add comment (optional) ",
		"--header=" + header,
	}, strings.Join(comments, "\n"))
	if err != nil {
		return "", err
	}
	lines := strings.SplitN(result, "\n", 2)
	if len(lines) >= 2 && strings.TrimSpace(lines[1]) != "" {
		return strings.TrimSpace(lines[1]), nil
	}
	return strings.TrimSpace(lines[0]), nil
}

// collectCategorizationComments returns unique non-internal comment values from
// a categorization.rules file.
func collectCategorizationComments(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var comments []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		after, ok := strings.CutPrefix(line, "comment")
		if !ok {
			continue
		}
		val := strings.TrimSpace(after)
		if val == "" || strings.HasPrefix(val, "__HLB_") {
			continue
		}
		if _, dup := seen[val]; !dup {
			seen[val] = struct{}{}
			comments = append(comments, val)
		}
	}
	return comments
}

// ── fzf runner ────────────────────────────────────────────────────────────────

// fzfRun runs fzf with the given args, piping input to its stdin, and returns
// the trimmed stdout.
//
// Exit code 1 (no match) returns the output buffer as-is, which is useful when
// --print-query is active. Exit code 130 (Ctrl-C) returns an error.
func fzfRun(ctx context.Context, args []string, input string) (string, error) {
	var buf bytes.Buffer
	cmd := exec.CommandContext(ctx, "fzf", args...)
	cmd.Stdin = strings.NewReader(input)
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr

	runErr := cmd.Run()
	output := strings.TrimRight(buf.String(), "\n")

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			switch exitErr.ExitCode() {
			case 1: // no match — still return whatever fzf wrote (query if --print-query)
				return output, nil
			case 130: // SIGINT / Ctrl-C
				return "", errors.New("categorize: cancelled by user")
			}
		}
		return "", runErr
	}
	return output, nil
}

// ── Filesystem helpers ─────────────────────────────────────────────────────────

// walkGlob returns all files under dir whose base name matches pattern.
// Returns nil, nil if dir does not exist.
func walkGlob(dir, pattern string) ([]string, error) {
	var matches []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		matched, matchErr := filepath.Match(pattern, filepath.Base(path))
		if matchErr != nil {
			return matchErr
		}
		if matched {
			matches = append(matches, path)
		}
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return matches, err
}

// writeLines writes lines joined by newlines to path, creating or truncating it.
func writeLines(path string, lines []string) error {
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

// shellEscape wraps s in single quotes, escaping embedded single quotes.
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
