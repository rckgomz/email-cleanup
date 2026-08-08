package gmail

import (
	"context"
	"fmt"
	"log/slog"

	gmailapi "google.golang.org/api/gmail/v1"
)

const progressLogInterval = 25

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
			if len(results)%progressLogInterval == 0 {
				slog.Default().Info("fetching message details", "fetched", len(results))
			}
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
	if len(ids) == 0 || size <= 0 {
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
