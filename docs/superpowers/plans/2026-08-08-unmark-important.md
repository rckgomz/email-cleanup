# Unmark Important Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an `unmark-important` command that removes Gmail's `IMPORTANT` label from matching messages, reusing `archive-old-mail`'s search/dry-run/journal machinery via a shared, generalized core instead of duplicating it.

**Architecture:** Generalize `gmail.Service.Archive` and `internal/cmd`'s `doArchiveRun` into label-agnostic shared functions (`gmail.APIService.modifyLabels`, `cmd.doBatchLabelRun`), each parameterized by which label to remove and by command-specific journal/output text. `archive-old-mail` becomes a thin wrapper around the shared core (behavior-preserving — its existing tests must pass unchanged), and the new `unmark-important` command is a second thin wrapper.

**Tech Stack:** Same as the existing project — Go, Cobra, Viper, `log/slog`, `google.golang.org/api/gmail/v1`, `golang.org/x/time/rate`, `golang.org/x/sync/errgroup`.

## Global Constraints

- `unmark-important` removes the `IMPORTANT` label only — it does not archive messages. Archiving stays a separate step via the existing `archive-old-mail` command.
- `--before` is **optional** for `unmark-important` (unlike `archive-old-mail`, where it's required). Query is `label:important` alone, or `label:important before:YYYY/MM/DD` when `--before` is given.
- `--apply` defaults to `false` (dry-run), same convention as `archive-old-mail`.
- `--limit` (default `0` = no limit) is included from the start, same flag/semantics as `archive-old-mail`'s `--limit`.
- Same `.history/journal.jsonl` file — no second journal. `Command` field distinguishes `"archive-old-mail"` from `"unmark-important"` journal entries.
- The `archive-old-mail` → `unmark-important` refactor must be **behavior-preserving**: every existing test in `archive_old_mail_test.go` must still pass, unchanged, proving the shared-core extraction didn't alter `archive-old-mail`'s external behavior.
- No live network/API calls in the automated test suite, consistent with the rest of the project.
- Reuse the existing `fakeGmailService` test double (defined in `archive_old_mail_test.go`) for `unmark-important`'s tests too — same package (`cmd`), no need to redefine it.

---

## File Structure

```
email-cleanup/
  internal/
    gmail/
      client.go                    # MODIFY: add RemoveLabel + modifyLabels helper
    cmd/
      batch_label_run.go            # CREATE: shared doBatchLabelRun + batchLabelOp + newRunID
      archive_old_mail.go            # MODIFY: doArchiveRun becomes a thin wrapper
      archive_old_mail_test.go        # MODIFY: add RemoveLabel to fakeGmailService (additive only)
      unmark_important.go              # CREATE: new command
      unmark_important_test.go          # CREATE: tests mirroring archive_old_mail_test.go
  README.md                            # MODIFY: document unmark-important
  CLAUDE.md                             # MODIFY: mention batchLabelOp pattern
```

---

### Task 1: Generalize `gmail.Service` with `RemoveLabel`

**Files:**
- Modify: `internal/gmail/client.go`

**Interfaces:**
- Consumes: nothing new (refactor of existing `Archive`).
- Produces:
  - `Service` interface gains: `RemoveLabel(ctx context.Context, ids []string, label string) error`
  - `APIService.RemoveLabel(ctx context.Context, ids []string, labelID string) error`
  - `APIService.modifyLabels(ctx context.Context, ids []string, labelID string) error` (unexported, shared by `Archive` and `RemoveLabel`)

This is a **behavior-preserving refactor** of `Archive` — no new unit tests are added here, consistent with the existing project convention that `Archive`/`Search`'s Gmail-API-calling methods are thin wiring verified by the full test suite + manual smoke testing (Task 4), not direct unit tests against a fake HTTP server. `Archive`'s error message text changes slightly (from `"archiving batch of %d messages: %w"` to `"removing label %s from batch of %d messages: %w"`) as a result of sharing the helper — this is safe since no existing test asserts on `Archive`'s exact error string.

- [ ] **Step 1: Add `RemoveLabel` to the `Service` interface**

In `internal/gmail/client.go`, find:

```go
type Service interface {
	// Search returns messages matching query. If limit > 0, it stops once
	// limit messages have been fetched, issuing no further API calls.
	Search(ctx context.Context, query string, limit int) ([]MessageMeta, error)
	Archive(ctx context.Context, ids []string) error
}
```

Replace with:

```go
type Service interface {
	// Search returns messages matching query. If limit > 0, it stops once
	// limit messages have been fetched, issuing no further API calls.
	Search(ctx context.Context, query string, limit int) ([]MessageMeta, error)
	Archive(ctx context.Context, ids []string) error
	// RemoveLabel removes the given Gmail label ID (e.g. "IMPORTANT") from
	// each message in ids.
	RemoveLabel(ctx context.Context, ids []string, label string) error
}
```

- [ ] **Step 2: Extract `modifyLabels` and rewrite `Archive`/add `RemoveLabel`**

Find:

```go
func (a *APIService) Archive(ctx context.Context, ids []string) error {
	for _, chunk := range chunkIDs(ids, batchModifyLimit) {
		req := &gmailapi.BatchModifyMessagesRequest{
			Ids:            chunk,
			RemoveLabelIds: []string{"INBOX"},
		}
		err := withRateLimitRetry(ctx, a.limiter, func() error {
			return a.svc.Users.Messages.BatchModify("me", req).Context(ctx).Do()
		})
		if err != nil {
			return fmt.Errorf("archiving batch of %d messages: %w", len(chunk), err)
		}
	}
	return nil
}
```

Replace with:

```go
func (a *APIService) Archive(ctx context.Context, ids []string) error {
	return a.modifyLabels(ctx, ids, "INBOX")
}

// RemoveLabel removes labelID from each message in ids, e.g. "IMPORTANT"
// to unmark messages as important.
func (a *APIService) RemoveLabel(ctx context.Context, ids []string, labelID string) error {
	return a.modifyLabels(ctx, ids, labelID)
}

// modifyLabels removes labelID from each message in ids, in chunks of up
// to batchModifyLimit, respecting the rate limiter and retrying on
// transient quota errors. Shared by Archive (labelID "INBOX") and
// RemoveLabel (any other label).
func (a *APIService) modifyLabels(ctx context.Context, ids []string, labelID string) error {
	for _, chunk := range chunkIDs(ids, batchModifyLimit) {
		req := &gmailapi.BatchModifyMessagesRequest{
			Ids:            chunk,
			RemoveLabelIds: []string{labelID},
		}
		err := withRateLimitRetry(ctx, a.limiter, func() error {
			return a.svc.Users.Messages.BatchModify("me", req).Context(ctx).Do()
		})
		if err != nil {
			return fmt.Errorf("removing label %s from batch of %d messages: %w", labelID, len(chunk), err)
		}
	}
	return nil
}
```

- [ ] **Step 3: Build and run the full existing test suite**

Run: `go build ./... && go vet ./...`
Expected: both succeed with no errors (confirms `RemoveLabel` satisfies any interface-implementer checks and nothing else references the old `Archive` internals in a way that breaks).

Run: `go test ./internal/gmail/... -race -v`
Expected: PASS — every existing test (all `TestAPIServiceSearch_*`, `TestChunkIDs`, `TestMessageMetaFromAPI*`, `TestIsRateLimitError`, `TestWithRateLimitRetry_*`) still passes unchanged. None of these tests exercise `Archive`/`RemoveLabel` directly, so this confirms the refactor didn't break anything else in the package.

- [ ] **Step 4: Commit**

```bash
git add internal/gmail/client.go
git commit -m "refactor(gmail): generalize Archive into modifyLabels + add RemoveLabel"
```

---

### Task 2: Generalize `internal/cmd`'s batch-run core

**Files:**
- Create: `internal/cmd/batch_label_run.go`
- Modify: `internal/cmd/archive_old_mail.go`
- Modify: `internal/cmd/archive_old_mail_test.go`

**Interfaces:**
- Consumes: `gmail.Service` (now with `RemoveLabel`) from Task 1; `journal.Journal`, `journal.RunRecord`, `journal.MessageRecord` (unchanged, from the original project).
- Produces:
  - `type batchLabelOp struct { Label, CommandName, AppliedAction, DryRunAction, ApplyMsgFormat, DryRunMsgFormat string }`
  - `func doBatchLabelRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, limit int, op batchLabelOp, logger *slog.Logger, out io.Writer) error`
  - `func newRunID() string` (moved here from `archive_old_mail.go`, unchanged)
  - `doArchiveRun` keeps its exact existing signature but its body becomes a one-line delegation to `doBatchLabelRun`.
  - `fakeGmailService` (in the test file) gains a working `RemoveLabel` implementation with call-tracking fields, for reuse by Task 3's tests.

This task is also a **behavior-preserving refactor** for `archive-old-mail` — every existing test in `archive_old_mail_test.go` must pass unchanged (they call `doArchiveRun` with its existing signature; only the fake's new `RemoveLabel` method is additive).

- [ ] **Step 1: Create `internal/cmd/batch_label_run.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"email-cleanup/internal/gmail"
	"email-cleanup/internal/journal"
)

// batchLabelOp parameterizes doBatchLabelRun for a specific label-removal
// command (e.g. archive-old-mail removes INBOX, unmark-important removes
// IMPORTANT).
type batchLabelOp struct {
	Label           string // Gmail label ID to remove, e.g. "INBOX" or "IMPORTANT"
	CommandName     string // recorded in journal.RunRecord.Command
	AppliedAction   string // journal.MessageRecord.Action when --apply succeeds
	DryRunAction    string // journal.MessageRecord.Action on a dry run
	ApplyMsgFormat  string // fmt format for the apply-success line; takes one %d (affected count)
	DryRunMsgFormat string // fmt format for the dry-run summary line; takes one %d (matched count)
}

// doBatchLabelRun searches for messages matching query, and on apply,
// removes op.Label from each match via svc.RemoveLabel. Every invocation
// (dry-run or apply) writes to the journal. Shared by archive-old-mail and
// unmark-important so both stay in sync under future changes.
func doBatchLabelRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, limit int, op batchLabelOp, logger *slog.Logger, out io.Writer) error {
	runID := newRunID()
	start := time.Now()

	matches, err := svc.Search(ctx, query, limit)
	if err != nil {
		if jErr := jrnl.WriteRun(journal.RunRecord{
			RunID:         runID,
			Timestamp:     start,
			Command:       op.CommandName,
			Args:          args,
			Query:         query,
			DryRun:        !apply,
			MatchedCount:  0,
			AffectedCount: 0,
			DurationMS:    time.Since(start).Milliseconds(),
			Status:        "error",
		}); jErr != nil {
			return fmt.Errorf("searching messages: %w (additionally failed to write error journal record: %v)", err, jErr)
		}
		return fmt.Errorf("searching messages: %w", err)
	}
	logger.Info("search complete", "matched", len(matches), "query", query)

	action := op.DryRunAction
	var affected int
	if apply && len(matches) > 0 {
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		if err := svc.RemoveLabel(ctx, ids, op.Label); err != nil {
			if jErr := jrnl.WriteRun(journal.RunRecord{
				RunID:         runID,
				Timestamp:     start,
				Command:       op.CommandName,
				Args:          args,
				Query:         query,
				DryRun:        !apply,
				MatchedCount:  len(matches),
				AffectedCount: 0,
				DurationMS:    time.Since(start).Milliseconds(),
				Status:        "error",
			}); jErr != nil {
				return fmt.Errorf("removing label: %w (additionally failed to write error journal record: %v)", err, jErr)
			}
			return fmt.Errorf("removing label: %w", err)
		}
		action = op.AppliedAction
		affected = len(matches)
	}

	for _, m := range matches {
		if err := jrnl.WriteMessage(journal.MessageRecord{
			RunID:     runID,
			MessageID: m.ID,
			Subject:   m.Subject,
			From:      m.From,
			Date:      m.Date,
			Action:    action,
		}); err != nil {
			return fmt.Errorf("writing journal message record: %w", err)
		}
	}

	if err := jrnl.WriteRun(journal.RunRecord{
		RunID:         runID,
		Timestamp:     start,
		Command:       op.CommandName,
		Args:          args,
		Query:         query,
		DryRun:        !apply,
		MatchedCount:  len(matches),
		AffectedCount: affected,
		DurationMS:    time.Since(start).Milliseconds(),
		Status:        "ok",
	}); err != nil {
		return fmt.Errorf("writing journal run record: %w", err)
	}

	if apply {
		fmt.Fprintf(out, op.ApplyMsgFormat, affected)
		fmt.Fprintln(out, "Reminder: run `task commit-history` to commit and push the updated journal.")
	} else {
		fmt.Fprintf(out, op.DryRunMsgFormat, len(matches))
	}
	return nil
}

func newRunID() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z")
}
```

- [ ] **Step 2: Rewrite `internal/cmd/archive_old_mail.go`**

Replace the entire file with:

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"

	"email-cleanup/internal/gmail"
	"email-cleanup/internal/journal"
)

var (
	beforeDate string
	applyFlag  bool
	limitFlag  int
)

var archiveOldMailCmd = &cobra.Command{
	Use:   "archive-old-mail",
	Short: "Archive inbox messages older than a given date",
	RunE:  runArchiveOldMail,
}

func init() {
	archiveOldMailCmd.Flags().StringVar(&beforeDate, "before", "", "cutoff date, format YYYY-MM-DD (required)")
	archiveOldMailCmd.Flags().BoolVar(&applyFlag, "apply", false, "actually archive matches (default is dry-run)")
	archiveOldMailCmd.Flags().IntVar(&limitFlag, "limit", 0, "maximum number of messages to process (0 = no limit)")
	if err := archiveOldMailCmd.MarkFlagRequired("before"); err != nil {
		panic(err)
	}
	RootCmd.AddCommand(archiveOldMailCmd)
}

func buildQuery(cutoff time.Time) string {
	return fmt.Sprintf("in:inbox before:%s", cutoff.Format("2006/01/02"))
}

var archiveOp = batchLabelOp{
	Label:           "INBOX",
	CommandName:     "archive-old-mail",
	AppliedAction:   "archived",
	DryRunAction:    "would_archive",
	ApplyMsgFormat:  "Archived %d message(s).\n",
	DryRunMsgFormat: "Dry run: %d message(s) would be archived. Re-run with --apply to archive them.\n",
}

func runArchiveOldMail(cmd *cobra.Command, args []string) error {
	cutoff, err := time.Parse("2006-01-02", beforeDate)
	if err != nil {
		return fmt.Errorf("invalid --before date %q, expected YYYY-MM-DD: %w", beforeDate, err)
	}
	if limitFlag < 0 {
		return fmt.Errorf("invalid --limit %d: must be >= 0", limitFlag)
	}

	credPath := credentialsDirName + "/" + credentialsFileName
	if _, statErr := statCredentials(credPath); statErr != nil {
		return fmt.Errorf("credentials not found at %s — run `email-cleanup init` or `email-cleanup credentials-help`: %w", credPath, statErr)
	}

	svc, err := newRealGmailService(cmd.Context())
	if err != nil {
		return fmt.Errorf("setting up Gmail client: %w", err)
	}

	jrnl, err := journal.Open(".history/journal.jsonl")
	if err != nil {
		return fmt.Errorf("opening journal: %w", err)
	}

	query := buildQuery(cutoff)
	return doArchiveRun(cmd.Context(), svc, jrnl, query, os.Args[1:], applyFlag, limitFlag, slog.Default(), cmd.OutOrStdout())
}

func doArchiveRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, limit int, logger *slog.Logger, out io.Writer) error {
	return doBatchLabelRun(ctx, svc, jrnl, query, args, apply, limit, archiveOp, logger, out)
}

