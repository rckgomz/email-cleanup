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

func TestBuildMarkReadQuery_NoBefore(t *testing.T) {
	got := buildMarkReadQuery("updates", false, time.Time{})
	want := "category:updates is:unread"
	if got != want {
		t.Errorf("buildMarkReadQuery(%q, false, ...) = %q, want %q", "updates", got, want)
	}
}

func TestBuildMarkReadQuery_WithBefore(t *testing.T) {
	cutoff := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	got := buildMarkReadQuery("updates", true, cutoff)
	want := "category:updates is:unread before:2026/05/01"
	if got != want {
		t.Errorf("buildMarkReadQuery(%q, true, ...) = %q, want %q", "updates", got, want)
	}
}

func TestBuildMarkReadQuery_OtherCategory(t *testing.T) {
	got := buildMarkReadQuery("promotions", false, time.Time{})
	want := "category:promotions is:unread"
	if got != want {
		t.Errorf("buildMarkReadQuery(%q, false, ...) = %q, want %q", "promotions", got, want)
	}
}

func TestValidCategories_KnownValues(t *testing.T) {
	for _, want := range []string{"primary", "social", "promotions", "updates", "forums", "personal"} {
		if !validCategories[want] {
			t.Errorf("validCategories[%q] = false, want true", want)
		}
	}
}

func TestValidCategories_RejectsUnknown(t *testing.T) {
	if validCategories["spam"] {
		t.Error("validCategories[\"spam\"] = true, want false")
	}
	if validCategories[""] {
		t.Error("validCategories[\"\"] = true, want false")
	}
}

func TestDoMarkReadRun_DryRun_DoesNotCallRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Newsletter 1", From: "a@example.com", Date: "2026-01-01"},
		{ID: "2", Subject: "Newsletter 2", From: "b@example.com", Date: "2026-01-02"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doMarkReadRun(context.Background(), svc, jrnl, "category:updates is:unread", []string{"mark-read"}, false, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doMarkReadRun() error = %v", err)
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
			if r["command"] != "mark-read" {
				t.Errorf("command = %v, want mark-read", r["command"])
			}
			if r["dry_run"] != true {
				t.Errorf("run record dry_run = %v, want true", r["dry_run"])
			}
			if r["matched_count"].(float64) != 2 {
				t.Errorf("matched_count = %v, want 2", r["matched_count"])
			}
		case "message":
			messageRecords++
			if r["action"] != "would_mark_read" {
				t.Errorf("message action = %v, want would_mark_read", r["action"])
			}
		}
	}
	if runRecords != 1 || messageRecords != 2 {
		t.Errorf("runRecords = %d, messageRecords = %d, want 1 and 2", runRecords, messageRecords)
	}
}

func TestDoMarkReadRun_Apply_RemovesUnreadLabelAndPrintsReminder(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Newsletter 1", From: "a@example.com", Date: "2026-01-01"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doMarkReadRun(context.Background(), svc, jrnl, "category:updates is:unread", []string{"mark-read", "--apply"}, true, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doMarkReadRun() error = %v", err)
	}

	if svc.removeLabelCalls != 1 {
		t.Errorf("removeLabelCalls = %d, want 1", svc.removeLabelCalls)
	}
	if svc.removedLabel != "UNREAD" {
		t.Errorf("removedLabel = %q, want UNREAD", svc.removedLabel)
	}
	if len(svc.removedIDs) != 1 || svc.removedIDs[0] != "1" {
		t.Errorf("removedIDs = %v, want [1]", svc.removedIDs)
	}
	if !strings.Contains(out.String(), "Marked 1 message(s) as read.") {
		t.Errorf("expected apply output to report 1 marked message, got %q", out.String())
	}
	if !strings.Contains(out.String(), "task commit-history") {
		t.Errorf("expected commit-history reminder in output, got %q", out.String())
	}

	records, err := journal.ReadAll(jPath)
	if err != nil {
		t.Fatalf("journal.ReadAll() error = %v", err)
	}
	for _, r := range records {
		if r["type"] == "message" && r["action"] != "marked_read" {
			t.Errorf("message action = %v, want marked_read", r["action"])
		}
		if r["type"] == "run" && r["dry_run"] != false {
			t.Errorf("run dry_run = %v, want false", r["dry_run"])
		}
	}
}

func TestDoMarkReadRun_SearchError_WritesErrorRunRecord(t *testing.T) {
	searchErr := errors.New("search boom")
	svc := &fakeGmailService{searchErr: searchErr}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doMarkReadRun(context.Background(), svc, jrnl, "category:updates is:unread", []string{"mark-read"}, true, 0, slog.Default(), out)
	if err == nil {
		t.Fatal("doMarkReadRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "search boom") {
		t.Errorf("doMarkReadRun() error = %v, want it to wrap search boom", err)
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

func TestDoMarkReadRun_RemoveLabelError_WritesErrorRunRecord(t *testing.T) {
	removeErr := errors.New("remove boom")
	svc := &fakeGmailService{
		matches: []gmail.MessageMeta{
			{ID: "1", Subject: "Newsletter 1", From: "a@example.com", Date: "2026-01-01"},
		},
		removeLabelErr: removeErr,
	}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doMarkReadRun(context.Background(), svc, jrnl, "category:updates is:unread", []string{"mark-read", "--apply"}, true, 0, slog.Default(), out)
	if err == nil {
		t.Fatal("doMarkReadRun() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "remove boom") {
		t.Errorf("doMarkReadRun() error = %v, want it to wrap remove boom", err)
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
			if r["action"] == "marked_read" {
				t.Errorf("did not expect a 'marked_read' message record when RemoveLabel failed, got %v", r)
			}
		}
	}
	if runRecords != 1 {
		t.Errorf("runRecords = %d, want 1", runRecords)
	}
}

func TestDoMarkReadRun_NoMatches_DoesNotCallRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: nil}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doMarkReadRun(context.Background(), svc, jrnl, "category:updates is:unread", nil, true, 0, slog.Default(), out)
	if err != nil {
		t.Fatalf("doMarkReadRun() error = %v", err)
	}
	if svc.removeLabelCalls != 0 {
		t.Errorf("removeLabelCalls = %d, want 0 when there are no matches", svc.removeLabelCalls)
	}
}

func TestDoMarkReadRun_Limit_PassedThroughToSearchAndCapsRemoveLabel(t *testing.T) {
	svc := &fakeGmailService{matches: []gmail.MessageMeta{
		{ID: "1", Subject: "Newsletter 1", From: "a@example.com", Date: "2026-01-01"},
		{ID: "2", Subject: "Newsletter 2", From: "b@example.com", Date: "2026-01-02"},
		{ID: "3", Subject: "Newsletter 3", From: "c@example.com", Date: "2026-01-03"},
	}}
	jPath := filepath.Join(t.TempDir(), "journal.jsonl")
	jrnl, err := journal.Open(jPath)
	if err != nil {
		t.Fatalf("journal.Open() error = %v", err)
	}
	out := &bytes.Buffer{}

	err = doMarkReadRun(context.Background(), svc, jrnl, "category:updates is:unread", []string{"mark-read", "--apply", "--limit=2"}, true, 2, slog.Default(), out)
	if err != nil {
		t.Fatalf("doMarkReadRun() error = %v", err)
	}

	if len(svc.removedIDs) != 2 {
		t.Errorf("removedIDs = %v, want 2 ids (limit=2)", svc.removedIDs)
	}
	if !strings.Contains(out.String(), "Marked 2 message(s) as read.") {
		t.Errorf("expected output to report 2 marked messages, got %q", out.String())
	}
}
