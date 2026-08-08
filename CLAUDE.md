# CLAUDE.md

Guidance for working in this repository.

## What this is

A Go CLI (`email-cleanup`) for batch Gmail operations, run locally instead
of through the Gmail UI. Commands: `archive-old-mail` (archives inbox
messages older than a given date) and `unmark-important` (removes the
Important marker from matching messages).

## Layout

Follows golang-standards/project-layout:
- `cmd/email-cleanup/main.go` — entrypoint, delegates to `internal/cmd.Execute()`
- `internal/cmd/` — one file per Cobra subcommand (`root.go`, `init.go`,
  `credentials_help.go`, `archive_old_mail.go`, `unmark_important.go`),
  plus `batch_label_run.go` holding the shared search/dry-run/journal core
  (`doBatchLabelRun`, `batchLabelOp`) both label-removal commands wrap.
- `internal/gmail/` — `Service` interface + `APIService` (real Gmail API
  implementation) in `client.go`; OAuth2 config/token handling in `auth.go`
- `internal/journal/` — append-only JSONL journal (`RunRecord`,
  `MessageRecord`)

## Conventions

- CLI: Cobra for commands, Viper for flag binding.
- Logging: `log/slog` only. Human-friendly text handler by default; the
  global `--json` flag switches to `slog.NewJSONHandler`. Set once in
  `RootCmd.PersistentPreRun`.
- Gmail access always goes through the `gmail.Service` interface
  (`internal/gmail/client.go`), never called directly from `internal/cmd`,
  so command logic can be unit-tested against a fake.
- Mutating commands default to dry-run; an explicit `--apply` flag is
  required to actually change the mailbox.
- `.credentials/` (OAuth client + cached token) is gitignored, never
  committed. `.history/journal.jsonl` is committed — it holds no secrets,
  only a record of actions and affected message metadata.
- Any command that mutates the mailbox prints a reminder to run
  `task commit-history` afterwards.

## Build / test

```bash
task build   # go build -o bin/email-cleanup ./cmd/email-cleanup
task test    # go test ./...
task fmt     # go fmt ./...
task vet     # go vet ./...
task tidy    # go mod tidy
task run -- <subcommand> [flags]
```

## Adding a new batch operation

If the new operation removes a Gmail label (like `archive-old-mail` and
`unmark-important` both do), it likely doesn't need new journal/dry-run
logic at all:

1. Add a new file in `internal/cmd/` defining a `batchLabelOp` (see
   `archive_old_mail.go`'s `archiveOp` or `unmark_important.go`'s
   `unmarkImportantOp`) and a thin `RunE`/`doXxxRun` pair that builds a
   query and calls `doBatchLabelRun` (`batch_label_run.go`) with that op.
2. Register the command on `RootCmd` in the new file's `init()`.

For an operation that isn't just "remove a label" (e.g. it needs a
different Gmail API capability, or different journal semantics):

1. Add a new file in `internal/cmd/`, separating the Cobra `RunE` wiring
   from a pure, testable `doXxxRun` function that takes a `gmail.Service`
   and a `*journal.Journal` — follow `doBatchLabelRun`'s structure as a
   reference even if you don't reuse it directly.
2. Extend `gmail.Service` in `internal/gmail/client.go` if the new
   operation needs a Gmail API capability not already exposed.
3. Write journal records for the new operation so it stays covered by the
   audit trail.
4. Register the command on `RootCmd` in the new file's `init()`.