func statCredentials(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func newRealGmailService(ctx context.Context) (gmail.Service, error) {
	credBytes, err := os.ReadFile(credentialsDirName + "/" + credentialsFileName)
	if err != nil {
		return nil, fmt.Errorf("reading credentials.json: %w", err)
	}
	config, err := gmail.LoadConfig(credBytes)
	if err != nil {
		return nil, err
	}
	cache := gmail.NewTokenCache(credentialsDirName + "/token.json")
	httpClient, err := gmail.GetClient(ctx, config, cache, func(authURL string) (string, error) {
		return promptForAuthCode(os.Stdout, authURL)
	})
	if err != nil {
		return nil, err
	}
	httpClient.Timeout = 30 * time.Second
	apiSvc, err := gmailapi.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating gmail api service: %w", err)
	}
	return gmail.NewAPIService(apiSvc), nil
}

func promptForAuthCode(w io.Writer, authURL string) (string, error) {
	fmt.Fprintf(w, "Go to the following link in your browser and sign in/consent:\n%s\n\n"+
		"Your browser will then redirect to a localhost address that fails to load "+
		"(e.g. \"This site can't be reached\" or \"connection refused\") — that's expected. "+
		"Look at the browser's address bar: copy the value of the \"code=\" query parameter "+
		"(everything after \"code=\" and before the next \"&\") and paste it below.\n\nCode: ", authURL)
	var code string
	if _, err := fmt.Scan(&code); err != nil {
		return "", fmt.Errorf("reading auth code: %w", err)
	}
	return code, nil
}
```

(This removes `doArchiveRun`'s old body and the `newRunID` function — both now live in `batch_label_run.go` — and adds the `archiveOp` variable. Every other function is unchanged.)

- [ ] **Step 3: Add `RemoveLabel` to `fakeGmailService` in `archive_old_mail_test.go`**

Find:

```go
type fakeGmailService struct {
	matches      []gmail.MessageMeta
	searchErr    error
	archivedIDs  []string
	archiveErr   error
	archiveCalls int
}
```

Replace with:

```go
type fakeGmailService struct {
	matches      []gmail.MessageMeta
	searchErr    error
	archivedIDs  []string
	archiveErr   error
	archiveCalls int

	removeLabelCalls int
	removedLabel     string
	removedIDs       []string
	removeLabelErr   error
}
```

Find:

```go
func (f *fakeGmailService) Archive(ctx context.Context, ids []string) error {
	f.archiveCalls++
	f.archivedIDs = append(f.archivedIDs, ids...)
	return f.archiveErr
}
```

Add immediately after it:

```go
func (f *fakeGmailService) RemoveLabel(ctx context.Context, ids []string, label string) error {
	f.removeLabelCalls++
	f.removedLabel = label
	f.removedIDs = append(f.removedIDs, ids...)
	return f.removeLabelErr
}
```

Do not change anything else in this file — every existing test function stays exactly as-is.

- [ ] **Step 4: Build and run the full existing test suite**

Run: `go build ./... && go vet ./...`
Expected: both succeed.

Run: `go test ./internal/cmd/... -race -v`
Expected: PASS — every existing test (`TestBuildQuery`, all `TestDoArchiveRun_*`, `TestPrintCredentialsHelp_*`, `TestRunInit_*`, `TestSetupLogger_*`, `TestRootCmd_HasJSONFlag`) passes **unchanged**. This is the critical regression check proving the refactor preserved `archive-old-mail`'s behavior exactly.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/batch_label_run.go internal/cmd/archive_old_mail.go internal/cmd/archive_old_mail_test.go
git commit -m "refactor(cmd): extract doBatchLabelRun shared by archive-old-mail"
```

