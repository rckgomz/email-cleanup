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

func TestBuildImportantQuery_NoBefore(t *testing.T) {
	got := buildImportantQuery(false, time.Time{})
	want := "label:important"
	if got != want {
		t.Errorf("buildImportantQuery(false, ...) = %q, want %q", got, want)
	}
}

func TestBuildImportantQuery_WithBefore(t *testing.T) {
	cutoff := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	got := buildImportantQuery(true, cutoff)
	want := "label:important before:2026/08/01"
	if got != want {
		t.Errorf("buildImportantQuery(true, ...) = %q, want %q", got, want)
	}
}

func TestDoUnmarkImportantRun_DryRun_DoesNotCallRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
		{ID: "2", Subject: "Important 2", From: "b@example.com", Date: "2026-01-02"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important"}, false, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}

	if svc.removeLabelCalls != 0 {
		t.Errorf("removeLabelCalls = %d, want 0 for dry run", svc.removeLabelCalls)
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
			if r["command"] != "unmark-important" {
				t.Errorf("command = %v, want unmark-important", r["command"])
			}
			if r["dry_run"] != true {
				t.Errorf("run record dry_run = %v, want true", r["dry_run"])
			}
			if r["matched_count"].(float64) != 2 {
				t.Errorf("matched_count = %v, want 2", r["matched_count"])
			}
		case "message":
			messageRecords++
			if r["action"] != "would_unmark_important" {
				t.Errorf("message action = %v, want would_unmark_important", r["action"])
			}
		}
	}
	if runRecords != 1 || messageRecords != 2 {
		t.Errorf("runRecords = %d, messageRecords = %d, want 1 and 2", runRecords, messageRecords)
	}
}

func TestDoUnmarkImportantRun_Apply_RemovesImportantLabelAndPrintsReminder(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important", "--apply"}, true, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}

	if svc.removeLabelCalls != 1 {
		t.Errorf("removeLabelCalls = %d, want 1", svc.removeLabelCalls)
	}
	if svc.removedLabel != "IMPORTANT" {
		t.Errorf("removedLabel = %q, want IMPORTANT", svc.removedLabel)
	}
	if len(svc.removedIDs) != 1 || svc.removedIDs[0] != "1" {
		t.Errorf("removedIDs = %v, want [1]", svc.removedIDs)
	}
	if !strings.Contains(out.String(), "Unmarked 1 message(s) as important.") {
		t.Errorf("expected apply output to report 1 unmarked message, got %q", out.String())
	}
	if !strings.Contains(out.String(), "task commit-history") {
		t.Errorf("expected commit-history reminder in output, got %q", out.String())
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	for _, r := range records {
		if r["type"] == "message" && r["action"] != "unmarked_important" {
			t.Errorf("message action = %v, want unmarked_important", r["action"])
		}
		if r["type"] == "run" && r["dry_run"] != false {
			t.Errorf("run dry_run = %v, want false", r["dry_run"])
		}
	}
}

func TestDoUnmarkImportantRun_SearchError_WritesErrorRunRecord(t *testing.T) {
	searchErr := errors.New("search boom")
	svc := &fakeGmailService{searchErr: searchErr}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important"}, true, 0, slog.Default(), out)
	if err == nil {
		t.Fatal("doUnmarkImportantRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "search boom") {
		t.Errorf("doUnmarkImportantRun() error = %v, want it to wrap search boom", err)
	}
	if svc.removeLabelCalls != 0 {
		t.Errorf("removeLabelCalls = %d, want 0 when search fails", svc.removeLabelCalls)
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	var runRecords int
	for _, r := range records {
		if r["type"] == "run" {
			runRecords++
			if r["status"] != "error" {
				t.Errorf("run record status = %v, want error", r["status"])
			}
		}
	}
	if runRecords != 1 {
		t.Errorf("runRecords = %d, want 1", runRecords)
	}
}

func TestDoUnmarkImportantRun_RemoveLabelError_WritesErrorRunRecord(t *testing.T) {
	removeErr := errors.New("remove boom")
	svc := &fakeGmailService{
		matches: []gmail.MessageMeta{
			{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
		},
		removeLabelErr: removeErr,
	}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important", "--apply"}, true, 0, slog.Default(), out)
	if err == nil {
		t.Fatal("doUnmarkImportantRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "remove boom") {
		t.Errorf("doUnmarkImportantRun() error = %v, want it to wrap remove boom", err)
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
			if r["action"] == "unmarked_important" {
				t.Errorf("did not expect an 'unmarked_important' message record when RemoveLabel failed, got %v", r)
			}
		}
	}
	if runRecords != 1 {
		t.Errorf("runRecords = %d, want 1", runRecords)
	}
}

func TestDoUnmarkImportantRun_NoMatches_DoesNotCallRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: nil}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", nil, true, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}
	if svc.removeLabelCalls != 0 {
		t.Errorf("removeLabelCalls = %d, want 0 when there are no matches", svc.removeLabelCalls)
	}
}

func TestDoUnmarkImportantRun_Limit_PassedThroughToSearchAndCapsRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Important 1", From: "a@example.com", Date: "2026-01-01"},
		{ID: "2", Subject: "Important 2", From: "b@example.com", Date: "2026-01-02"},
		{ID: "3", Subject: "Important 3", From: "c@example.com", Date: "2026-01-03"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doUnmarkImportantRun(context.Background(), svc, jrnl, "label:important", []string{"unmark-important", "--apply", "--limit=2"}, true, 2, slog.Default(), out)
	if err != nil {
		t.Fatalf("doUnmarkImportantRun() error = %v", err)
	}

	if len(svc.removedIDs) != 2 {
		t.Errorf("removedIDs = %v, want 2 ids (limit=2)", svc.removedIDs)
	}
	if !strings.Contains(out.String(), "Unmarked 2 message(s) as important.") {
		t.Errorf("expected output to report 2 unmarked messages, got %q", out.String())
	}
}
