# Design: Gmail Batch-Ops Toolkit — `archive-old-mail`

Date: 2026-08-07

## Purpose

The user wants to perform batch operations on their Gmail inbox (starting with
archiving old mail) faster/cheaper than doing it through the Gmail UI or
paying a coworker to do it manually. This is the first of several planned
batch tools sharing one codebase.

First operation: move all emails in the inbox older than a given date to
archive (i.e. remove the `INBOX` label).

## Stack

- Go, following the [golang-standards project-layout](https://github.com/golang-standards/project-layout)
- [Cobra](https://github.com/spf13/cobra) — CLI command structure
- [Viper](https://github.com/spf13/viper) — config/flag binding (flags now,
  env vars/config file available later without redesign)
- [Task](https://taskfile.dev) — build/dev task runner (replaces Makefile)
- `log/slog` (stdlib) — structured logging
- Gmail API (`google.golang.org/api/gmail/v1`) + `golang.org/x/oauth2/google`
  for auth

## Project Structure

```
email-cleanup/
  cmd/
    email-cleanup/
      main.go                  # entrypoint, calls internal/cmd.Execute()
  internal/
    cmd/
      root.go                   # cobra root command, viper binding, --json flag, slog setup
      init.go                    # `init` subcommand
      credentials_help.go         # `credentials-help` subcommand (also used by init)
      archive_old_mail.go          # `archive-old-mail` subcommand
    gmail/
      auth.go                      # OAuth2 flow, token cache load/save
      client.go                    # SearchMessages(), ArchiveMessages()
    journal/
      journal.go                    # JSONL writer/reader for run + per-message records
  .credentials/                      # gitignored; created by `init`
    credentials.json                  # OAuth client, user-provided (not committed)
    token.json                        # cached user token, generated (not committed)
  .history/
    journal.jsonl                     # committed; append-only run/message log
  .gitignore
  go.mod
  Taskfile.yml
  CLAUDE.md
  README.md
```

## Commands

### `email-cleanup init`

Creates `.credentials/` (mode 0700) if missing. Checks whether
`.credentials/credentials.json` is already present; if not, prints the same
setup instructions as `credentials-help` (see below). Idempotent — safe to
re-run.

### `email-cleanup credentials-help`

Pure informational command (no side effects, can be run any time) that prints
the steps to obtain `credentials.json` for a Gmail API OAuth Desktop client,
covering **two paths**:

1. **Manual (Google Cloud Console)**: create/select a GCP project, enable the
   Gmail API, configure the OAuth consent screen (Testing mode, add own
   account as test user), create an OAuth Client ID (Desktop app type),
   download the JSON, save as `.credentials/credentials.json`.
2. **`gcloud` CLI (copy-paste commands)**: equivalent steps as `gcloud`
   commands the user can run themselves — e.g. `gcloud projects create`,
   `gcloud services enable gmail.googleapis.com`, and the consent
   screen/OAuth client creation commands. These are printed as text for the
   user to copy and run manually; **the tool does not execute `gcloud`
   itself** (explicitly out of scope per user request).

### `email-cleanup archive-old-mail --before=YYYY-MM-DD [--apply]`

- `--before` (required): cutoff date. Builds Gmail search query
  `in:inbox before:YYYY/MM/DD`.
- `--apply` (bool, default `false`): without it, dry-run only — lists matched
  message count and details, makes no changes. With it, archives matches.

Flow:
1. Verify `.credentials/credentials.json` exists; if not, error telling the
   user to run `email-cleanup init` or `credentials-help`.
2. Load or obtain OAuth token (`.credentials/token.json`), refreshing or
   running the consent flow as needed. Scope: `gmail.modify`.
3. Paginate `users.messages.list` with the query, collecting all matches.
4. Dry run: log count and per-message summary (subject, date), write a
   journal `run` record with `dry_run: true` and `message` records with
   action `would_archive`. No API mutation.
5. Apply: batch-remove the `INBOX` label via `users.messages.batchModify` in
   chunks of ≤1000 ids (Gmail API limit). Log progress
   (`archived 250/1200`). Write journal `run` record with `dry_run: false`
   and `message` records with action `archived`. On completion, print the
   commit reminder (see Journal section below).

Idempotent: re-running after archiving is safe, since already-archived mail
no longer matches `in:inbox`.

## Logging

`log/slog` used throughout. Root command sets up the handler once in
`PersistentPreRun`:
- Default: human-friendly text handler to stderr (level, message, key=value
  pairs).
- `--json` global flag: switches to `slog.NewJSONHandler` for machine-readable
  output. Same log call sites, handler chosen once at startup.

## Journal

Append-only JSONL at `.history/journal.jsonl`, one JSON object per line, two
record kinds sharing a `run_id`:

- **`run` record**: `run_id`, `timestamp`, `command`, `args`, `query`,
  `dry_run`, `matched_count`, `affected_count`, `duration_ms`, `status`
  (`ok`/`error`).
- **`message` record**: `run_id`, `message_id`, `subject`, `from`, `date`,
  `action` (`archived` / `would_archive`).

Every invocation of `archive-old-mail` (dry-run or apply) writes to the
journal. This is durable, greppable/`jq`-able, and lays groundwork for a
future `history`/`undo` command without a storage redesign. `.history/` is
committed to git (not gitignored) — it holds no credentials or secrets, only
a record of actions taken and which messages were affected (subject, from,
date), so it's kept as a versioned audit trail.

**Commit reminder**: any command that produces an effect (mutates the mailbox
— e.g. `archive-old-mail --apply`; dry runs do not count, since they don't
change mailbox state, though they still append to the journal file) prints a
reminder after completing, pointing at a Task target that commits and pushes
the journal:

```
Reminder: run `task commit-history` to commit and push the updated journal.
```

### `task commit-history`

New Taskfile target that stages `.history/`, commits with an auto-generated
message (e.g. `Update journal: <run_id> (<n> messages archived)`), and pushes
to the current branch's remote. Not run automatically by the CLI itself —
the CLI only prints the reminder; committing/pushing stays a deliberate,
user-triggered step (consistent with never auto-pushing on the user's
behalf).

## Error Handling

- Missing `credentials.json` → clear error pointing to `init` /
  `credentials-help`.
- Auth/API errors → fail fast, log via slog, non-zero exit, still write a
  journal `run` record with `status: error` where possible.
- Gmail API errors during batch apply → log which chunk failed, stop (don't
  silently continue past partial failures), leave journal reflecting what
  actually succeeded.

## Testing

- Unit tests for query building (`internal/gmail`), journal read/write
  (`internal/journal`), and flag/date parsing.
- Gmail API calls are exercised via an interface so they can be faked/mocked
  in tests — no live API calls in the test suite.

## Out of Scope (for this spec)

- Automated `gcloud`-driven provisioning of the GCP project/OAuth client.
- A `history`/`undo` command reading the journal (future work, but journal
  format is designed to support it).
- Any operation beyond `archive-old-mail` (future cmd/ subcommands will
  follow the same pattern).
