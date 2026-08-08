package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	credentialsDirName  = ".credentials"
	credentialsFileName = "credentials.json"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create the .credentials directory and show setup instructions if needed",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInit(cmd.OutOrStdout(), credentialsDirName)
	},
}

func runInit(w io.Writer, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	credPath := filepath.Join(dir, credentialsFileName)
	_, err := os.Stat(credPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(w, "Created %s\n\n", dir)
		fmt.Fprint(w, credentialsHelpText)
		return nil
	case err != nil:
		return fmt.Errorf("checking %s: %w", credPath, err)
	default:
		fmt.Fprintf(w, "%s already exists — nothing to do.\n", credPath)
		return nil
	}
}

func init() {
	RootCmd.AddCommand(initCmd)
}
