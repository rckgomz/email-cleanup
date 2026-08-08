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
