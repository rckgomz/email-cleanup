package cmd

import (
	"log/slog"
	"testing"
)

func TestSetupLogger_JSONTrue_UsesJSONHandler(t *testing.T) {
	setupLogger(true)
	_, ok := slog.Default().Handler().(*slog.JSONHandler)
	if !ok {
		t.Errorf("Handler() = %T, want *slog.JSONHandler", slog.Default().Handler())
	}
}

func TestSetupLogger_JSONFalse_UsesTextHandler(t *testing.T) {
	setupLogger(false)
	_, ok := slog.Default().Handler().(*slog.TextHandler)
	if !ok {
		t.Errorf("Handler() = %T, want *slog.TextHandler", slog.Default().Handler())
	}
}

func TestRootCmd_HasJSONFlag(t *testing.T) {
	flag := RootCmd.PersistentFlags().Lookup("json")
	if flag == nil {
		t.Fatal("expected --json persistent flag to be registered")
	}
	if flag.DefValue != "false" {
		t.Errorf("--json default = %q, want false", flag.DefValue)
	}
}
