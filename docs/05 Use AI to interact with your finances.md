# How to use AI to classify your transactions

One of the big advantages of PTA is complete data privacy.

If you've gone through the trouble of downloading CSV files instead
of giving your credentials to Plaid and setting up a ledger, you care about
privacy. But, AI can massively speed up the classification process, allowing
you to set up and maintain your ledger with much less effort.

Here are effective ways I use AI with plain-text accounting.

## Review and Rule Creation (Recommended)

Use an open-source coding agent like [OpenCode](https://opencode.ai/docs) with
a local model on [Ollama](https://docs.ollama.com/integrations/opencode). Then,
you can provide a list of unknown transactions, existing accounts, and existing
rules to the agent and ask it to classify the rest.

## As part of the build process

You could add a `preprocess` step to your build process that uses an AI model
to classify transactions before they are added to the ledger.

1. Have the AI add another column to your CSV file with the suggested account.
   You could write a little script that passes in the CSV file and the exisitng
   accounts as context and adds the output to the CSV file under the columnd
   `hledger_target_account`.
2. Then, update your column mapping to include `hledger_target_account`.
   ```text
   fields date, description, amount, hledger_target_account
   ```
3. Add a conditional override to your `rules` file. Remember that later rules
   override earlier ones, so you can decide whether you want your other rules
   to take precedent or not.
   ```text
   # ── Target Column Override ────────────────────────────────────────────────────
   # If the CSV 'target_account' column contains at least one letter or number,
   # it means the column is not empty. Overwrite account2 with its value.
   if %hledger_target_account [a-zA-Z0-9]
    account2 %hledger_target_account
   ```

This will make builds take a lot longer, and be non-deterministic, so I don't recommend it. But, if you want to get fancy, you can do it.

---

AI moves fast, so these recommendations may be outdated by the time you read this.
