package main

import (
	"strings"
	"testing"
)

func TestConsolidate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "no if-blocks",
			input: `skip 1
fields date, description, amount
account1 assets:checking
`,
			want: `skip 1
fields date, description, amount
account1 assets:checking
`,
		},
		{
			name: "no duplicates",
			input: `if PAYROLL
  account2 revenue:salary

if safeway
  account2 expenses:food:groceries
`,
			want: `if PAYROLL
  account2 revenue:salary

if safeway
  account2 expenses:food:groceries
`,
		},
		{
			name: "two blocks merged into one",
			input: `if safeway
  account2 expenses:food:groceries

if walmart
  account2 expenses:food:groceries
`,
			want: `if
safeway
walmart
  account2 expenses:food:groceries
`,
		},
		{
			name: "three blocks merged into one",
			input: `if safeway
  account2 expenses:food:groceries

if walmart
  account2 expenses:food:groceries

if target
  account2 expenses:food:groceries
`,
			want: `if
safeway
walmart
target
  account2 expenses:food:groceries
`,
		},
		{
			name: "multiple groups each consolidated independently",
			input: `if safeway
  account2 expenses:food:groceries

if PAYROLL
  account2 revenue:salary

if walmart
  account2 expenses:food:groceries

if DIRECT DEPOSIT
  account2 revenue:salary
`,
			want: `if
safeway
walmart
  account2 expenses:food:groceries

if
PAYROLL
DIRECT DEPOSIT
  account2 revenue:salary
`,
		},
		{
			name: "leading comment on first block is preserved; duplicate block comment dropped",
			input: `; grocery stores
if safeway
  account2 expenses:food:groceries

; another grocery
if walmart
  account2 expenses:food:groceries
`,
			want: `; grocery stores
if
safeway
walmart
  account2 expenses:food:groceries
`,
		},
		{
			name: "directives and preamble preserved",
			input: `skip 1
fields date, description, amount
date-format %Y-%m-%d
account1 assets:mybank:checking
account2 expenses:unknown

if PAYROLL
  account2 revenue:salary

if safeway
  account2 expenses:food:groceries

if walmart
  account2 expenses:food:groceries
`,
			want: `skip 1
fields date, description, amount
date-format %Y-%m-%d
account1 assets:mybank:checking
account2 expenses:unknown

if PAYROLL
  account2 revenue:salary

if
safeway
walmart
  account2 expenses:food:groceries
`,
		},
		{
			name: "multi-matcher block treated as one unit",
			input: `if
safeway
trader joe's
  account2 expenses:food:groceries

if walmart
  account2 expenses:food:groceries
`,
			want: `if
safeway
trader joe's
walmart
  account2 expenses:food:groceries
`,
		},
		{
			name: "multi-assignment blocks consolidated",
			input: `if amazon
  account2 expenses:shopping
  comment  online

if ebay
  account2 expenses:shopping
  comment  online
`,
			want: `if
amazon
ebay
  account2 expenses:shopping
  comment  online
`,
		},
		{
			name: "blocks with different assignments not merged",
			input: `if amazon
  account2 expenses:shopping
  comment  online

if ebay
  account2 expenses:shopping
`,
			want: `if amazon
  account2 expenses:shopping
  comment  online

if ebay
  account2 expenses:shopping
`,
		},
		{
			name: "trailing blank lines preserved",
			input: `if safeway
  account2 expenses:food:groceries

if walmart
  account2 expenses:food:groceries

`,
			want: `if
safeway
walmart
  account2 expenses:food:groceries

`,
		},
		{
			name: "assignment indentation preserved as-is",
			input: `if safeway
	account2 expenses:food:groceries

if walmart
	account2 expenses:food:groceries
`,
			want: `if
safeway
walmart
	account2 expenses:food:groceries
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			items := parseRulesItems([]byte(tc.input))
			items = consolidateIfBlocks(items)
			got := serializeRulesItems(items)
			if got != tc.want {
				t.Errorf("consolidate mismatch\ninput:\n%s\ngot:\n%s\nwant:\n%s",
					indent(tc.input), indent(got), indent(tc.want))
			}
		})
	}
}

// indent prefixes every line with "  " to make multi-line diffs readable.
func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}
