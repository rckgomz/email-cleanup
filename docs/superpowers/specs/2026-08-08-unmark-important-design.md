# Design: `unmark-important` command

Date: 2026-08-08

## Purpose

The user has ~6,292 messages in Gmail marked as "Important" (Gmail's
auto-applied or user-applied `IMPORTANT` label) and wants a batch tool to
remove that marker, analogous to how `archive-old-mail` batch-removes the
`INBOX` label. This is the second command in the `email-cleanup` toolkit.

Removing the `IMPORTANT` label only unmarks messages as important — it does
**not** archive them. Messages stay exactly where they are (inbox or
elsewhere); only the importance marker is removed. Archiving, if wanted,
stays a separate step via the existing `archive-old-mail` command.

## Command

```
email-cleanup unmark-important [--before=YYYY-MM-DD] [--apply] [--limit=N]
```

- `--before` (optional, format `YYYY-MM-DD`): if given, restricts to
  messages older than this date, matching Gmail's `before:YYYY/MM/DD`
  search syntax. If omitted, all messages currently marked Important are
  targeted, matching the user's stated goal directly.
- `--apply` (bool, default `false`): dry-run by default, same convention as
  `archive-old-mail`. Without it, lists what would be unmarked and makes no
  changes.
- `--limit` (int, default `0` = no limit): caps how many matching messages
  are processed, same as `archive-old-mail`'s `--limit`. Included from the
  start given the ~6,292-message scale — useful for testing on a small
  batch before running unlimited.

Query built: `label:important` alone, or `label:important before:YYYY/MM/DD`
when `--before` is supplied.

## Architecture: generalize, don't duplicate

`archive-old-mail`'s search → dry-run/apply → journal-write → reminder flow
is ~150 lines of logic that `unmark-important` needs almost identically,
differing only in: which label gets removed, the verb used in journal
message-record `action` values, the `command` string recorded in journal
run-records, and the human-readable success message. Rather than
copy-pasting this into a second file (which would let the two commands
drift apart under future changes — e.g. if the journal schema changes),
generalize the shared logic into one parameterized core function used by
both commands.

### `internal/gmail` changes

- `Service` interface gains a generic method:
  ```go
  RemoveLabel(ctx context.Context, ids []string, label string) error
  ```
- `APIService.Archive` and the new `APIService.RemoveLabel` both become
  thin wrappers around one unexported helper:
  ```go
  func (a *APIService) modifyLabels(ctx context.Context, ids []string, labelID string) error
  ```
  which contains the existing chunking (≤1000 ids per `batchModify` call),
  rate limiting, and retry-with-backoff logic, unchanged from today's
  `Archive` implementation — just parameterized on which label ID to
  remove. `Archive(ctx, ids)` becomes
  `return a.modifyLabels(ctx, ids, "INBOX")`;
  `RemoveLabel(ctx, ids, label)` becomes
  `return a.modifyLabels(ctx, ids, label)`.
- No behavior change to existing `archive-old-mail` functionality — this is
  a pure refactor of `Archive`'s internals plus one new method.

### `internal/cmd` changes

- Generalize `doArchiveRun` (in `archive_old_mail.go`) into a shared core
  function, e.g.:
  ```go
  func doBatchLabelRun(
      ctx context.Context,
      svc gmail.Service,
      jrnl *journal.Journal,
      query string,
      args []string,
      apply bool,
      limit int,
      label string,          // e.g. "INBOX" or "IMPORTANT"
      commandName string,    // e.g. "archive-old-mail" or "unmark-important"
      actionVerb string,     // e.g. "archived" or "unmarked_important"
      dryRunVerb string,     // e.g. "would_archive" or "would_unmark_important"
      applySuccessMsg string, // e.g. "Archived %d message(s).\n"
      logger *slog.Logger,
      out io.Writer,
  ) error
  ```
  Exact parameter list is an implementation detail for the plan to nail
  down — the point is one function, not two near-duplicates.
- `archive_old_mail.go`'s `runArchiveOldMail` (the Cobra `RunE`) calls this
  shared function with `label="INBOX"`, `commandName="archive-old-mail"`,
  etc. — same external behavior as today, verified by the existing test
  suite continuing to pass.
- New file `internal/cmd/unmark_important.go`: Cobra command registration
  (`--before` optional this time, `--apply`, `--limit`), `buildImportantQuery`
  helper (mirrors `buildQuery` but optional cutoff), and `runUnmarkImportant`
  calling the shared core function with `label="IMPORTANT"`,
  `commandName="unmark-important"`, etc.
- New file `internal/cmd/unmark_important_test.go` mirroring
  `archive_old_mail_test.go`'s test structure, using the same
  `fakeGmailService` (extended with a `RemoveLabel` method, and tracking
  calls similarly to how `archiveCalls`/`archivedIDs` work today).

### Journal

Same `.history/journal.jsonl` file — no second journal. The existing
`RunRecord`/`MessageRecord` schema (from `internal/journal`) needs no
changes: `Command` field distinguishes `"archive-old-mail"` from
`"unmark-important"` entries, and `Action` field values differ
(`"unmarked_important"`/`"would_unmark_important"` vs
`"archived"`/`"would_archive"`) but the field itself already accepts any
string. One unified, chronological audit trail across both commands.

### Reminder

The existing `task commit-history` reminder text is generic ("Reminder: run
`task commit-history` to commit and push the updated journal.") and applies
unchanged to `unmark-important --apply` runs.

## Error handling

Same as `archive-old-mail`: missing `credentials.json` → clear error
pointing at `init`/`credentials-help`; Search/RemoveLabel failures write an
error-status journal run record (reusing the existing pattern) rather than
silently losing the audit trail; rate-limit errors retry with backoff via
the already-shared `gmail` package machinery.

## Testing

- `internal/gmail`: extend `fakeGmailServer`/`APIService` tests (or add
  focused new ones) to cover `RemoveLabel`/`modifyLabels` — at minimum,
  confirm it calls `batchModify` with the given label in `RemoveLabelIds`
  and reuses existing chunking/rate-limit/retry behavior (already covered
  by `Archive`'s tests once both share `modifyLabels`).
- `internal/cmd`: table-driven or duplicated-but-parallel test cases for
  `doBatchLabelRun` covering dry-run, apply, search error, remove-label
  error, no-matches, and limit — analogous to the existing
  `TestDoArchiveRun_*` suite, but exercised through both commands'
  thin wrappers (or directly against the shared function, implementer's
  call during planning).
- No live network calls in tests, consistent with the rest of the project.

## Out of Scope

- Combining unmark + archive in one pass (explicitly rejected — separate
  concerns, run `archive-old-mail` afterward if both are wanted).
- Any label other than `IMPORTANT`/`INBOX` (e.g. a fully generic
  "remove-label" command) — YAGNI until a third use case actually exists.
- Changing `archive-old-mail`'s external CLI behavior or flags — this
  spec's refactor must be behavior-preserving for that command.
