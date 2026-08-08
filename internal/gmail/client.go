package gmail

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
	gmailapi "google.golang.org/api/gmail/v1"
)

const progressLogInterval = 25

const batchModifyLimit = 1000

// listPageSize is the max messages.list page size Gmail allows, used to
// minimize the number of sequential list calls needed for large mailboxes.
const listPageSize = 500

// searchConcurrency bounds how many messages.get calls run at once during
// Search. Gmail's per-user quota is ~250 units/sec and each get costs 5
// units, so this stays well under that even accounting for other API
// activity, without needing explicit rate-limit retry/backoff.
const searchConcurrency = 15

type MessageMeta struct {
	ID      string
	Subject string
	From    string
	Date    string
}

type Service interface {
	// Search returns messages matching query. If limit > 0, it stops once
	// limit messages have been fetched, issuing no further API calls.
	Search(ctx context.Context, query string, limit int) ([]MessageMeta, error)
	Archive(ctx context.Context, ids []string) error
}

type APIService struct {
	svc *gmailapi.Service
}

func NewAPIService(svc *gmailapi.Service) *APIService {
	return &APIService{svc: svc}
}

func (a *APIService) Search(ctx context.Context, query string, limit int) ([]MessageMeta, error) {
	ids, err := a.listMessageIDs(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	return a.fetchMessageMetas(ctx, ids)
}

// listMessageIDs paginates through messages.list, which only returns IDs
// (no headers), so this is cheap even for large result sets. Stops early
// once limit IDs are collected, if limit > 0.
func (a *APIService) listMessageIDs(ctx context.Context, query string, limit int) ([]string, error) {
	var ids []string
	pageToken := ""
	for {
		call := a.svc.Users.Messages.List("me").Q(query).MaxResults(listPageSize).Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("listing messages: %w", err)
		}
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
			if limit > 0 && len(ids) >= limit {
				return ids, nil
			}
		}
		slog.Default().Info("listing messages", "listed", len(ids))
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return ids, nil
}

// fetchMessageMetas fetches Subject/From/Date for each id concurrently,
// bounded by searchConcurrency, since Gmail's API requires one call per
// message to get headers. Order of the returned slice matches ids. On the
// first error, in-flight and not-yet-started fetches are cancelled and
// that error is returned.
func (a *APIService) fetchMessageMetas(ctx context.Context, ids []string) ([]MessageMeta, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	results := make([]MessageMeta, len(ids))
	var fetched atomic.Int64

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(searchConcurrency)

	for i, id := range ids {
		g.Go(func() error {
			full, err := a.svc.Users.Messages.Get("me", id).
				Format("metadata").
				MetadataHeaders("Subject", "From", "Date").
				Context(gctx).Do()
			if err != nil {
				return fmt.Errorf("getting message %s: %w", id, err)
			}
			results[i] = messageMetaFromAPI(full)
			if n := fetched.Add(1); n%progressLogInterval == 0 {
				slog.Default().Info("fetching message details", "fetched", n, "total", len(ids))
			}
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
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