---

### Task 3: `unmark-important` command

**Files:**
- Create: `internal/cmd/unmark_important.go`
- Create: `internal/cmd/unmark_important_test.go`

**Interfaces:**
- Consumes: `batchLabelOp`, `doBatchLabelRun`, `newRunID` from Task 2 (`internal/cmd/batch_label_run.go`); `statCredentials`, `newRealGmailService`, `credentialsDirName`, `credentialsFileName` from `archive_old_mail.go`/`init.go` (existing, same package); `fakeGmailService` (with `RemoveLabel`) from Task 2's updated `archive_old_mail_test.go`.
- Produces:
  - `func buildImportantQuery(hasBefore bool, cutoff time.Time) string`
  - `var unmarkImportantOp batchLabelOp`
  - `func runUnmarkImportant(cmd *cobra.Command, args []string) error` (Cobra `RunE`)
  - `func doUnmarkImportantRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, limit int, logger *slog.Logger, out io.Writer) error`

- [ ] **Step 1: Write the failing tests**

Create `internal/cmd/unmark_important_test.go`:

```go
package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"email-cleanup/internal/gmail"
	"email-cleanup/internal/journal"
)

func TestBuildImportantQuery_NoBefore(t *testing.T) {
	got := buildImportantQuery(false, time.Time{})
	want := "label:important"
	if got != want {
		t.Errorf("buildImportantQuery(false, ...) = %q, want %q", got, want)
	}
}

func TestBuildImportantQuery_WithBefore(t *testing.T) {
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got := buildImportantQuery(true, cutoff)
	want := "label:important before:2026/08/01"
	if got != want {
		t.Errorf("buildImportantQuery(true, ...) = %q, want %q", got, want)
	}
}

func TestDoUnmarkImportantRun_DryRun_DoesNotCallRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
		{ID: "2", Subject: "Important 2", From: "b@example.com", Date: "2026-01-02"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important"}, false, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}

	if svc.removeLabelCalls != 0 {
		t.Errorf("removeLabelCalls = %d, want 0 for dry run", svc.removeLabelCalls)
	}
	if !strings.Contains(out.String(), "2 message") {
		t.Errorf("expected dry-run output to mention 2 messages, got %q", out.String())
	}
	if strings.Contains(out.String(), "commit-history") {
		t.Error("did not expect commit-history reminder on dry run")
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	var runRecords, messageRecords int
	for _, r := range records {
		switch r["type"] {
		case "run":
			runRecords++
			if r["command"] != "unmark-important" {
				t.Errorf("command = %v, want unmark-important", r["command"])
			}
			if r["dry_run"] != true {
				t.Errorf("run record dry_run = %v, want true", r["dry_run"])
			}
			if r["matched_count"].(float64) != 2 {
				t.Errorf("matched_count = %v, want 2", r["matched_count"])
			}
		case "message":
			messageRecords++
			if r["action"] != "would_unmark_important" {
				t.Errorf("message action = %v, want would_unmark_important", r["action"])
			}
		}
	}
	if runRecords != 1 || messageRecords != 2 {
		t.Errorf("runRecords = %d, messageRecords = %d, want 1 and 2", runRecords, messageRecords)
	}
}

func TestDoUnmarkImportantRun_Apply_RemovesImportantLabelAndPrintsReminder(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important", "--apply"}, true, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}

	if svc.removeLabelCalls != 1 {
		t.Errorf("removeLabelCalls = %d, want 1", svc.removeLabelCalls)
	}
	if svc.removedLabel != "IMPORTANT" {
		t.Errorf("removedLabel = %q, want IMPORTANT", svc.removedLabel)
	}
	if len(svc.removedIDs) != 1 || svc.removedIDs[0] != "1" {
		t.Errorf("removedIDs = %v, want [1]", svc.removedIDs)
	}
	if !strings.Contains(out.String(), "Unmarked 1 message(s) as important.") {
		t.Errorf("expected apply output to report 1 unmarked message, got %q", out.String())
	}
	if !strings.Contains(out.String(), "task commit-history") {
		t.Errorf("expected commit-history reminder in output, got %q", out.String())
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	for _, r := range records {
		if r["type"] == "message" && r["action"] != "unmarked_important" {
			t.Errorf("message action = %v, want unmarked_important", r["action"])
		}
		if r["type"] == "run" && r["dry_run"] != false {
			t.Errorf("run dry_run = %v, want false", r["dry_run"])
		}
	}
}

func TestDoUnmarkImportantRun_SearchError_WritesErrorRunRecord(t *testing.T) {
	searchErr := errors.New("search boom")
	svc := &fakeGmailService{searchErr: searchErr}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important"}, true, 0, slog.Default(), out)
	if err == nil {
		t.Fatal("doUnmarkImportantRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "search boom") {
		t.Errorf("doUnmarkImportantRun() error = %v, want it to wrap search boom", err)
	}
	if svc.removeLabelCalls != 0 {
		t.Errorf("removeLabelCalls = %d, want 0 when search fails", svc.removeLabelCalls)
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	var runRecords int
	for _, r := range records {
		if r["type"] == "run" {
			runRecords++
			if r["status"] != "error" {
				t.Errorf("run record status = %v, want error", r["status"])
			}
		}
	}
	if runRecords != 1 {
		t.Errorf("runRecords = %d, want 1", runRecords)
	}
}

func TestDoUnmarkImportantRun_RemoveLabelError_WritesErrorRunRecord(t *testing.T) {
	removeErr := errors.New("remove boom")
	svc := &fakeGmailService{
		matches: []gmail.MessageMeta{
			{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
		},
		removeLabelErr: removeErr,
	}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important", "--apply"}, true, 0, slog.Default(), out)
	if err == nil {
		t.Fatal("doUnmarkImportantRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "remove boom") {
		t.Errorf("doUnmarkImportantRun() error = %v, want it to wrap remove boom", err)
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	var runRecords int
	for _, r := range records {
		switch r["type"] {
		case "run":
			runRecords++
			if r["status"] != "error" {
				t.Errorf("run record status = %v, want error", r["status"])
			}
			if r["matched_count"].(float64) != 1 {
				t.Errorf("matched_count = %v, want 1", r["matched_count"])
			}
			if r["affected_count"].(float64) != 0 {
				t.Errorf("affected_count = %v, want 0", r["affected_count"])
			}
		case "message":
			if r["action"] == "unmarked_important" {
				t.Errorf("did not expect an 'unmarked_important' message record when RemoveLabel failed, got %v", r)
			}
		}
	}
	if runRecords != 1 {
		t.Errorf("runRecords = %d, want 1", runRecords)
	}
}

func TestDoUnmarkImportantRun_NoMatches_DoesNotCallRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: nil}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", nil, true, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}
	if svc.removeLabelCalls != 0 {
		t.Errorf("removeLabelCalls = %d, want 0 when there are no matches", svc.removeLabelCalls)
	}
}

func TestDoUnmarkImportantRun_Limit_PassedThroughToSearchAndCapsRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
		{ID: "2", Subject: "Important 2", From: "b@example.com", Date: "2026-01-02"},
		{ID: "3", Subject: "Important 3", From: "c@example.com", Date: "2026-01-03"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important", "--apply", "--limit=2"}, true, 2, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}

	if len(svc.removedIDs) != 2 {
		t.Errorf("removedIDs = %v, want 2 ids (limit=2)", svc.removedIDs)
	}
	if !strings.Contains(out.String(), "Unmarked 2 message(s) as important.") {
		t.Errorf("expected output to report 2 unmarked messages, got %q", out.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cmd/... -run 'TestBuildImportantQuery|TestDoUnmarkImportantRun' -v`
