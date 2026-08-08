package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpen_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "journal.jsonl")

	if _, err := Open(path); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("expected parent directory to exist: %v", err)
	}
}

func TestWriteRun_AppendsJSONLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	rec := RunRecord{
		Type:          "run",
		RunID:         "run-1",
		Timestamp:     time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC),
		Command:       "archive-old-mail",
		Args:          []string{"--before=2026-08-01"},
		Query:         "in:inbox before:2026/08/01",
		DryRun:        true,
		MatchedCount:  3,
		AffectedCount: 0,
		DurationMS:    42,
		Status:        "ok",
	}
	if err := j.WriteRun(rec); err != nil {
		t.Fatalf("WriteRun() error = %v", err)
	}

	records, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0]["run_id"] != "run-1" {
		t.Errorf("run_id = %v, want run-1", records[0]["run_id"])
	}
	if records[0]["matched_count"].(float64) != 3 {
		t.Errorf("matched_count = %v, want 3", records[0]["matched_count"])
	}
}

func TestWriteMessage_AppendsJSONLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	rec := MessageRecord{
		Type:      "message",
		RunID:     "run-1",
		MessageID: "msg-1",
		Subject:   "Old newsletter",
		From:      "news@example.com",
		Date:      "2026-01-01",
		Action:    "would_archive",
	}
	if err := j.WriteMessage(rec); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	records, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("len(records) = %d, want 1", len(records))
	}
	if records[0]["message_id"] != "msg-1" {
		t.Errorf("message_id = %v, want msg-1", records[0]["message_id"])
	}
	if records[0]["action"] != "would_archive" {
		t.Errorf("action = %v, want would_archive", records[0]["action"])
	}
}

func TestReadAll_ReturnsRecordsInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	j, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	if err := j.WriteMessage(MessageRecord{Type: "message", RunID: "run-1", MessageID: "a"}); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	if err := j.WriteMessage(MessageRecord{Type: "message", RunID: "run-1", MessageID: "b"}); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}

	records, err := ReadAll(path)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("len(records) = %d, want 2", len(records))
	}
	if records[0]["message_id"] != "a" || records[1]["message_id"] != "b" {
		t.Errorf("records out of order: %v", records)
	}
}
