# Budget your finances

The [budget report](https://hledger.org/1.51/hledger.html#budget-report) compares your actual spending against goals you define with
periodic transaction rules. It shows performance as a percentage, and hides
accounts that have no budget goal.

This is _goal-based budgeting_: you set targets for specific accounts and
periods, then review how closely you hit them. It contrasts with envelope
budgeting (stricter, more work).

## Defining budget goals

Budget goals are written as periodic transaction rules (`~`) in a journal file.
The natural place is a file in `_manual_`, since goals are hand-written and not
imported from a bank.

Create `sources/_manual_/{year}/budget.journal`:

```ledger
; sources/_manual_/2026/budget.journal

;; Monthly spending goals
~ monthly in 2026
    (expenses:food)                $400
    (expenses:transportation)       $80
    (expenses:housing:rent)       $1500
    (expenses:entertainment)       $100
```

The parentheses make these _unbalanced_ postings — they exist only to define
the goal amounts and don't affect your account balances.

Include the file from your year journal:

```ledger
; 2026.journal
include commodities.journal
include accounts.journal
include reports/2026-opening.journal
include sources/_manual_/2026/budget.journal
include sources/2026-imports.journal
```

## Enabling the report

The budget report is disabled by default. Enable it in `hledger-build.toml`:

```toml
[reports.budget]
enabled = true
# args    = ["bal", "--budget", "--monthly", "--no-elide"]
```

Then, read the docs for more information on how to customize the report.
