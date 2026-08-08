package gmail

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/api/option"

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
		{"zero size", []string{"a", "b"}, 0, nil},
		{"negative size", []string{"a", "b"}, -1, nil},
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

// fakeGmailServer serves the Gmail v1 REST endpoints Search needs
// (messages.list and messages.get) and tracks how many messages.get
// requests are in flight concurrently, so tests can assert on real
// concurrent behavior rather than mocking it away.
type fakeGmailServer struct {
	ids         []string
	failID      string
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
}

func newFakeGmailServer(t *testing.T, ids []string) (*httptest.Server, *fakeGmailServer) {
	t.Helper()
	f := &fakeGmailServer{ids: ids}
	mux := http.NewServeMux()

	mux.HandleFunc("/gmail/v1/users/me/messages", func(w http.ResponseWriter, r *http.Request) {
		resp := gmailapi.ListMessagesResponse{}
		for _, id := range f.ids {
			resp.Messages = append(resp.Messages, &gmailapi.Message{Id: id})
		}
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Errorf("encoding list response: %v", err)
		}
	})

	mux.HandleFunc("/gmail/v1/users/me/messages/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/gmail/v1/users/me/messages/")
		if f.failID != "" && id == f.failID {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}

		cur := f.inFlight.Add(1)
		defer f.inFlight.Add(-1)
		for {
			max := f.maxInFlight.Load()
			if cur <= max {
				break
			}
			if f.maxInFlight.CompareAndSwap(max, cur) {
				break
			}
		}

		msg := gmailapi.Message{
			Id: id,
			Payload: &gmailapi.MessagePart{
				Headers: []*gmailapi.MessagePartHeader{
					{Name: "Subject", Value: "Subject-" + id},
				},
			},
		}
		if err := json.NewEncoder(w).Encode(msg); err != nil {
			t.Errorf("encoding get response: %v", err)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, f
}

func newTestAPIService(t *testing.T, server *httptest.Server) *APIService {
	t.Helper()
	svc, err := gmailapi.NewService(context.Background(),
		option.WithEndpoint(server.URL),
		option.WithHTTPClient(server.Client()),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("gmailapi.NewService() error = %v", err)
	}
	return NewAPIService(svc)
}

func TestAPIServiceSearch_FetchesConcurrentlyAndPreservesOrder(t *testing.T) {
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("msg-%02d", i)
	}
	server, fake := newFakeGmailServer(t, ids)
	svc := newTestAPIService(t, server)

	got, err := svc.Search(context.Background(), "in:inbox", 0)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if len(got) != len(ids) {
		t.Fatalf("len(got) = %d, want %d", len(got), len(ids))
	}
	for i, meta := range got {
		if meta.ID != ids[i] {
			t.Errorf("got[%d].ID = %q, want %q (order not preserved)", i, meta.ID, ids[i])
		}
		if meta.Subject != "Subject-"+ids[i] {
			t.Errorf("got[%d].Subject = %q, want %q", i, meta.Subject, "Subject-"+ids[i])
		}
	}

	if max := fake.maxInFlight.Load(); max <= 1 {
		t.Errorf("maxInFlight = %d, want > 1 (fetches should run concurrently)", max)
	}
	if max := fake.maxInFlight.Load(); max > searchConcurrency {
		t.Errorf("maxInFlight = %d, want <= searchConcurrency (%d)", max, searchConcurrency)
	}
}

func TestAPIServiceSearch_RespectsLimit(t *testing.T) {
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("msg-%02d", i)
	}
	server, _ := newFakeGmailServer(t, ids)
	svc := newTestAPIService(t, server)

	got, err := svc.Search(context.Background(), "in:inbox", 5)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("len(got) = %d, want 5", len(got))
	}
}

func TestAPIServiceSearch_OneFailureCancelsRestAndReturnsError(t *testing.T) {
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = fmt.Sprintf("msg-%02d", i)
	}
	server, fake := newFakeGmailServer(t, ids)
	fake.failID = "msg-20"
	svc := newTestAPIService(t, server)

	got, err := svc.Search(context.Background(), "in:inbox", 0)
	if err == nil {
		t.Fatal("Search() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "msg-20") {
		t.Errorf("Search() error = %v, want it to mention msg-20", err)
	}
	if got != nil {
		t.Errorf("Search() results = %v, want nil on error", got)
	}
}

func TestAPIServiceSearch_NoMatches(t *testing.T) {
	server, _ := newFakeGmailServer(t, nil)
	svc := newTestAPIService(t, server)

	got, err := svc.Search(context.Background(), "in:inbox", 0)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestAPIServiceSearch_ResultsAreASortedSupersetOfIDs(t *testing.T) {
	// Sanity check that concurrent fetches don't drop or duplicate ids
	// even though they complete out of order.
	ids := make([]string, 23)
	for i := range ids {
		ids[i] = fmt.Sprintf("msg-%02d", i)
	}
	server, _ := newFakeGmailServer(t, ids)
	svc := newTestAPIService(t, server)

	got, err := svc.Search(context.Background(), "in:inbox", 0)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	gotIDs := make([]string, len(got))
	for i, m := range got {
		gotIDs[i] = m.ID
	}
	sort.Strings(gotIDs)
	wantIDs := make([]string, len(ids))
	copy(wantIDs, ids)
	sort.Strings(wantIDs)
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("got ids = %v, want %v", gotIDs, wantIDs)
	}
}
