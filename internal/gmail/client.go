package gmail

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
	"google.golang.org/api/googleapi"

	gmailapi "google.golang.org/api/gmail/v1"
)

const progressLogInterval = 25

const batchModifyLimit = 1000

// listPageSize is the max messages.list page size Gmail allows, used to
// minimize the number of sequential list calls needed for large mailboxes.
const listPageSize = 500

// searchConcurrency bounds how many messages.get calls run at once during
// Search.
const searchConcurrency = 15

// gmailRequestsPerSecond throttles every Gmail API call (list, get,
// batchModify) to stay under the default per-user quota (15000 units/min;
// list and get each cost 5 units), leaving headroom for batchModify calls
// and other quota consumers. At this rate a very large mailbox takes
// longer to search, but won't blow through the quota mid-run. If this is
// too conservative for your account's actual quota, request a higher
// limit at https://cloud.google.com/docs/quotas/help/request_increase.
const gmailRequestsPerSecond = 30

// maxRateLimitRetries bounds how many times a single call is retried after
// a quota/rate-limit error before giving up.
const maxRateLimitRetries = 6

// backoffSleep is overridable in tests so retry tests don't take real
// wall-clock backoff time.
var backoffSleep = func(ctx context.Context, d time.Duration) error {
	select {
	case <-time.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

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
	// RemoveLabel removes the given Gmail label ID (e.g. "IMPORTANT") from
	// each message in ids.
	RemoveLabel(ctx context.Context, ids []string, label string) error
}

type APIService struct {
	svc     *gmailapi.Service
	limiter *rate.Limiter
}

func NewAPIService(svc *gmailapi.Service) *APIService {
	return &APIService{
		svc:     svc,
		limiter: rate.NewLimiter(rate.Limit(gmailRequestsPerSecond), searchConcurrency),
	}
}

// withRateLimitRetry paces fn via limiter and, if fn fails with a Gmail
// rate-limit/quota error, retries with exponential backoff (plus jitter)
// up to maxRateLimitRetries times. Non-rate-limit errors are returned
// immediately without retry.
func withRateLimitRetry(ctx context.Context, limiter *rate.Limiter, fn func() error) error {
	var lastErr error
	for attempt := 0; attempt <= maxRateLimitRetries; attempt++ {
		if err := limiter.Wait(ctx); err != nil {
			return err
		}

		err := fn()
		if err == nil {
			return nil
		}
		if !isRateLimitError(err) {
			return err
		}
		lastErr = err
		if attempt == maxRateLimitRetries {
			break
		}

		backoff := time.Duration(1<<attempt) * time.Second
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
		wait := backoff + time.Duration(rand.Int63n(int64(backoff)/2+1))
		slog.Default().Warn("gmail API rate limit hit, backing off", "attempt", attempt+1, "wait", wait)
		if err := backoffSleep(ctx, wait); err != nil {
			return err
		}
	}
	return fmt.Errorf("exceeded retries after rate limiting: %w", lastErr)
}

func isRateLimitError(err error) bool {
	var gErr *googleapi.Error
	if !errors.As(err, &gErr) {
		return false
	}
	if gErr.Code == 429 {
		return true
	}
	if gErr.Code != 403 {
		return false
	}
	for _, item := range gErr.Errors {
		reason := strings.ToLower(item.Reason)
		if strings.Contains(reason, "ratelimitexceeded") || strings.Contains(reason, "quotaexceeded") || strings.Contains(reason, "rate_limit_exceeded") {
			return true
		}
	}
	msg := strings.ToLower(gErr.Message + " " + gErr.Body)
	return strings.Contains(msg, "rate_limit_exceeded") || strings.Contains(msg, "quota exceeded") || strings.Contains(msg, "ratelimitexceeded")
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
		var resp *gmailapi.ListMessagesResponse
		err := withRateLimitRetry(ctx, a.limiter, func() error {
			call := a.svc.Users.Messages.List("me").Q(query).MaxResults(listPageSize).Context(ctx)
			if pageToken != "" {
				call = call.PageToken(pageToken)
			}
			r, err := call.Do()
			if err != nil {
				return err
			}
			resp = r
			return nil
		})
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
			var full *gmailapi.Message
			err := withRateLimitRetry(gctx, a.limiter, func() error {
				m, err := a.svc.Users.Messages.Get("me", id).
					Format("metadata").
					MetadataHeaders("Subject", "From", "Date").
					Context(gctx).Do()
				if err != nil {
					return err
				}
				full = m
				return nil
			})
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
	return a.modifyLabels(ctx, ids, "INBOX")
}

// RemoveLabel removes labelID from each message in ids, e.g. "IMPORTANT"
// to unmark messages as important.
func (a *APIService) RemoveLabel(ctx context.Context, ids []string, labelID string) error {
	return a.modifyLabels(ctx, ids, labelID)
}

// modifyLabels removes labelID from each message in ids, in chunks of up
// to batchModifyLimit, respecting the rate limiter and retrying on
// transient quota errors. Shared by Archive (labelID "INBOX") and
// RemoveLabel (any other label).
func (a *APIService) modifyLabels(ctx context.Context, ids []string, labelID string) error {
	for _, chunk := range chunkIDs(ids, batchModifyLimit) {
		req := &gmailapi.BatchModifyMessagesRequest{
			Ids:            chunk,
			RemoveLabelIds: []string{labelID},
		}
		err := withRateLimitRetry(ctx, a.limiter, func() error {
			return a.svc.Users.Messages.BatchModify("me", req).Context(ctx).Do()
		})
		if err != nil {
			return fmt.Errorf("removing label %s from batch of %d messages: %w", labelID, len(chunk), err)
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
