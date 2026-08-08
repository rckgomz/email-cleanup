package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrintCredentialsHelp_PrintsManualAndGcloudSteps(t *testing.T) {
	buf := &bytes.Buffer{}

	if err := printCredentialsHelp(buf); err != nil {
		t.Fatalf("printCredentialsHelp() error = %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "console.cloud.google.com") {
		t.Error("expected output to mention the Google Cloud Console")
	}
	if !strings.Contains(out, "gcloud services enable gmail.googleapis.com") {
		t.Error("expected output to mention the gcloud services enable command")
	}
	if !strings.Contains(out, "credentials.json") {
		t.Error("expected output to mention credentials.json")
	}
}
