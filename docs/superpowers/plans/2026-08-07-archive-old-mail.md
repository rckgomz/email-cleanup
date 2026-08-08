# Archive Old Mail Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go CLI (`email-cleanup`) that authenticates to Gmail via OAuth2 and archives inbox messages older than a given date, with dry-run-by-default safety, structured logging, and an append-only journal of every run.

**Architecture:** A single Cobra root binary (`cmd/email-cleanup`) with subcommands (`init`, `credentials-help`, `archive-old-mail`) implemented in `internal/cmd`. Gmail access is abstracted behind a small `gmail.Service` interface (`internal/gmail`) so command logic can be unit-tested against a fake, with a real implementation wrapping the generated Gmail API client. Every `archive-old-mail` run — dry-run or apply — appends structured records to a JSONL journal (`internal/journal`) checked into git.

**Tech Stack:** Go (module `email-cleanup`), Cobra, Viper, `log/slog`, Task (Taskfile.dev), `google.golang.org/api/gmail/v1`, `golang.org/x/oauth2`.

## Global Constraints

- Follow the [golang-standards project-layout](https://github.com/golang-standards/project-layout): `cmd/`, `internal/`.
- CLI framework: Cobra. Config/flag binding: Viper.
- Logging: `log/slog` only. Default handler is human-friendly text to stderr; global `--json` flag switches to `slog.NewJSONHandler`. Handler is chosen once, in the root command's `PersistentPreRun`.
- OAuth scope: `https://www.googleapis.com/auth/gmail.modify`.
- `archive-old-mail` defaults to dry-run; mutating the mailbox requires the explicit `--apply` flag.
- Gmail `batchModify` calls are chunked to ≤1000 message IDs per call.
- `.credentials/` (holds `credentials.json` and `token.json`) is gitignored. `.history/journal.jsonl` is **not** gitignored — it is committed.
- Any command that mutates the mailbox (`archive-old-mail --apply`) prints a reminder after completing: `Reminder: run \`task commit-history\` to commit and push the updated journal.`
- `credentials-help` output must cover both the manual Google Cloud Console path and equivalent `gcloud` CLI commands (informational text only — the tool never executes `gcloud`).
- Build/dev tasks go through a Taskfile (`task build`, `task test`, `task tidy`, `task fmt`, `task vet`, `task run --`, `task commit-history`), not a Makefile.
- No live network/API calls in the automated test suite — Gmail API interactions are exercised through the `gmail.Service` interface and a test fake.

---

## File Structure

```
email-cleanup/
  cmd/
    email-cleanup/
      main.go
  internal/
    cmd/
      root.go
      root_test.go
      credentials_help.go
      credentials_help_test.go
      init.go
      init_test.go
      archive_old_mail.go
      archive_old_mail_test.go
    gmail/
      auth.go
      auth_test.go
      client.go
      client_test.go
    journal/
      journal.go
      journal_test.go
  .gitignore
  go.mod
  go.sum
  Taskfile.yml
  CLAUDE.md
  README.md
```

---

### Task 1: Project scaffolding

**Files:**
- Create: `go.mod`
- Create: `.gitignore`
- Create: `Taskfile.yml`
- Create: `cmd/email-cleanup/main.go`
- Create: `internal/cmd/root.go` (minimal stub, fleshed out in Task 5)

**Interfaces:**
- Produces: Go module `email-cleanup`, buildable via `go build ./...`; `task` targets `build`, `test`, `tidy`, `fmt`, `vet`, `run`.

- [ ] **Step 1: Initialize the Go module**

```bash
cd /Users/erryk/Projects/email-cleanup
go mod init email-cleanup
```

- [ ] **Step 2: Add dependencies**

```bash
go get github.com/spf13/cobra@latest
go get github.com/spf13/viper@latest
go get google.golang.org/api@latest
go get golang.org/x/oauth2@latest
```

- [ ] **Step 3: Write `.gitignore`**

```
/.credentials/
/bin/
```

- [ ] **Step 4: Write a minimal `internal/cmd/root.go` stub**

```go
package cmd

import "github.com/spf13/cobra"

var RootCmd = &cobra.Command{
	Use:   "email-cleanup",
	Short: "Batch operations for Gmail",
}

func Execute() error {
	return RootCmd.Execute()
}
```

- [ ] **Step 5: Write `cmd/email-cleanup/main.go`**

```go
package main

import (
	"fmt"
	"os"

	"email-cleanup/internal/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```

- [ ] **Step 6: Write `Taskfile.yml`**

```yaml
version: '3'

tasks:
  build:
    desc: Build the email-cleanup binary
    cmds:
      - go build -o bin/email-cleanup ./cmd/email-cleanup

  run:
    desc: Run email-cleanup (pass args after --)
    cmds:
      - go run ./cmd/email-cleanup {{.CLI_ARGS}}

  test:
    desc: Run the test suite
    cmds:
      - go test ./...

  tidy:
    desc: Tidy go.mod/go.sum
    cmds:
      - go mod tidy

  fmt:
    desc: Format Go source
    cmds:
      - go fmt ./...

  vet:
    desc: Vet Go source
    cmds:
      - go vet ./...

  commit-history:
    desc: Commit and push the .history journal
    cmds:
      - git add .history
      - git commit -m "Update journal"
      - git push
```

- [ ] **Step 7: Verify it builds and the task runner works**

Run: `go build ./...`
Expected: exits 0, no output.

Run: `task --list`
Expected: lists `build`, `run`, `test`, `tidy`, `fmt`, `vet`, `commit-history`.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum .gitignore Taskfile.yml cmd internal/cmd/root.go
git commit -m "Scaffold Go module, Cobra root stub, and Taskfile"
```

---

### Task 2: Journal package

**Files:**
- Create: `internal/journal/journal.go`
- Test: `internal/journal/journal_test.go`

**Interfaces:**
- Produces:
  - `type RunRecord struct { Type, RunID, Command, Query, Status string; Timestamp time.Time; Args []string; DryRun bool; MatchedCount, AffectedCount int; DurationMS int64 }`
  - `type MessageRecord struct { Type, RunID, MessageID, Subject, From, Date, Action string }`
  - `func Open(path string) (*Journal, error)`
  - `func (j *Journal) WriteRun(rec RunRecord) error`
  - `func (j *Journal) WriteMessage(rec MessageRecord) error`
  - `func ReadAll(path string) ([]map[string]any, error)`

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/journal/... -v`
Expected: FAIL — `Open`, `RunRecord`, `MessageRecord`, `ReadAll` undefined (package doesn't exist yet).

- [ ] **Step 3: Implement `internal/journal/journal.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/journal/... -v`
Expected: PASS — all 4 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/journal
git commit -m "Add JSONL journal package with run/message records"
```

---

### Task 3: Gmail service interface, pure helpers, and API-backed implementation

**Files:**
- Create: `internal/gmail/client.go`
- Test: `internal/gmail/client_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type MessageMeta struct { ID, Subject, From, Date string }`
  - `type Service interface { Search(ctx context.Context, query string) ([]MessageMeta, error); Archive(ctx context.Context, ids []string) error }`
  - `func NewAPIService(svc *gmailapi.Service) *APIService` (implements `Service`)
  - `func chunkIDs(ids []string, size int) [][]string` (unexported, tested in-package)
  - `func messageMetaFromAPI(msg *gmailapi.Message) MessageMeta` (unexported, tested in-package)

- [ ] **Step 1: Write the failing tests for the pure helpers**

```go
package gmail

import (
	"reflect"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
)

func TestChunkIDs(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		size int
		want [][]string
	}{
		{"empty", nil, 2, nil},
		{"exact multiple", []string{"a", "b", "c", "d"}, 2, [][]string{{"a", "b"}, {"c", "d"}}},
		{"remainder", []string{"a", "b", "c"}, 2, [][]string{{"a", "b"}, {"c"}}},
		{"single chunk larger than input", []string{"a", "b"}, 5, [][]string{{"a", "b"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chunkIDs(tt.ids, tt.size)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chunkIDs(%v, %d) = %v, want %v", tt.ids, tt.size, got, tt.want)
			}
		})
	}
}

func TestMessageMetaFromAPI(t *testing.T) {
	msg := &gmailapi.Message{
		Id: "msg-123",
		Payload: &gmailapi.MessagePart{
			Headers: []*gmailapi.MessagePartHeader{
				{Name: "Subject", Value: "Old newsletter"},
				{Name: "From", Value: "news@example.com"},
				{Name: "Date", Value: "Wed, 01 Jan 2026 10:00:00 +0000"},
			},
		},
	}

	got := messageMetaFromAPI(msg)
	want := MessageMeta{
		ID:      "msg-123",
		Subject: "Old newsletter",
		From:    "news@example.com",
		Date:    "Wed, 01 Jan 2026 10:00:00 +0000",
	}
	if got != want {
		t.Errorf("messageMetaFromAPI() = %+v, want %+v", got, want)
	}
}

func TestMessageMetaFromAPI_MissingHeaders(t *testing.T) {
	msg := &gmailapi.Message{Id: "msg-456", Payload: &gmailapi.MessagePart{}}

	got := messageMetaFromAPI(msg)
	want := MessageMeta{ID: "msg-456"}
	if got != want {
		t.Errorf("messageMetaFromAPI() = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gmail/... -v`
Expected: FAIL — `chunkIDs`, `messageMetaFromAPI`, `MessageMeta` undefined.

- [ ] **Step 3: Implement `internal/gmail/client.go`**

```go
package gmail

import (
	"context"
	"fmt"

	gmailapi "google.golang.org/api/gmail/v1"
)

const batchModifyLimit = 1000

type MessageMeta struct {
	ID      string
	Subject string
	From    string
	Date    string
}

type Service interface {
	Search(ctx context.Context, query string) ([]MessageMeta, error)
	Archive(ctx context.Context, ids []string) error
}

type APIService struct {
	svc *gmailapi.Service
}

func NewAPIService(svc *gmailapi.Service) *APIService {
	return &APIService{svc: svc}
}

func (a *APIService) Search(ctx context.Context, query string) ([]MessageMeta, error) {
	var results []MessageMeta
	pageToken := ""
	for {
		call := a.svc.Users.Messages.List("me").Q(query).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("listing messages: %w", err)
		}
		for _, m := range resp.Messages {
			full, err := a.svc.Users.Messages.Get("me", m.Id).
				Format("metadata").
				MetadataHeaders("Subject", "From", "Date").
				Context(ctx).Do()
			if err != nil {
				return nil, fmt.Errorf("getting message %s: %w", m.Id, err)
			}
			results = append(results, messageMetaFromAPI(full))
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return results, nil
}

func (a *APIService) Archive(ctx context.Context, ids []string) error {
	for _, chunk := range chunkIDs(ids, batchModifyLimit) {
		req := &gmailapi.BatchModifyMessagesRequest{
			Ids:            chunk,
			RemoveLabelIds: []string{"INBOX"},
		}
		if err := a.svc.Users.Messages.BatchModify("me", req).Context(ctx).Do(); err != nil {
			return fmt.Errorf("archiving batch of %d messages: %w", len(chunk), err)
		}
	}
	return nil
}

func chunkIDs(ids []string, size int) [][]string {
	if len(ids) == 0 {
		return nil
	}
	var chunks [][]string
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		chunks = append(chunks, ids[i:end])
	}
	return chunks
}

func messageMetaFromAPI(msg *gmailapi.Message) MessageMeta {
	meta := MessageMeta{ID: msg.Id}
	if msg.Payload == nil {
		return meta
	}
	for _, h := range msg.Payload.Headers {
		switch h.Name {
		case "Subject":
			meta.Subject = h.Value
		case "From":
			meta.From = h.Value
		case "Date":
			meta.Date = h.Value
		}
	}
	return meta
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gmail/... -v`
Expected: PASS — `TestChunkIDs` (all subtests), `TestMessageMetaFromAPI`, `TestMessageMetaFromAPI_MissingHeaders`.

Note: `Search` and `Archive` on `APIService` call the real Gmail API and are not covered by unit tests here — they're thin wiring over the tested pure helpers (`chunkIDs`, `messageMetaFromAPI`) and are verified manually per the README once `archive-old-mail` is wired up in Task 8.

- [ ] **Step 5: Commit**

```bash
git add internal/gmail/client.go internal/gmail/client_test.go
git commit -m "Add gmail.Service interface and API-backed implementation"
```

---

### Task 4: OAuth2 auth and token cache

**Files:**
- Create: `internal/gmail/auth.go`
- Test: `internal/gmail/auth_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks (independent of `client.go`).
- Produces:
  - `const Scope = "https://www.googleapis.com/auth/gmail.modify"`
  - `type TokenCache struct{ path string }`
  - `func NewTokenCache(path string) *TokenCache`
  - `func (c *TokenCache) Load() (*oauth2.Token, error)`
  - `func (c *TokenCache) Save(tok *oauth2.Token) error`
  - `func LoadConfig(credentialsJSON []byte) (*oauth2.Config, error)`
  - `func GetClient(ctx context.Context, config *oauth2.Config, cache *TokenCache, promptFunc func(authURL string) (code string, err error)) (*http.Client, error)`

- [ ] **Step 1: Write the failing tests**

```go
package gmail

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestTokenCache_SaveThenLoad_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	cache := NewTokenCache(path)

	want := &oauth2.Token{
		AccessToken:  "access-123",
		RefreshToken: "refresh-456",
		TokenType:    "Bearer",
		Expiry:       time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC),
	}

	if err := cache.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := cache.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

func TestTokenCache_Load_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	cache := NewTokenCache(path)

	_, err := cache.Load()
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing file")
	}
}

const fakeCredentialsJSON = `{
	"installed": {
		"client_id": "test-client-id.apps.googleusercontent.com",
		"client_secret": "test-secret",
		"auth_uri": "https://accounts.google.com/o/oauth2/auth",
		"token_uri": "https://oauth2.googleapis.com/token",
		"redirect_uris": ["urn:ietf:wg:oauth:2.0:oob", "http://localhost"]
	}
}`

func TestLoadConfig_ParsesClientCredentials(t *testing.T) {
	config, err := LoadConfig([]byte(fakeCredentialsJSON))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if config.ClientID != "test-client-id.apps.googleusercontent.com" {
		t.Errorf("ClientID = %q, want test-client-id.apps.googleusercontent.com", config.ClientID)
	}
	if len(config.Scopes) != 1 || config.Scopes[0] != Scope {
		t.Errorf("Scopes = %v, want [%s]", config.Scopes, Scope)
	}
}

func TestLoadConfig_InvalidJSON(t *testing.T) {
	_, err := LoadConfig([]byte("not json"))
	if err == nil {
		t.Fatal("LoadConfig() error = nil, want error for invalid JSON")
	}
}

func TestGetClient_UsesCachedToken_SkipsPrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token.json")
	cache := NewTokenCache(path)
	cachedTok := &oauth2.Token{AccessToken: "cached-access", RefreshToken: "cached-refresh", Expiry: time.Now().Add(time.Hour)}
	if err := cache.Save(cachedTok); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	config := &oauth2.Config{ClientID: "id", Scopes: []string{Scope}}
	promptCalled := false
	promptFunc := func(authURL string) (string, error) {
		promptCalled = true
		return "", errors.New("prompt should not be called")
	}

	_, err := GetClient(context.Background(), config, cache, promptFunc)
	if err != nil {
		t.Fatalf("GetClient() error = %v", err)
	}
	if promptCalled {
		t.Error("promptFunc was called even though a valid cached token existed")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/gmail/... -run 'TestTokenCache|TestLoadConfig|TestGetClient' -v`
Expected: FAIL — `NewTokenCache`, `LoadConfig`, `GetClient`, `Scope` undefined.

- [ ] **Step 3: Implement `internal/gmail/auth.go`**

```go
package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const Scope = "https://www.googleapis.com/auth/gmail.modify"

type TokenCache struct {
	path string
}

func NewTokenCache(path string) *TokenCache {
	return &TokenCache{path: path}
}

func (c *TokenCache) Load() (*oauth2.Token, error) {
	data, err := os.ReadFile(c.path)
	if err != nil {
		return nil, fmt.Errorf("reading token cache: %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal(data, &tok); err != nil {
		return nil, fmt.Errorf("parsing token cache: %w", err)
	}
	return &tok, nil
}

func (c *TokenCache) Save(tok *oauth2.Token) error {
	data, err := json.MarshalIndent(tok, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling token: %w", err)
	}
	if err := os.WriteFile(c.path, data, 0o600); err != nil {
		return fmt.Errorf("writing token cache: %w", err)
	}
	return nil
}

func LoadConfig(credentialsJSON []byte) (*oauth2.Config, error) {
	config, err := google.ConfigFromJSON(credentialsJSON, Scope)
	if err != nil {
		return nil, fmt.Errorf("parsing credentials.json: %w", err)
	}
	return config, nil
}

// GetClient returns an authenticated HTTP client, reusing the cached token
// if present and valid, otherwise running the OAuth2 consent flow via
// promptFunc (which is given the auth URL and must return the code the
// user obtained after authorizing).
func GetClient(ctx context.Context, config *oauth2.Config, cache *TokenCache, promptFunc func(authURL string) (code string, err error)) (*http.Client, error) {
	tok, err := cache.Load()
	if err != nil {
		authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
		code, err := promptFunc(authURL)
		if err != nil {
			return nil, fmt.Errorf("getting auth code: %w", err)
		}
		tok, err = config.Exchange(ctx, code)
		if err != nil {
			return nil, fmt.Errorf("exchanging auth code: %w", err)
		}
		if err := cache.Save(tok); err != nil {
			return nil, fmt.Errorf("caching token: %w", err)
		}
	}
	return config.Client(ctx, tok), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/gmail/... -v`
Expected: PASS — all tests in `internal/gmail` (both `client_test.go` and `auth_test.go`).

- [ ] **Step 5: Commit**

```bash
git add internal/gmail/auth.go internal/gmail/auth_test.go
git commit -m "Add OAuth2 config loading, token cache, and GetClient"
```

---

### Task 5: Root command — Cobra + Viper + slog handler selection

**Files:**
- Modify: `internal/cmd/root.go`
- Test: `internal/cmd/root_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `var RootCmd *cobra.Command`
  - `func Execute() error`
  - `func setupLogger(jsonOutput bool)` (unexported, sets `slog.SetDefault`)

- [ ] **Step 1: Write the failing test**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/... -run 'TestSetupLogger|TestRootCmd_HasJSONFlag' -v`
Expected: FAIL — `setupLogger` undefined, `--json` flag not registered.

- [ ] **Step 3: Implement `internal/cmd/root.go`**

```go
package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var jsonLogs bool

var RootCmd = &cobra.Command{
	Use:   "email-cleanup",
	Short: "Batch operations for Gmail",
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cmd/... -v`
Expected: PASS — all 3 tests.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/root.go internal/cmd/root_test.go
git commit -m "Wire Cobra root command with Viper --json flag and slog handler selection"
```

---

### Task 6: `credentials-help` command

**Files:**
- Create: `internal/cmd/credentials_help.go`
- Test: `internal/cmd/credentials_help_test.go`

**Interfaces:**
- Consumes: `RootCmd` from Task 5 (`internal/cmd/root.go`).
- Produces:
  - `const credentialsHelpText string`
  - `var credentialsHelpCmd *cobra.Command` (registered as `credentials-help` on `RootCmd`)

- [ ] **Step 1: Write the failing test**

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestCredentialsHelpCmd_PrintsManualAndGcloudSteps(t *testing.T) {
	buf := &bytes.Buffer{}
	credentialsHelpCmd.SetOut(buf)
	credentialsHelpCmd.SetArgs([]string{})

	if err := credentialsHelpCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cmd/... -run TestCredentialsHelpCmd -v`
Expected: FAIL — `credentialsHelpCmd` undefined.

- [ ] **Step 3: Implement `internal/cmd/credentials_help.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

const credentialsHelpText = `To use email-cleanup you need a Gmail API OAuth "Desktop app" client,
saved as .credentials/credentials.json.

=== Option A: Google Cloud Console (manual) ===

1. Go to https://console.cloud.google.com/ and create a project (or select
   an existing one).
2. Go to "APIs & Services > Library", search for "Gmail API", and enable it.
3. Go to "APIs & Services > OAuth consent screen". Choose "External", fill
   in the required fields, and add your own Google account under
   "Test users" (keeps the app in Testing mode, no verification needed).
4. Go to "APIs & Services > Credentials", click "Create Credentials >
   OAuth client ID", choose Application type "Desktop app", and create it.
5. Download the client's JSON and save it as:
     .credentials/credentials.json

=== Option B: gcloud CLI (copy-paste; not run automatically by this tool) ===

  gcloud projects create email-cleanup-$RANDOM --name="email-cleanup"
  gcloud config set project <PROJECT_ID_FROM_ABOVE>
  gcloud services enable gmail.googleapis.com

  # The OAuth consent screen and OAuth client ID still require either the
  # Console UI or the (alpha) "gcloud alpha iap oauth-brands" /
  # "oauth-clients" commands, since a Desktop client secret cannot be
  # downloaded non-interactively. After creating the client in the
  # Console, download its JSON and save it as:
  #   .credentials/credentials.json

Once credentials.json is in place, run:
  email-cleanup archive-old-mail --before=YYYY-MM-DD
to trigger the one-time browser consent flow, which caches a token at
.credentials/token.json for future runs.
`

var credentialsHelpCmd = &cobra.Command{
	Use:   "credentials-help",
	Short: "Show steps to obtain a Gmail API credentials.json",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, err := fmt.Fprint(cmd.OutOrStdout(), credentialsHelpText)
		return err
	},
}

func init() {
	RootCmd.AddCommand(credentialsHelpCmd)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cmd/... -v`
Expected: PASS — all tests in `internal/cmd`.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/credentials_help.go internal/cmd/credentials_help_test.go
git commit -m "Add credentials-help command with manual and gcloud instructions"
```

---

### Task 7: `init` command

**Files:**
- Create: `internal/cmd/init.go`
- Test: `internal/cmd/init_test.go`

**Interfaces:**
- Consumes: `credentialsHelpText` from Task 6 (`internal/cmd/credentials_help.go`), `RootCmd` from Task 5.
- Produces:
  - `const credentialsDirName = ".credentials"`
  - `const credentialsFileName = "credentials.json"`
  - `func runInit(w io.Writer, dir string) error` (exported logic used by `initCmd`, directly testable with a temp dir)

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cmd/... -run TestRunInit -v`
Expected: FAIL — `runInit`, `credentialsFileName` undefined.

- [ ] **Step 3: Implement `internal/cmd/init.go`**

```go
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	credentialsDirName  = ".credentials"
	credentialsFileName = "credentials.json"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create the .credentials directory and show setup instructions if needed",
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInit(cmd.OutOrStdout(), credentialsDirName)
	},
}

func runInit(w io.Writer, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	credPath := filepath.Join(dir, credentialsFileName)
	_, err := os.Stat(credPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		fmt.Fprintf(w, "Created %s\n\n", dir)
		fmt.Fprint(w, credentialsHelpText)
		return nil
	case err != nil:
		return fmt.Errorf("checking %s: %w", credPath, err)
	default:
		fmt.Fprintf(w, "%s already exists — nothing to do.\n", credPath)
		return nil
	}
}

func init() {
	RootCmd.AddCommand(initCmd)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cmd/... -v`
Expected: PASS — all tests in `internal/cmd`.

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/init.go internal/cmd/init_test.go
git commit -m "Add init command to create .credentials and show setup help"
```

---

### Task 8: `archive-old-mail` command

**Files:**
- Create: `internal/cmd/archive_old_mail.go`
- Test: `internal/cmd/archive_old_mail_test.go`

**Interfaces:**
- Consumes:
  - `gmail.Service`, `gmail.MessageMeta` from Task 3 (`internal/gmail/client.go`)
  - `journal.Journal`, `journal.RunRecord`, `journal.MessageRecord`, `journal.Open` from Task 2 (`internal/journal/journal.go`)
  - `RootCmd` from Task 5
- Produces:
  - `func buildQuery(cutoff time.Time) string`
  - `func doArchiveRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, logger *slog.Logger, out io.Writer) error`

- [ ] **Step 1: Write the failing tests**

```go
package cmd

import (
	"bytes"
	"context"
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
	archivedIDs  []string
	archiveErr   error
	archiveCalls int
}

func (f *fakeGmailService) Search(ctx context.Context, query string) ([]gmail.MessageMeta, error) {
	return f.matches, nil
}

func (f *fakeGmailService) Archive(ctx context.Context, ids []string) error {
	f.archiveCalls++
	f.archivedIDs = append(f.archivedIDs, ids...)
	return f.archiveErr
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(nil, nil))
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cmd/... -run TestDoArchiveRun -v`
Expected: FAIL — `doArchiveRun`, `buildQuery` undefined.

- [ ] **Step 3: Implement `internal/cmd/archive_old_mail.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/spf13/cobra"

	"email-cleanup/internal/gmail"
	"email-cleanup/internal/journal"
)

var (
	beforeDate string
	applyFlag  bool
)

var archiveOldMailCmd = &cobra.Command{
	Use:   "archive-old-mail",
	Short: "Archive inbox messages older than a given date",
	RunE:  runArchiveOldMail,
}

func init() {
	archiveOldMailCmd.Flags().StringVar(&beforeDate, "before", "", "cutoff date, format YYYY-MM-DD (required)")
	archiveOldMailCmd.Flags().BoolVar(&applyFlag, "apply", false, "actually archive matches (default is dry-run)")
	if err := archiveOldMailCmd.MarkFlagRequired("before"); err != nil {
		panic(err)
	}
	RootCmd.AddCommand(archiveOldMailCmd)
}

func buildQuery(cutoff time.Time) string {
	return fmt.Sprintf("in:inbox before:%s", cutoff.Format("2006/01/02"))
}

func runArchiveOldMail(cmd *cobra.Command, args []string) error {
	cutoff, err := time.Parse("2006-01-02", beforeDate)
	if err != nil {
		return fmt.Errorf("invalid --before date %q, expected YYYY-MM-DD: %w", beforeDate, err)
	}

	credPath := credentialsDirName + "/" + credentialsFileName
	if _, statErr := statCredentials(credPath); statErr != nil {
		return fmt.Errorf("credentials not found at %s — run `email-cleanup init` or `email-cleanup credentials-help`: %w", credPath, statErr)
	}

	svc, err := newRealGmailService(cmd.Context())
	if err != nil {
		return fmt.Errorf("setting up Gmail client: %w", err)
	}

	jrnl, err := journal.Open(".history/journal.jsonl")
	if err != nil {
		return fmt.Errorf("opening journal: %w", err)
	}

	query := buildQuery(cutoff)
	return doArchiveRun(cmd.Context(), svc, jrnl, query, cmd.Flags().Args(), applyFlag, slog.Default(), cmd.OutOrStdout())
}

func doArchiveRun(ctx context.Context, svc gmail.Service, jrnl *journal.Journal, query string, args []string, apply bool, logger *slog.Logger, out io.Writer) error {
	runID := newRunID()
	start := time.Now()

	matches, err := svc.Search(ctx, query)
	if err != nil {
		return fmt.Errorf("searching messages: %w", err)
	}
	logger.Info("search complete", "matched", len(matches), "query", query)

	action := "would_archive"
	var affected int
	if apply && len(matches) > 0 {
		ids := make([]string, len(matches))
		for i, m := range matches {
			ids[i] = m.ID
		}
		if err := svc.Archive(ctx, ids); err != nil {
			return fmt.Errorf("archiving messages: %w", err)
		}
		action = "archived"
		affected = len(matches)
	}

	for _, m := range matches {
		if err := jrnl.WriteMessage(journal.MessageRecord{
			RunID:     runID,
			MessageID: m.ID,
			Subject:   m.Subject,
			From:      m.From,
			Date:      m.Date,
			Action:    action,
		}); err != nil {
			return fmt.Errorf("writing journal message record: %w", err)
		}
	}

	if err := jrnl.WriteRun(journal.RunRecord{
		RunID:         runID,
		Timestamp:     start,
		Command:       "archive-old-mail",
		Args:          args,
		Query:         query,
		DryRun:        !apply,
		MatchedCount:  len(matches),
		AffectedCount: affected,
		DurationMS:    time.Since(start).Milliseconds(),
		Status:        "ok",
	}); err != nil {
		return fmt.Errorf("writing journal run record: %w", err)
	}

	if apply {
		fmt.Fprintf(out, "Archived %d message(s).\n", affected)
		fmt.Fprintln(out, "Reminder: run `task commit-history` to commit and push the updated journal.")
	} else {
		fmt.Fprintf(out, "Dry run: %d message(s) would be archived. Re-run with --apply to archive them.\n", len(matches))
	}
	return nil
}

func newRunID() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z")
}
```

Note: `statCredentials` and `newRealGmailService` are small wiring helpers that use `os.Stat`, `internal/gmail.LoadConfig`, `internal/gmail.GetClient`, and `gmailapi.NewService` to build a real `gmail.Service` for `runArchiveOldMail`. They are not unit-tested (they require real files/network); add them now as straightforward wiring:

```go
func statCredentials(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func newRealGmailService(ctx context.Context) (gmail.Service, error) {
	credBytes, err := os.ReadFile(credentialsDirName + "/" + credentialsFileName)
	if err != nil {
		return nil, fmt.Errorf("reading credentials.json: %w", err)
	}
	config, err := gmail.LoadConfig(credBytes)
	if err != nil {
		return nil, err
	}
	cache := gmail.NewTokenCache(credentialsDirName + "/token.json")
	httpClient, err := gmail.GetClient(ctx, config, cache, promptForAuthCode)
	if err != nil {
		return nil, err
	}
	apiSvc, err := gmailapi.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("creating gmail api service: %w", err)
	}
	return gmail.NewAPIService(apiSvc), nil
}

func promptForAuthCode(authURL string) (string, error) {
	fmt.Printf("Go to the following link in your browser, then paste the authorization code:\n%s\n\nCode: ", authURL)
	var code string
	if _, err := fmt.Scan(&code); err != nil {
		return "", fmt.Errorf("reading auth code: %w", err)
	}
	return code, nil
}
```

Add the two extra imports this requires (`os`, `google.golang.org/api/gmail/v1` as `gmailapi`, `google.golang.org/api/option`) to the top of `internal/cmd/archive_old_mail.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cmd/... -v`
Expected: PASS — all tests in `internal/cmd`, including `TestBuildQuery` and all `TestDoArchiveRun_*` cases.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: PASS across `internal/journal`, `internal/gmail`, `internal/cmd`.

- [ ] **Step 6: Commit**

```bash
git add internal/cmd/archive_old_mail.go internal/cmd/archive_old_mail_test.go
git commit -m "Add archive-old-mail command with dry-run, apply, and journal writes"
```

---

### Task 9: Build verification and manual smoke test

**Files:**
- None created — verification only.

**Interfaces:**
- Consumes: everything from Tasks 1–8.

- [ ] **Step 1: Build the binary**

Run: `task build`
Expected: exits 0, produces `bin/email-cleanup`.

- [ ] **Step 2: Verify `--help` lists all subcommands**

Run: `./bin/email-cleanup --help`
Expected: output lists `init`, `credentials-help`, `archive-old-mail`, and the global `--json` flag.

- [ ] **Step 3: Verify `credentials-help` runs standalone**

Run: `./bin/email-cleanup credentials-help`
Expected: prints both the manual Console steps and the `gcloud` commands, no error.

- [ ] **Step 4: Verify `init` is idempotent**

Run: `./bin/email-cleanup init && ./bin/email-cleanup init`
Expected: first run creates `.credentials/` and prints setup instructions; second run prints "already exists" style message (since `credentials.json` still won't exist yet, both runs will actually print instructions — that's fine, since the dir isn't the gate, the file is; verify no error either way).

- [ ] **Step 5: Verify `archive-old-mail` fails cleanly without credentials**

Run: `./bin/email-cleanup archive-old-mail --before=2026-08-01`
Expected: non-zero exit, error message pointing at `init` / `credentials-help`, since `.credentials/credentials.json` does not exist yet in a fresh checkout.

- [ ] **Step 6: Commit (only if any fixes were needed in prior steps)**

If steps 1–5 required code changes, stage and commit them with a message describing the fix. If everything passed as implemented, no commit is needed for this task.

---

### Task 10: `README.md` and `CLAUDE.md`

**Files:**
- Create: `README.md`
- Create: `CLAUDE.md`

**Interfaces:**
- Consumes: final command names/flags from Tasks 5–8 (`init`, `credentials-help`, `archive-old-mail --before --apply --json`), Taskfile targets from Task 1.

- [ ] **Step 1: Write `README.md`**

```markdown
# email-cleanup

Batch Gmail operations, run from the command line instead of the Gmail UI.

## Setup

1. Install Go 1.21+ and [Task](https://taskfile.dev/installation/).
2. Get a Gmail API OAuth "Desktop app" client:
   ```bash
   task run -- credentials-help
   ```
   Follow either the manual Google Cloud Console steps or the `gcloud`
   command list it prints, then save the downloaded JSON as
   `.credentials/credentials.json`.
3. Initialize the credentials directory (safe to run any time):
   ```bash
   task run -- init
   ```
4. Build the binary:
   ```bash
   task build
   ```

The first command that talks to Gmail opens a browser consent flow and
caches the resulting token at `.credentials/token.json`. `.credentials/` is
gitignored — never commit it.

## Usage

### Archive old inbox mail

```bash
# Dry run (default) — shows what would be archived, changes nothing
./bin/email-cleanup archive-old-mail --before=2026-08-01

# Actually archive matches
./bin/email-cleanup archive-old-mail --before=2026-08-01 --apply
```

`--before` accepts `YYYY-MM-DD` and matches Gmail's `in:inbox before:...`
search semantics. Re-running is safe — once a message is archived it no
longer matches `in:inbox`.

### Machine-readable logs

Add `--json` to any command to switch logging from human-friendly text to
JSON lines on stderr:

```bash
./bin/email-cleanup archive-old-mail --before=2026-08-01 --json
```

### Journal

Every `archive-old-mail` run (dry-run or apply) appends records to
`.history/journal.jsonl` — one `run` record plus one `message` record per
affected message. This file is committed to git as an audit trail. After a
run that mutates the mailbox, commit and push it:

```bash
task commit-history
```

## Development

```bash
task test    # run the test suite
task fmt     # gofmt
task vet     # go vet
task tidy    # go mod tidy
```

## Project Layout

Follows [golang-standards/project-layout](https://github.com/golang-standards/project-layout):
- `cmd/email-cleanup/` — CLI entrypoint
- `internal/cmd/` — Cobra subcommands
- `internal/gmail/` — Gmail API client, OAuth2 auth
- `internal/journal/` — JSONL run/message journal
```

- [ ] **Step 2: Write `CLAUDE.md`**

```markdown
# CLAUDE.md

Guidance for working in this repository.

## What this is

A Go CLI (`email-cleanup`) for batch Gmail operations, run locally instead
of through the Gmail UI. First command: `archive-old-mail`, which archives
inbox messages older than a given date.

## Layout

Follows golang-standards/project-layout:
- `cmd/email-cleanup/main.go` — entrypoint, delegates to `internal/cmd.Execute()`
- `internal/cmd/` — one file per Cobra subcommand (`root.go`, `init.go`,
  `credentials_help.go`, `archive_old_mail.go`)
- `internal/gmail/` — `Service` interface + `APIService` (real Gmail API
  implementation) in `client.go`; OAuth2 config/token handling in `auth.go`
- `internal/journal/` — append-only JSONL journal (`RunRecord`,
  `MessageRecord`)

## Conventions

- CLI: Cobra for commands, Viper for flag binding.
- Logging: `log/slog` only. Human-friendly text handler by default; the
  global `--json` flag switches to `slog.NewJSONHandler`. Set once in
  `RootCmd.PersistentPreRun`.
- Gmail access always goes through the `gmail.Service` interface
  (`internal/gmail/client.go`), never called directly from `internal/cmd`,
  so command logic can be unit-tested against a fake.
- Mutating commands default to dry-run; an explicit `--apply` flag is
  required to actually change the mailbox.
- `.credentials/` (OAuth client + cached token) is gitignored, never
  committed. `.history/journal.jsonl` is committed — it holds no secrets,
  only a record of actions and affected message metadata.
- Any command that mutates the mailbox prints a reminder to run
  `task commit-history` afterwards.

## Build / test

```bash
task build   # go build -o bin/email-cleanup ./cmd/email-cleanup
task test    # go test ./...
task fmt     # go fmt ./...
task vet     # go vet ./...
task tidy    # go mod tidy
task run -- <subcommand> [flags]
```

## Adding a new batch operation

1. Add a new file in `internal/cmd/` for the subcommand, following the
   pattern in `archive_old_mail.go` (separate the Cobra `RunE` wiring from
   a pure, testable `doXxxRun` function that takes a `gmail.Service` and a
   `*journal.Journal`).
2. Extend `gmail.Service` in `internal/gmail/client.go` if the new
   operation needs a Gmail API capability not already exposed.
3. Write journal records for the new operation so it stays covered by the
   audit trail.
4. Register the command on `RootCmd` in the new file's `init()`.
```

- [ ] **Step 3: Commit**

```bash
git add README.md CLAUDE.md
git commit -m "Add README and CLAUDE.md"
```

---

## Self-Review Notes

- **Spec coverage:** `init` (Task 7), `credentials-help` with manual+gcloud paths (Task 6), `archive-old-mail` dry-run/apply/journal/reminder (Task 8), Cobra/Viper/slog/--json (Task 5), journal JSONL with run+message records committed to git (Task 2), Taskfile including `commit-history` (Task 1), README/CLAUDE.md (Task 10) — all covered.
- **Type consistency:** `gmail.MessageMeta{ID, Subject, From, Date}` used identically in `client.go` and `archive_old_mail.go`; `journal.RunRecord`/`journal.MessageRecord` field names match between `journal.go` and `archive_old_mail.go`; `gmail.Service` interface signature matches both `APIService` (Task 3) and `fakeGmailService` in tests (Task 8).
- **No placeholders:** all steps contain full code; wiring helpers (`statCredentials`, `newRealGmailService`, `promptForAuthCode`) are spelled out in full in Task 8 rather than deferred.
