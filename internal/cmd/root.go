package cmd

import "github.com/spf13/cobra"

var RootCmd = &cobra.Command{
	Use:   "email-cleanup",
	Short: "Batch operations for Gmail",
}

func Execute() error {
	return RootCmd.Execute()
}