Expected: FAIL — `buildImportantQuery`, `doUnmarkImportantRun` undefined.

- [ ] **Step 3: Create `internal/cmd/unmark_important.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"email-cleanup/internal/gmail"
	"email-cleanup/internal/journal"
)

var (
	unmarkBeforeDate string
	unmarkApplyFlag  bool
	unmarkLimitFlag  int
)

var unmarkImportantCmd = &cobra.Command{
	Use:   "unmark-important",
	Short: "Remove the Important marker from matching messages",
	RunE:  runUnmarkImportant,
}

func init() {
	unmarkImportantCmd.Flags().StringVar(&unmarkBeforeDate, "before", "", "optional cutoff date, format YYYY-MM-DD (unmarks all Important mail if omitted)")
	unmarkImportantCmd.Flags().BoolVar(&unmarkApplyFlag, "apply", false, "actually unmark matches (default is dry-run)")
	unmarkImportantCmd.Flags().IntVar(&unmarkLimitFlag, "limit", 0, "maximum number of messages to process (0 = no limit)")
	RootCmd.AddCommand(unmarkImportantCmd)
}

// buildImportantQuery returns the Gmail search query for messages marked
// Important, optionally restricted to before cutoff when hasBefore is true.
func buildImportantQuery(hasBefore bool, cutoff time.Time) string {
	if hasBefore {
		return fmt.Sprintf("label:important before:%s", cutoff.Format("2006/01/02"))
	}
	return "label:important"
}

var unmarkImportantOp = batchLabelOp{
	Label:           "IMPORTANT",
	CommandName:     "unmark-important",
	AppliedAction:   "unmarked_important",
	DryRunAction:    "would_unmark_important",
	ApplyMsgFormat:  "Unmarked %d message(s) as important.\n",
	DryRunMsgFormat: "Dry run: %d message(s) would be unmarked as important. Re-run with --apply to unmark them.\n",
}

func runUnmarkImportant(cmd *cobra.Command, args []string) error {
	hasBefore := unmarkBeforeDate != ""
	var cutoff time.Time
	if hasBefore {
		var err error
		cutoff, err = time.Parse("2006-01-02", unmarkBeforeDate)
		if err != nil {
			return fmt.Errorf("invalid --before date %q, expected YYYY-MM-DD: %w", unmarkBeforeDate, err)
		}
	}
	if unmarkLimitFlag < 0 {
		return fmt.Errorf("invalid --limit %d: must be >= 0", unmarkLimitFlag)
	}

	credPath := credentialsDirName + "/" + credentialsFileName
	if _, statErr := statCredentials(credPath); statErr != nil {
		return fmt.Errorf("credentials not found at %s — run `email-cleanup init` or `email-cleanup credentials-help`: %w", credPath, statErr)
	}

	svc, err := newRealGmailService(cmd.Context())
	if err != nil {
		return fmt.Errorf("setting up Gmail client: %w", err)
	}

	jrnl, err := journal.Open(".history/journal.jsonl")
	if err != nil {
		return fmt.Errorf("opening journal: %w", err)
	}

	query := buildImportantQuery(hasBefore, cutoff)
	return doUnmarkImportantRun(cmd.Context(), svc, jrnl, query, os.Args[1:], unmarkApplyFlag, unmarkLimitFlag, slog.Default(), cmd.OutOrStdout())
}

func doUnmarkImportantRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, limit int, logger *slog.Logger, out io.Writer) error {
	return doBatchLabelRun(ctx, svc, jrnl, query, args, apply, limit, unmarkImportantOp, logger, out)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cmd/... -v`
