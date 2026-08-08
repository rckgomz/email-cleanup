package cmd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"email-cleanup/internal/gmail"
	"email-cleanup/internal/journal"
)

func TestBuildQuery(t *testing.T) {
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got := buildQuery(cutoff)
	want := "in:inbox before:2026/08/01"
	if got != want {
		t.Errorf("buildQuery() = %q, want %q", got, want)
	}
}

type fakeGmailService struct {
	matches      []gmail.MessageMeta
	searchErr    error
	archivedIDs  []string
	archiveErr   error
	archiveCalls int
}

func (f *fakeGmailService) Search(ctx context.Context, query string) ([]gmail.MessageMeta, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.matches, nil
}

func (f *fakeGmailService) Archive(ctx context.Context, ids []string) error {
	f.archiveCalls++
	f.archivedIDs = append(f.archivedIDs, ids...)
	return f.archiveErr
}

func TestDoArchiveRun_DryRun_DoesNotCallArchive(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Old 1", From: "a@example.com", Date: "2026-01-01"},
		{ID: "2", Subject: "Old 2", From: "b@example.com", Date: "2026-01-02"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doArchiveRun(context.Background(), svc, jrnl, "in:inbox before:2026/08/01", []string{"--before=2026-08-01"}, false, slog.Default(), out)
	if err != nil {
		t.Fatalf("doArchiveRun() error = %v", err)
	}

	if svc.archiveCalls != 0 {
		t.Errorf("archiveCalls = %d, want 0 for dry run", svc.archiveCalls)
	}
	if !strings.Contains(out.String(), "2 message") {
		t.Errorf("expected dry-run output to mention 2 messages, got %q", out.String())
	}
	if strings.Contains(out.String(), "commit-history") {
		t.Error("did not expect commit-history reminder on dry run")
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	var runRecords, messageRecords int
	for _, r := range records {
		switch r["type"] {
		case "run":
			runRecords++
			if r["dry_run"] != true {
				t.Errorf("run record dry_run = %v, want true", r["dry_run"])
			}
			if r["matched_count"].(float64) != 2 {
				t.Errorf("matched_count = %v, want 2", r["matched_count"])
			}
		case "message":
			messageRecords++
			if r["action"] != "would_archive" {
				t.Errorf("message action = %v, want would_archive", r["action"])
			}
		}
	}
	if runRecords != 1 || messageRecords != 2 {
		t.Errorf("runRecords = %d, messageRecords = %d, want 1 and 2", runRecords, messageRecords)
	}
}

func TestDoArchiveRun_Apply_ArchivesAndPrintsReminder(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Old 1", From: "a@example.com", Date: "2026-01-01"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doArchiveRun(context.Background(), svc, jrnl, "in:inbox before:2026/08/01", []string{"--before=2026-08-01"}, true, slog.Default(), out)
	if err != nil {
		t.Fatalf("doArchiveRun() error = %v", err)
	}

	if svc.archiveCalls != 1 {
		t.Errorf("archiveCalls = %d, want 1", svc.archiveCalls)
	}
	if len(svc.archivedIDs) != 1 || svc.archivedIDs[0] != "1" {
		t.Errorf("archivedIDs = %v, want [1]", svc.archivedIDs)
	}
	if !strings.Contains(out.String(), "task commit-history") {
		t.Errorf("expected commit-history reminder in output, got %q", out.String())
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	for _, r := range records {
		if r["type"] == "message" && r["action"] != "archived" {
			t.Errorf("message action = %v, want archived", r["action"])
		}
		if r["type"] == "run" && r["dry_run"] != false {
			t.Errorf("run dry_run = %v, want false", r["dry_run"])
		}
	}
}

func TestDoArchiveRun_SearchError_WritesErrorRunRecord(t *testing.T) {
	searchErr := errors.New("search boom")
	svc := &fakeGmailService{searchErr: searchErr}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doArchiveRun(context.Background(), svc, jrnl, "in:inbox before:2026/08/01", []string{"--before=2026-08-01"}, true, slog.Default(), out)
	if err == nil {
		t.Fatal("doArchiveRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "search boom") {
		t.Errorf("doArchiveRun() error = %v, want it to wrap search boom", err)
	}
	if svc.archiveCalls != 0 {
		t.Errorf("archiveCalls = %d, want 0 when search fails", svc.archiveCalls)
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	var runRecords, messageRecords int
	for _, r := range records {
		switch r["type"] {
		case "run":
			runRecords++
			if r["status"] != "error" {
				t.Errorf("run record status = %v, want error", r["status"])
			}
			if r["matched_count"].(float64) != 0 {
				t.Errorf("matched_count = %v, want 0", r["matched_count"])
			}
			if r["affected_count"].(float64) != 0 {
				t.Errorf("affected_count = %v, want 0", r["affected_count"])
			}
		case "message":
			messageRecords++
		}
	}
	if runRecords != 1 {
		t.Errorf("runRecords = %d, want 1", runRecords)
	}
	if messageRecords != 0 {
		t.Errorf("messageRecords = %d, want 0", messageRecords)
	}
}

func TestDoArchiveRun_ArchiveError_WritesErrorRunRecord(t *testing.T) {
	archiveErr := errors.New("archive boom")
	svc := &fakeGmailService{
		matches: []gmail.MessageMeta{
			{ID: "1", Subject: "Old 1", From: "a@example.com", Date: "2026-01-01"},
		},
		archiveErr: archiveErr,
	}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doArchiveRun(context.Background(), svc, jrnl, "in:inbox before:2026/08/01", []string{"--before=2026-08-01", "--apply"}, true, slog.Default(), out)
	if err == nil {
		t.Fatal("doArchiveRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "archive boom") {
		t.Errorf("doArchiveRun() error = %v, want it to wrap archive boom", err)
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	var runRecords int
	for _, r := range records {
		switch r["type"] {
		case "run":
			runRecords++
			if r["status"] != "error" {
				t.Errorf("run record status = %v, want error", r["status"])
			}
			if r["matched_count"].(float64) != 1 {
				t.Errorf("matched_count = %v, want 1", r["matched_count"])
			}
			if r["affected_count"].(float64) != 0 {
				t.Errorf("affected_count = %v, want 0", r["affected_count"])
			}
		case "message":
			if r["action"] == "archived" {
				t.Errorf("did not expect an 'archived' message record when Archive failed, got %v", r)
			}
		}
	}
	if runRecords != 1 {
		t.Errorf("runRecords = %d, want 1", runRecords)
	}
}

func TestDoArchiveRun_NoMatches_DoesNotCallArchive(t *testing.T) {
	svc := &fakeGmailService{matches: nil}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doArchiveRun(context.Background(), svc, jrnl, "in:inbox before:2026/08/01", nil, true, slog.Default(), out)
	if err != nil {
		t.Fatalf("doArchiveRun() error = %v", err)
	}
	if svc.archiveCalls != 0 {
		t.Errorf("archiveCalls = %d, want 0 when there are no matches", svc.archiveCalls)
	}
}
