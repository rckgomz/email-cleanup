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