Expected: PASS — all tests in `internal/cmd`, including the new `unmark_important_test.go` tests and every pre-existing test (unaffected).

- [ ] **Step 5: Run the full test suite**

Run: `go build ./... && go vet ./... && go test ./... -race`
Expected: PASS across `internal/journal`, `internal/gmail`, `internal/cmd`.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/unmark_important.go internal/cmd/unmark_important_test.go
git commit -m "feat(cmd): add unmark-important command"
```

---

### Task 4: Build verification and manual smoke test

**Files:**
- None created — verification only.

**Interfaces:**
- Consumes: everything from Tasks 1–3.

- [ ] **Step 1: Build the binary**

Run: `task build`
Expected: exits 0, produces `bin/email-cleanup`.

- [ ] **Step 2: Verify `--help` lists the new command**

Run: `./bin/email-cleanup --help`
Expected: `unmark-important` appears in the `Available Commands` list alongside `archive-old-mail`, `init`, `credentials-help`.

- [ ] **Step 3: Verify `unmark-important --help` shows the expected flags**

Run: `./bin/email-cleanup unmark-important --help`
Expected: `--before` (marked optional, not required — no `[required]` marker or `MarkFlagRequired` error when omitted), `--apply`, `--limit`, plus the global `--json`.

- [ ] **Step 4: Dry run against the real account with a small limit**

Run: `./bin/email-cleanup unmark-important --limit=5`
Expected: exits 0, prints `Dry run: N message(s) would be unmarked as important. Re-run with --apply to unmark them.` with `N <= 5`, no mailbox changes, a new `run` journal record with `command: "unmark-important"`, `dry_run: true`.

- [ ] **Step 5: Apply on a small limit**

Run: `./bin/email-cleanup unmark-important --apply --limit=3`
Expected: exits 0, prints `Unmarked N message(s) as important.` (N ≤ 3) followed by the `task commit-history` reminder. Spot-check in Gmail (or via a subsequent dry run with the same query) that those messages no longer carry the Important marker.

- [ ] **Step 6: Confirm `archive-old-mail` still works (regression check)**

Run: `./bin/email-cleanup archive-old-mail --before=2026-08-01`
Expected: dry-run output identical in shape to before this plan's changes (e.g. `Dry run: N message(s) would be archived...`), confirming Tasks 1–2's refactor didn't change `archive-old-mail`'s external behavior.

- [ ] **Step 7: Commit (only if any fixes were needed in prior steps)**

If steps 1–6 required code changes, stage and commit them with a message describing the fix. If everything passed as implemented, no commit is needed for this task.

---

### Task 5: Update `README.md` and `CLAUDE.md`

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: final command name/flags from Task 3 (`unmark-important --before --apply --limit`).

- [ ] **Step 1: Add an `unmark-important` section to `README.md`**

In `README.md`, immediately after the existing `### Archive old inbox mail` section (right before `### Machine-readable logs`), insert:

