# Design: `mark-read` command

Date: 2026-08-08

## Purpose

The user has ~3,258 unread messages in Gmail's "Updates" category and wants
a batch tool to mark them read, analogous to how `archive-old-mail` and
`unmark-important` batch-remove `INBOX`/`IMPORTANT`. This is the third
command in the `email-cleanup` toolkit.

Marking a message "read" in Gmail is removing its `UNREAD` label — no other
change happens. Messages keep their location, labels, and importance
marker; only the unread marker is removed.

## Command

```
email-cleanup mark-read [--category=updates] [--before=YYYY-MM-DD] [--apply] [--limit=N]
```

- `--category` (string, default `"updates"`): which Gmail category to
  target. Validated against Gmail's known category names — `primary`,
  `social`, `promotions`, `updates`, `forums`, `personal` — rejecting
  anything else with a clear error before any API call, the same style as
  the `--before` date-parse error. Defaulting to `"updates"` means running
  `mark-read` with no flags matches the user's stated goal directly.
- `--before` (optional, format `YYYY-MM-DD`): if given, restricts to
  messages older than this date, matching Gmail's `before:YYYY/MM/DD`
  search syntax — same optional semantics as `unmark-important`'s
  `--before`.
- `--apply` (bool, default `false`): dry-run by default, same convention as
  the other two commands.
- `--limit` (int, default `0` = no limit): caps how many matching messages
  are processed, same as the other two commands' `--limit`.

Query built:
- `category:updates is:unread` (default, no `--before`)
- `category:updates is:unread before:2026/05/01` (with `--before`)

The `is:unread` filter is always included — the point of this command is to
change unread messages to read, and omitting it would mean every run
rescans and re-journals every already-read message in the category, most of
which are no-ops.

## Architecture: reuse the existing shared core

`archive-old-mail` and `unmark-important` already share `doBatchLabelRun`
and `batchLabelOp` (in `internal/cmd/batch_label_run.go`), built exactly for
this situation: a command that searches, then removes one Gmail label from
every match, differing only in which label, which strings go in the
journal, and which messages get printed. `mark-read` needs no changes to
`internal/gmail` or `internal/cmd/batch_label_run.go` — it only needs a
third `batchLabelOp` value and a thin wrapper, per the pattern CLAUDE.md's
"Adding a new batch operation" section already documents.

### `internal/cmd` changes

New file `internal/cmd/mark_read.go`, mirroring `unmark_important.go`'s
structure:

- `buildMarkReadQuery(category string, hasBefore bool, cutoff time.Time) string`
  producing the query forms above.
- `validCategories` — the fixed set `{primary, social, promotions, updates,
  forums, personal}` — and a validation check in the `RunE` function,
  erroring with the invalid value and the valid set if `--category` doesn't
  match.
- `var markReadOp = batchLabelOp{
    Label:           "UNREAD",
    CommandName:     "mark-read",
    AppliedAction:   "marked_read",
    DryRunAction:    "would_mark_read",
    ApplyMsgFormat:  "Marked %d message(s) as read.\n",
    DryRunMsgFormat: "Dry run: %d message(s) would be marked as read. Re-run with --apply to mark them.\n",
  }`
- `runMarkRead` (Cobra `RunE`): parses `--category`/`--before`/`--limit`,
  validates them, builds the query, and calls `doMarkReadRun`.
- `doMarkReadRun(ctx, svc, jrnl, query, args, apply, limit, logger, out) error`:
  one-line delegation to `doBatchLabelRun(..., markReadOp, ...)`, same shape
  as `doArchiveRun`/`doUnmarkImportantRun`.

New file `internal/cmd/mark_read_test.go`, mirroring
`unmark_important_test.go`'s structure and reusing the existing
`fakeGmailService` (already has `RemoveLabel` tracking from the
`unmark-important` work) — no test double changes needed.

### Journal

Same `.history/journal.jsonl` file, no schema changes. `Command` field
value `"mark-read"` distinguishes these entries from the other two
commands'; `Action` field values are `"marked_read"`/`"would_mark_read"`.

### Reminder

The existing `task commit-history` reminder applies unchanged to
`mark-read --apply` runs — no changes needed to `doBatchLabelRun`.

## Error handling

Same as the other two commands: missing `credentials.json` → clear error
pointing at `init`/`credentials-help`; invalid `--category` → clear error
listing the valid set; invalid `--before` → clear date-parse error;
Search/RemoveLabel failures write an error-status journal run record;
rate-limit errors retry with backoff via the already-shared `gmail` package
machinery. All of this is inherited for free from `doBatchLabelRun`, except
`--category` validation, which is new to this command.

## Testing

`internal/cmd/mark_read_test.go`, no live network calls, mirroring
`unmark_important_test.go`'s six scenarios plus category validation:

- `buildMarkReadQuery` with and without `--before`
- `doMarkReadRun` dry-run: does not call `RemoveLabel`
- `doMarkReadRun` apply: calls `RemoveLabel` with `"UNREAD"`, prints the
  success message + commit-history reminder
- `doMarkReadRun` search error: writes an error-status journal run record
- `doMarkReadRun` RemoveLabel error: writes an error-status journal run
  record, no `marked_read` message records
- `doMarkReadRun` no matches: does not call `RemoveLabel`
- `doMarkReadRun` limit: passed through to search, caps `RemoveLabel`'s ids
- `--category` validation: valid values accepted, invalid value rejected
  with a clear error, before any `gmail.Service` call

No changes needed to `internal/gmail`'s test suite — `RemoveLabel`/
`modifyLabels` are already covered by the `unmark-important` work.

## Out of Scope

- Any category other than the fixed six Gmail defines — no free-text
  `--category` escape hatch (YAGNI; the validated list already covers every
  real Gmail category).
- Combining `mark-read` with archiving or unmarking-important in one pass —
  separate concerns, same rationale as `unmark-important`'s spec.
- Changing `archive-old-mail`'s or `unmark-important`'s external CLI
  behavior, or `doBatchLabelRun`/`batchLabelOp`'s signature — this command
  is purely additive on top of the existing shared core.
