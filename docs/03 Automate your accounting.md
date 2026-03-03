# Why `hledger-build`?

Plain-text accounting is powerful, but getting started and staying organized
takes real effort. You need to wrangle CSV exports from banks, write conversion
rules, maintain journal files across years, and somehow keep it all consistent
as your financial life evolves. Most of this work isn't _accounting_ — it's
plumbing.

`hledger-build` handles the plumbing. It gives you a reproducible pipeline that
converts raw bank statements into journals and reports, so you can focus on the
parts that actually matter: reviewing your finances and making decisions.

If you're new to double-entry accounting, read the
[Understand accounting principles](<./Understand\ accounting\ principles.md>) first.

## Core Principles

### Version Control Everything

Your entire financial history — source files, conversion scripts, journals, and
reports — lives in version control. This gives you a verifiable chain from raw
bank exports to final reports: every transformation is reproducible and
auditable.

More practically, it means you can experiment freely. Rename accounts,
restructure files, or rewrite conversion rules knowing you can always review or
revert changes.

### Refactor Fearlessly

Because the pipeline is automated and version-controlled, you can change any
part of the system without risk:

- **Bank changes its CSV format?** Update your conversion rules, regenerate
  historical journals, and diff the reports to confirm nothing shifted.
- **Want finer-grained expense categories?** Tweak your rules, regenerate, and
  review — no risky search-and-replace across finalized files.
- **Reorganizing your account hierarchy?** Make the change, rebuild, and verify
  the reports still add up.

### Automate Report Generation

Every time you rebuild, reports are regenerated automatically. This keeps the
feedback loop tight: make a change, see the effect immediately.

### Minimize Manual Entries

Manual data entry is slow and error-prone — in my experience it takes more
effort than managing the rest of the system combined. Automate everything you
can from electronic statements and reserve manual entries for the exceptions.
Not every transaction needs fine-grained tracking either. In my setup, all ATM
withdrawals go under `expenses:misc:cash`. Unless the total becomes
significant, I don't invest time detailing each one. I'd recommend a similar
approach, especially if you don't handle large amounts of cash.

### Start Simple, Add Detail Over Time

Begin with the accounts and sources that matter most, and expand from there.

For example, say you pay off a credit card from your main bank account each
month. Initially, your bank statement conversion might produce something like:

```ledger
2024-03-01 Credit Card Payment
    expenses:credit card           1,200.00 EUR
    assets:bank:checking          -1,200.00 EUR
```

That's fine to start with — it captures the cash flow. Later, when you start
importing the credit card's own statements, you can restructure this. The
bank-side transaction becomes a transfer to a liability account, and the credit
card statement fills in the actual spending:

```ledger
; From bank statement — pays off the credit card balance
2024-03-01 Credit Card Payment
    liabilities:credit card:balance payments   1,200.00 EUR
    assets:bank:checking                      -1,200.00 EUR

; From credit card statement — individual charges
2024-02-12 Supermarket
    expenses:food:groceries           45.30 EUR
    liabilities:credit card          -45.30 EUR

2024-02-15 Electric Company
    expenses:housing:utilities       120.00 EUR
    liabilities:credit card         -120.00 EUR

; From credit card statement — payment received
2024-03-01 Credit Card Payment
    liabilities:credit card                    1,200.00 EUR
    liabilities:credit card:balance payments  -1,200.00 EUR
```

The same approach works for Amazon, PayPal, pensions, or brokerage accounts —
start with broad categories, refine as you go. With everything under version
control, adding a new source means regenerating journals and confirming the
aggregate reports still make sense.

### Split Journals by Year

Yearly journal files keep builds fast and give you natural boundaries for
closing out income and expense accounts. Both `hledger` and `ledger` provide
`close`/`equity` commands to carry forward asset and liability balances into a
new year.

### Intuitive Layout and Conventions

`hledger-build` aims to be easy to pick up and highly configurable. It
establishes a small set of conventions — where source files go, how journals
are organized, how reports are generated — that are simple enough to remember
without checking the docs every time. The goal is a setup where the common
workflow (drop in new statements, rebuild, review, commit) requires minimal
thought.

At the same time, almost everything is configurable: conversion rules, account
mappings, report formats, and the build pipeline itself can all be adapted to
your needs. The conventions are sensible defaults, not constraints.

For a detailed walkthrough of the directory structure and workflow, see [How to use hledger-build](./How%20to%20use%20hledger-build.md).