```markdown
### Unmark important mail

```bash
# Dry run (default) — shows what would be unmarked, changes nothing
./bin/email-cleanup unmark-important

# Actually remove the Important marker
./bin/email-cleanup unmark-important --apply

# Only unmark Important mail older than a date
./bin/email-cleanup unmark-important --before=2026-08-01 --apply

# Try it on just the first 10 matches first
./bin/email-cleanup unmark-important --apply --limit=10
```

Unlike `archive-old-mail`, `--before` is optional here — omit it to target
every message currently marked Important. This command only removes the
Important marker; it does not archive or otherwise move messages. Run
`archive-old-mail` separately afterward if you also want them archived.
```

- [ ] **Step 2: Update `CLAUDE.md`**

In `CLAUDE.md`, find:

```markdown
## What this is

A Go CLI (`email-cleanup`) for batch Gmail operations, run locally instead
of through the Gmail UI. First command: `archive-old-mail`, which archives
inbox messages older than a given date.
```

Replace with:

```markdown
## What this is

A Go CLI (`email-cleanup`) for batch Gmail operations, run locally instead
of through the Gmail UI. Commands: `archive-old-mail` (archives inbox
messages older than a given date) and `unmark-important` (removes the
Important marker from matching messages).
```

Find:

```markdown
- `internal/cmd/` — one file per Cobra subcommand (`root.go`, `init.go`,
  `credentials_help.go`, `archive_old_mail.go`)
```

