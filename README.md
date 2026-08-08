# email-cleanup

Batch Gmail operations, run from the command line instead of the Gmail UI.

## Setup

1. Install Go (see `go.mod` for the required version) and [Task](https://taskfile.dev/installation/).
2. Get a Gmail API OAuth "Desktop app" client:
   ```bash
   task run -- credentials-help
   ```
   Follow either the manual Google Cloud Console steps or the `gcloud`
   command list it prints, then save the downloaded JSON as
   `.credentials/credentials.json`.
3. Initialize the credentials directory (safe to run any time):
   ```bash
   task run -- init
   ```
4. Build the binary:
   ```bash
   task build
   ```

The first command that talks to Gmail opens a browser consent flow and
caches the resulting token at `.credentials/token.json`. `.credentials/` is
gitignored — never commit it.

## Usage

### Archive old inbox mail

```bash
# Dry run (default) — shows what would be archived, changes nothing
./bin/email-cleanup archive-old-mail --before=2026-08-01

# Actually archive matches
./bin/email-cleanup archive-old-mail --before=2026-08-01 --apply

# Try it on just the first 10 matches before running it on everything
./bin/email-cleanup archive-old-mail --before=2026-08-01 --apply --limit=10
```

`--before` accepts `YYYY-MM-DD` and matches Gmail's `in:inbox before:...`
search semantics. Re-running is safe — once a message is archived it no
longer matches `in:inbox`.

Gmail's API only returns message IDs from a search; getting each message's
Subject/From/Date (needed for the journal) takes one API call per message.
This tool fetches those concurrently (up to 15 at a time) instead of one at
a time, so large mailboxes search significantly faster — but very large
result sets (tens of thousands of messages) can still take a while, since
requests are throttled to stay under Gmail's default per-user API quota
(15,000 units/minute). If you still hit a quota error, the tool retries
with backoff automatically; if it happens repeatedly, you can request a
higher quota at
https://cloud.google.com/docs/quotas/help/request_increase.

`--limit` caps how many matching messages are processed (searched and,
with `--apply`, archived). Useful for testing on a small batch before
running the same command without `--limit` on the full result set.
Defaults to `0` (no limit).

### Machine-readable logs

Add `--json` to any command to switch logging from human-friendly text to
JSON lines on stderr:

```bash
./bin/email-cleanup archive-old-mail --before=2026-08-01 --json
```

### Journal

Every `archive-old-mail` run (dry-run or apply) appends records to
`.history/journal.jsonl` — one `run` record plus one `message` record per
affected message. This file is committed to git as an audit trail. After a
run that mutates the mailbox, commit and push it:

```bash
task commit-history
```

Known limitation: if an `--apply` run fails partway through archiving, the
journal's `run` record is written with `status: error` and
`affected_count: 0`, but because Gmail batch requests are chunked
internally, some messages may have already been archived on Gmail's side
before the failure — check Gmail directly if a run errors mid-archive.

## Development

```bash
task test    # run the test suite
task fmt     # gofmt
task vet     # go vet
task tidy    # go mod tidy
```

## Project Layout

Follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout):
- `cmd/email-cleanup/` — CLI entrypoint
- `internal/cmd/` — Cobra subcommands
- `internal/gmail/` — Gmail API client, OAuth2 auth
- `internal/journal/` — JSONL run/message journal
