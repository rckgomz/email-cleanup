package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunInit_CreatesDirAndPrintsHelp_WhenCredentialsMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".credentials")
	buf := &bytes.Buffer{}

	if err := runInit(buf, dir); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected dir to exist: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %v, want 0700", info.Mode().Perm())
	}
	if !strings.Contains(buf.String(), "credentials.json") {
		t.Error("expected help text to be printed when credentials.json is missing")
	}
}

func TestRunInit_Idempotent_WhenCredentialsAlreadyPresent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	credPath := filepath.Join(dir, credentialsFileName)
	if err := os.WriteFile(credPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	buf := &bytes.Buffer{}
	if err := runInit(buf, dir); err != nil {
		t.Fatalf("runInit() error = %v", err)
	}

	if strings.Contains(buf.String(), "console.cloud.google.com") {
		t.Error("did not expect setup instructions when credentials.json already exists")
	}
	if !strings.Contains(buf.String(), "already exists") {
		t.Error("expected a message noting credentials.json already exists")
	}
}