Replace with:

```markdown
- `internal/cmd/` — one file per Cobra subcommand (`root.go`, `init.go`,
  `credentials_help.go`, `archive_old_mail.go`, `unmark_important.go`),
  plus `batch_label_run.go` holding the shared search/dry-run/journal core
  (`doBatchLabelRun`, `batchLabelOp`) both label-removal commands wrap.
```

Find the `## Adding a new batch operation` section:

```markdown
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
```

Replace with:

```markdown
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
```

- [ ] **Step 3: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "docs: document unmark-important command"
```

---

## Self-Review Notes

- **Spec coverage:** command name/flags (`--before` optional, `--apply`, `--limit`) — Task 3; generalized `gmail.Service`/`APIService` — Task 1; generalized `internal/cmd` core (`doBatchLabelRun`/`batchLabelOp`) with `archive-old-mail` as a behavior-preserving thin wrapper — Task 2; same journal file, `Command`/`Action` field differentiation — Tasks 2–3; `task commit-history` reminder reuse — Task 2 (unchanged text, reused via `doBatchLabelRun`); manual verification — Task 4; docs — Task 5. All spec sections covered.
- **Type consistency:** `batchLabelOp` fields (`Label`, `CommandName`, `AppliedAction`, `DryRunAction`, `ApplyMsgFormat`, `DryRunMsgFormat`) used identically in `batch_label_run.go`, `archive_old_mail.go`'s `archiveOp`, and `unmark_important.go`'s `unmarkImportantOp`. `gmail.Service.RemoveLabel(ctx, ids, label)` signature matches across the interface (Task 1), `APIService` (Task 1), `fakeGmailService` (Task 2), and both call sites (`doBatchLabelRun` in Task 2, exercised by Task 3's tests). `doArchiveRun` and `doUnmarkImportantRun` share an identical parameter list/order, both delegating to `doBatchLabelRun`.
- **No placeholders:** all steps contain full code; the two refactor tasks (1–2) specify exact "find this / replace with this" diffs against the plan's own record of current file contents, not abstract descriptions.
- **Behavior-preservation is the key risk in this plan** (Tasks 1–2 touch already-shipped, already-used-on-a-real-63K-message-mailbox code) — mitigated by requiring the full pre-existing test suite to pass unchanged after each refactor step, plus an explicit regression check in Task 4 Step 6 against the real Gmail account.
