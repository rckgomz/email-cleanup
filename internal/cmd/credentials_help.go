package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const credentialsHelpText = `To use email-cleanup you need a Gmail API OAuth "Desktop app" client,
saved as .credentials/credentials.json.

=== Option A: Google Cloud Console (manual) ===

1. Go to https://console.cloud.google.com/ and create a project (or select
   an existing one).
2. Go to "APIs & Services > Library", search for "Gmail API", and enable it.
3. Go to "APIs & Services > OAuth consent screen". Choose "External", fill
   in the required fields, and add your own Google account under
   "Test users" (keeps the app in Testing mode, no verification needed).
4. Go to "APIs & Services > Credentials", click "Create Credentials >
   OAuth client ID", choose Application type "Desktop app", and create it.
5. Download the client's JSON and save it as:
     .credentials/credentials.json

=== Option B: gcloud CLI (copy-paste; not run automatically by this tool) ===

  gcloud projects create email-cleanup-$RANDOM --name="email-cleanup"
  gcloud config set project <PROJECT_ID_FROM_ABOVE>
  gcloud services enable gmail.googleapis.com

  # The OAuth consent screen and OAuth client ID still require either the
  # Console UI or the (alpha) "gcloud alpha iap oauth-brands" /
  # "oauth-clients" commands, since a Desktop client secret cannot be
  # downloaded non-interactively. After creating the client in the
  # Console, download its JSON and save it as:
  #   .credentials/credentials.json

Once credentials.json is in place, run:
  email-cleanup archive-old-mail --before=YYYY-MM-DD
to trigger the one-time browser consent flow, which caches a token at
.credentials/token.json for future runs.
`

func printCredentialsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, credentialsHelpText)
	return err
}

var credentialsHelpCmd = &cobra.Command{
	Use:   "credentials-help",
	Short: "Show steps to obtain a Gmail API credentials.json",
	RunE: func(cmd *cobra.Command, args []string) error {
		return printCredentialsHelp(cmd.OutOrStdout())
	},
}

func init() {
	RootCmd.AddCommand(credentialsHelpCmd)
}
