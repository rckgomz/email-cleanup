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
