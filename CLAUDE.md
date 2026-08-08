# CLAUDE.md

Guidance for working in this repository.

## What this is

A Go CLI (`email-cleanup`) for batch Gmail operations, run locally instead
of through the Gmail UI. First command: `archive-old-mail`, which archives
inbox messages older than a given date.

## Layout

Follows golang-standards/project-layout:
- `cmd/email-cleanup/main.go` — entrypoint, delegates to `internal/cmd.Execute()`
- `internal/cmd/` — one file per Cobra subcommand (`root.go`, `init.go`,
  `credentials_help.go`, `archive_old_mail.go`)
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

1. Add a new file in `internal/cmd/` for the subcommand, following the
   pattern in `archive_old_mail.go` (separate the Cobra `RunE` wiring from
   a pure, testable `doXxxRun` function that takes a `gmail.Service` and a
   `*journal.Journal`).
2. Extend `gmail.Service` in `internal/gmail/client.go` if the new
   operation needs a Gmail API capability not already exposed.
3. Write journal records for the new operation so it stays covered by the
   audit trail.
4. Register the command on `RootCmd` in the new file's `init()`.
