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
	runID := newRunID()
	start := time.Now()

	matches, err := svc.Search(ctx, query, limit)
	if err != nil {
		if jErr := jrnl.WriteRun(journal.RunRecord{
			RunID:         runID,
			Timestamp:     start,
			Command:       "archive-old-mail",
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

	action := "would_archive"
	var affected int
	if apply && len(matches) > 0 {
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		if err := svc.Archive(ctx, ids); err != nil {
			if jErr := jrnl.WriteRun(journal.RunRecord{
				RunID:         runID,
				Timestamp:     start,
				Command:       "archive-old-mail",
				Args:          args,
				Query:         query,
				DryRun:        !apply,
				MatchedCount:  len(matches),
				AffectedCount: 0,
				DurationMS:    time.Since(start).Milliseconds(),
				Status:        "error",
			}); jErr != nil {
				return fmt.Errorf("archiving messages: %w (additionally failed to write error journal record: %v)", err, jErr)
			}
			return fmt.Errorf("archiving messages: %w", err)
		}
		action = "archived"
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
		Command:       "archive-old-mail",
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
		fmt.Fprintf(out, "Archived %d message(s).\n", affected)
		fmt.Fprintln(out, "Reminder: run `task commit-history` to commit and push the updated journal.")
	} else {
		fmt.Fprintf(out, "Dry run: %d message(s) would be archived. Re-run with --apply to archive them.\n", len(matches))
	}
	return nil
}

func newRunID() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z")
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
