package journal

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type RunRecord struct {
	Type          string    `json:"type"`
	RunID         string    `json:"run_id"`
	Timestamp     time.Time `json:"timestamp"`
	Command       string    `json:"command"`
	Args          []string  `json:"args"`
	Query         string    `json:"query"`
	DryRun        bool      `json:"dry_run"`
	MatchedCount  int       `json:"matched_count"`
	AffectedCount int       `json:"affected_count"`
	DurationMS    int64     `json:"duration_ms"`
	Status        string    `json:"status"`
}

type MessageRecord struct {
	Type      string `json:"type"`
	RunID     string `json:"run_id"`
	MessageID string `json:"message_id"`
	Subject   string `json:"subject"`
	From      string `json:"from"`
	Date      string `json:"date"`
	Action    string `json:"action"`
}

type Journal struct {
	path string
}

func Open(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating journal directory: %w", err)
	}
	return &Journal{path: path}, nil
}

func (j *Journal) WriteRun(rec RunRecord) error {
	rec.Type = "run"
	return j.appendLine(rec)
}

func (j *Journal) WriteMessage(rec MessageRecord) error {
	rec.Type = "message"
	return j.appendLine(rec)
}

func (j *Journal) appendLine(v any) error {
	f, err := os.OpenFile(j.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening journal: %w", err)
	}
	defer f.Close()

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshaling journal record: %w", err)
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing journal record: %w", err)
	}
	return nil
}

func ReadAll(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening journal: %w", err)
	}
	defer f.Close()

	var records []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("parsing journal line: %w", err)
		}
		records = append(records, rec)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading journal: %w", err)
	}
	return records, nil
}
