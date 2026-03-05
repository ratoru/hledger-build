# hledger-build

A fast, incremental build system for [hledger](https://hledger.org/) personal
finance workflows. It converts raw bank CSV exports into hledger journal files
and generates standard financial reports automatically. I designed it to be an
easy entry point for plain-text accounting newcomers.

If you're new to the world of personal finance tracking, there is quite a bit
to learn before `hledger-build` will make sense to you. Fortunately, the world
of plain-text accounting is full of generous people who have written detailed
guides on how to get started. I did my best to consolidate the information here
in one place for you.

I suggest reading my guides in the following order:

1. [Why track your finances?](<./docs/01 Track your finances.md>)
2. [Learn accounting principles](<./docs/02 Learn accounting principles.md>)
3. [Automate your accounting](<./docs/03 Automate your accounting.md>)
4. [How to use hledger-build](<./docs/04 How to use hledger-build.md>)
5. Then continue with my [guides](./docs/guides) for more specific how-tos,
   like fetching commodity prices or AI integration.

If you'd like to dive in deeper, I recommend checking out [Further Reading](#further-reading) below.

## Installation

Download a pre-built binary from the [releases page](https://github.com/ratoru/hledger-build/releases),
or install with Go:

```bash
go install github.com/ratoru/hledger-build/cmd/hledger-build@latest
```

This requires Go 1.26+ and places the binary in `~/go/bin/`. Make sure that directory is in your `PATH`.

## Quick Start

First, install the latest version of [hledger](https://hledger.org/install.html). [fzf](https://github.com/junegunn/fzf)
is also recommended but not required.

Then, run

```bash
# Scaffold a new project
hledger-build init
# Run the full pipeline
hledger-build
# Start categorizing transactions
hledger-build categorize
```

After updating `hledger-build`, run `hledger-build --force` to regenerate all files.

## Dev Setup

Please install

- hledger
- just
- [golangci-lint](https://golangci-lint.run/docs/welcome/install/local/)
- [pricehist](https://pypi.org/project/pricehist/)

## Further Reading

- [plaintextaccounting.org](https://plaintextaccounting.org) — community hub for plain-text accounting tools and workflows
- [hledger manual](https://hledger.org/hledger.html) — seriously, this is a good read!
- [Why plain-text accounting?](https://beancount.github.io/docs/command_line_accounting_in_context.html)
- [full-fledged-hledger](https://github.com/adept/full-fledged-hledger) — the multi-year, multi-account workflow that inspired hledger-build
- [hledger-flow](https://github.com/apauley/hledger-flow) — another automation tool for hledger workflows

## Credit

This project would not be possible without the following projects. I created
`hledger-build` to make PTA accessible to my friends, and I did not want to
write Haskell.

- [hledger](https://hledger.org/)
- [full-fledged-hledger](https://github.com/adept/full-fledged-hledger)
- [hledger-flow](https://github.com/apauley/hledger-flow)
