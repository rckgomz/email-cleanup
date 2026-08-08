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
	markReadCategory   string
	markReadBeforeDate string
	markReadApplyFlag  bool
	markReadLimitFlag  int
)

var markReadCmd = &cobra.Command{
	Use:   "mark-read",
	Short: "Mark unread messages in a category as read",
	RunE:  runMarkRead,
}

func init() {
	markReadCmd.Flags().StringVar(&markReadCategory, "category", "updates", "Gmail category to target (primary, social, promotions, updates, forums, personal)")
	markReadCmd.Flags().StringVar(&markReadBeforeDate, "before", "", "optional cutoff date, format YYYY-MM-DD (marks all matching unread mail if omitted)")
	markReadCmd.Flags().BoolVar(&markReadApplyFlag, "apply", false, "actually mark matches read (default is dry-run)")
	markReadCmd.Flags().IntVar(&markReadLimitFlag, "limit", 0, "maximum number of messages to process (0 = no limit)")
	RootCmd.AddCommand(markReadCmd)
}

var validCategories = map[string]bool{
	"primary":    true,
	"social":     true,
	"promotions": true,
	"updates":    true,
	"forums":     true,
	"personal":   true,
}

// buildMarkReadQuery returns the Gmail search query for unread messages in
// category, optionally restricted to before cutoff when hasBefore is true.
func buildMarkReadQuery(category string, hasBefore bool, cutoff time.Time) string {
	if hasBefore {
		return fmt.Sprintf("category:%s is:unread before:%s", category, cutoff.Format("2006/01/02"))
	}
	return fmt.Sprintf("category:%s is:unread", category)
}

var markReadOp = batchLabelOp{
	Label:           "UNREAD",
	CommandName:     "mark-read",
	AppliedAction:   "marked_read",
	DryRunAction:    "would_mark_read",
	ApplyMsgFormat:  "Marked %d message(s) as read.\n",
	DryRunMsgFormat: "Dry run: %d message(s) would be marked as read. Re-run with --apply to mark them.\n",
}

func runMarkRead(cmd *cobra.Command, args []string) error {
	if !validCategories[markReadCategory] {
		return fmt.Errorf("invalid --category %q: must be one of primary, social, promotions, updates, forums, personal", markReadCategory)
	}

	hasBefore := markReadBeforeDate != ""
	var cutoff time.Time
	if hasBefore {
		var err error
		cutoff, err = time.Parse("2006-01-02", markReadBeforeDate)
		if err != nil {
			return fmt.Errorf("invalid --before date %q, expected YYYY-MM-DD: %w", markReadBeforeDate, err)
		}
	}
	if markReadLimitFlag < 0 {
		return fmt.Errorf("invalid --limit %d: must be >= 0", markReadLimitFlag)
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

	query := buildMarkReadQuery(markReadCategory, hasBefore, cutoff)
	return doMarkReadRun(cmd.Context(), svc, jrnl, query, os.Args[1:], markReadApplyFlag, markReadLimitFlag, slog.Default(), cmd.OutOrStdout())
}

func doMarkReadRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, limit int, logger *slog.Logger, out io.Writer) error {
	return doBatchLabelRun(ctx, svc, jrnl, query, args, apply, limit, markReadOp, logger, out)
}
