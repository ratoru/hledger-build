package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// rulesItem is a parsed element from a hledger .rules file.
// Exactly one field is populated: either verbatim lines (directives, comments,
// blank lines) or a parsed if-block.
type rulesItem struct {
	verbatim []string
	block    *ifBlock
}

func newConsolidateCmd() *cobra.Command {
	var write bool
	cmd := &cobra.Command{
		Use:   "consolidate <rules-file>",
		Short: "Merge if-blocks with identical assignments in an hledger rules file",
		Long: `Consolidate reads the given hledger .rules file, finds all if-blocks that
share identical assignments (account2, comment, etc.), and merges their
matchers into a single if-block. The result is printed to stdout.

Example — before:

  if safeway
    account2 expenses:food:groceries

  if walmart
    account2 expenses:food:groceries

After:

  if
  safeway
  walmart
    account2 expenses:food:groceries

Use --write / -w to overwrite the file in place instead of printing to stdout.`,
		Args:         cobra.ExactArgs(1),
		RunE:         func(cmd *cobra.Command, args []string) error { return runConsolidate(args[0], write) },
		SilenceUsage: true,
	}
	cmd.Flags().BoolVarP(&write, "write", "w", false, "overwrite the rules file in place")
	return cmd
}

func runConsolidate(path string, write bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	items := parseRulesItems(data)
	items = consolidateIfBlocks(items)
	out := serializeRulesItems(items)

	if write {
		return os.WriteFile(path, []byte(out), 0o600)
	}
	_, err = fmt.Print(out)
	return err
}

// parseRulesItems parses a hledger .rules file into a flat slice of items.
//
// Blank lines and comment lines (;, #) that immediately precede an if-block
// are attached to that block as preComments so they travel with it during
// consolidation. Blank/comment lines that precede a regular directive are
// emitted as a separate verbatim item.
func parseRulesItems(data []byte) []rulesItem {
	sc := bufio.NewScanner(bytes.NewReader(data))
	var lines []string
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	var items []rulesItem
	var pending []string // blank/comment lines buffered until we know what follows

	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		isBlankOrComment := trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#")

		if isBlankOrComment {
			pending = append(pending, line)
			i++
			continue
		}

		if trimmed == "if" || strings.HasPrefix(trimmed, "if ") {
			// Pending blank/comment lines belong to this if-block.
			var block *ifBlock
			block, i = parseIfBlock(lines, i, pending)
			pending = nil
			items = append(items, rulesItem{block: block})
		} else {
			// Regular directive. Flush pending as a verbatim item, then this line.
			if len(pending) > 0 {
				items = append(items, rulesItem{verbatim: append([]string(nil), pending...)})
				pending = nil
			}
			items = append(items, rulesItem{verbatim: []string{line}})
			i++
		}
	}

	if len(pending) > 0 {
		items = append(items, rulesItem{verbatim: append([]string(nil), pending...)})
	}
	return items
}

// parseIfBlock parses a single if-block starting at lines[i], attaching
// preComments, and returns the block and the updated index.
func parseIfBlock(lines []string, i int, preComments []string) (*ifBlock, int) {
	block := &ifBlock{preComments: append([]string(nil), preComments...)}
	trimmed := strings.TrimSpace(lines[i])
	if pattern, ok := strings.CutPrefix(trimmed, "if "); ok {
		block.matchers = []string{strings.TrimSpace(pattern)}
	}
	i++

	// Continuation matchers: non-indented, non-blank/comment lines before the
	// first assignment.
	for i < len(lines) {
		l, lt := lines[i], strings.TrimSpace(lines[i])
		if len(l) > 0 && (l[0] == ' ' || l[0] == '\t') {
			break // hit an assignment
		}
		if lt == "" || strings.HasPrefix(lt, ";") || strings.HasPrefix(lt, "#") {
			break
		}
		if lt == "if" || strings.HasPrefix(lt, "if ") {
			break
		}
		block.matchers = append(block.matchers, lt)
		i++
	}

	// Assignments: indented lines.
	for i < len(lines) {
		l := lines[i]
		if len(l) == 0 || (l[0] != ' ' && l[0] != '\t') {
			break
		}
		block.assignments = append(block.assignments, l)
		i++
	}
	return block, i
}

// consolidateIfBlocks groups if-blocks by their canonical assignments and
// merges the matchers of duplicate blocks into the first occurrence.
// Duplicate blocks (and their attached preComments) are removed.
func consolidateIfBlocks(items []rulesItem) []rulesItem {
	type ref struct{ block *ifBlock }
	firstByKey := map[string]*ref{}

	for i := range items {
		b := items[i].block
		if b == nil {
			continue
		}
		key := rulesAssignmentsKey(b.assignments)
		if r, exists := firstByKey[key]; exists {
			r.block.matchers = append(r.block.matchers, b.matchers...)
			items[i] = rulesItem{} // remove this duplicate
		} else {
			firstByKey[key] = &ref{block: b}
		}
	}

	// Drop empty items (removed duplicates).
	out := items[:0]
	for _, item := range items {
		if item.block != nil || len(item.verbatim) > 0 {
			out = append(out, item)
		}
	}
	return out
}

// rulesAssignmentsKey returns a canonical string for a slice of assignment
// lines, used to detect if-blocks with identical bodies.
func rulesAssignmentsKey(assignments []string) string {
	parts := make([]string, len(assignments))
	for i, a := range assignments {
		parts[i] = strings.TrimSpace(a)
	}
	return strings.Join(parts, "\x00")
}

// serializeRulesItems writes items back to a string.
// Single-matcher if-blocks use the compact "if PATTERN" syntax; blocks with
// multiple matchers use the multi-line "if\nMATCHER\n..." form.
func serializeRulesItems(items []rulesItem) string {
	var sb strings.Builder
	for _, item := range items {
		if item.block != nil {
			b := item.block
			for _, l := range b.preComments {
				sb.WriteString(l)
				sb.WriteByte('\n')
			}
			if len(b.matchers) == 1 {
				sb.WriteString("if ")
				sb.WriteString(b.matchers[0])
				sb.WriteByte('\n')
			} else {
				sb.WriteString("if\n")
				for _, m := range b.matchers {
					sb.WriteString(m)
					sb.WriteByte('\n')
				}
			}
			for _, a := range b.assignments {
				sb.WriteString(a)
				sb.WriteByte('\n')
			}
		} else {
			for _, l := range item.verbatim {
				sb.WriteString(l)
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}
