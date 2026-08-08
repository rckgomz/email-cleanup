package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var jsonLogs bool

var RootCmd = &cobra.Command{
	Use:           "email-cleanup",
	Short:         "Batch operations for Gmail",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		setupLogger(jsonLogs)
	},
}

func Execute() error {
	return RootCmd.Execute()
}

func setupLogger(jsonOutput bool) {
	var handler slog.Handler
	if jsonOutput {
		handler = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	slog.SetDefault(slog.New(handler))
}

func init() {
	RootCmd.PersistentFlags().BoolVar(&jsonLogs, "json", false, "output logs as JSON")
	if err := viper.BindPFlag("json", RootCmd.PersistentFlags().Lookup("json")); err != nil {
		panic(err)
	}
}
