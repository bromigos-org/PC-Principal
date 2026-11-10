package memory

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxEventBatchSize is the most events gnosis accepts in one request.
	MaxEventBatchSize = 100
	// DefaultEventBatchSize keeps one request short. gnosis handles the
	// events of a batch one after another, so a smaller batch finishes
	// sooner and less is in doubt when a request fails.
	DefaultEventBatchSize     = 25
	defaultBatchMaxAttempts   = 3
	defaultBatchRetryBackoff  = 2 * time.Second
	maxBatchRetryBackoffSteps = 6
)

// EventIngester is the part of Client that batch ingestion needs.
type EventIngester interface {
	IngestEvents(ctx context.Context, events []ClientEvent) (ClientEventBatchResponse, error)
}

// BatchIngestConfig controls IngestEventsInBatches. Zero values take the
// defaults.
type BatchIngestConfig struct {
	// BatchSize is the number of events per request, at most
	// MaxEventBatchSize.
	BatchSize int
	// MaxAttempts is how often one batch is sent before giving up.
	MaxAttempts int
	// Backoff is the wait before the second attempt. It doubles for every
	// attempt after that.
	Backoff time.Duration
	// RequestTimeout bounds each attempt when set. Zero leaves the bound to
	// the client, which allows a batch more time the larger it is.
	RequestTimeout time.Duration
}

func (c BatchIngestConfig) withDefaults() BatchIngestConfig {
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultEventBatchSize
	}
	if c.BatchSize > MaxEventBatchSize {
		c.BatchSize = MaxEventBatchSize
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaultBatchMaxAttempts
	}
	if c.Backoff <= 0 {
		c.Backoff = defaultBatchRetryBackoff
	}
	return c
}

// BatchIngestSummary reports what IngestEventsInBatches got through.
type BatchIngestSummary struct {
	// Events is the number of events in batches that succeeded.
	Events int
	// Accepted and Duplicates count the per-event answers from gnosis. A
	// duplicate is an event gnosis already had under the same idempotency
	// key, which is the normal answer when a batch is sent again.
	Accepted   int
	Duplicates int
	// Batches is the number of batches that succeeded.
	Batches int
	// Retries is the number of extra attempts that were needed.
	Retries int
}

// IngestEventsInBatches sends events to gnosis in batches, in order.
//
// A batch that fails for a reason that may pass (a timeout, a server error,
// a network error) is sent again after a growing wait, up to
// config.MaxAttempts times. Sending a batch again is safe: every event
// carries an idempotency key, and gnosis answers "duplicate" for the events
// of an earlier attempt that did land, even though the client had given up
// on that attempt.
//
// It stops at the first batch that cannot be delivered and returns the error
// with the summary of what went through before it. The remaining batches are
// not sent, so a struggling gnosis is not handed more work.
func IngestEventsInBatches(ctx context.Context, client EventIngester, events []ClientEvent, config BatchIngestConfig) (BatchIngestSummary, error) {
	config = config.withDefaults()
	var summary BatchIngestSummary
	total := (len(events) + config.BatchSize - 1) / config.BatchSize
	for start := 0; start < len(events); start += config.BatchSize {
		batch := events[start:min(start+config.BatchSize, len(events))]
		response, retries, err := ingestBatchWithRetry(ctx, client, batch, config)
		summary.Retries += retries
		if err != nil {
			return summary, fmt.Errorf("batch %d of %d: %w", summary.Batches+1, total, err)
		}
		summary.Batches++
		summary.Events += len(batch)
		for _, result := range response.Results {
			switch result.Status {
			case EventIngestStatusAccepted:
				summary.Accepted++
			case EventIngestStatusDuplicate:
				summary.Duplicates++
			}
		}
	}
	return summary, nil
}

func ingestBatchWithRetry(ctx context.Context, client EventIngester, batch []ClientEvent, config BatchIngestConfig) (ClientEventBatchResponse, int, error) {
	retries := 0
	for attempt := 1; ; attempt++ {
		response, err := ingestBatchOnce(ctx, client, batch, config.RequestTimeout)
		if err == nil || !IsTransient(err) || attempt >= config.MaxAttempts {
			return response, retries, err
		}
		if waitErr := sleep(ctx, retryBackoff(config.Backoff, attempt)); waitErr != nil {
			return response, retries, waitErr
		}
		retries++
	}
}

func ingestBatchOnce(ctx context.Context, client EventIngester, batch []ClientEvent, timeout time.Duration) (ClientEventBatchResponse, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return client.IngestEvents(ctx, batch)
}

// retryBackoff is the wait after the given failed attempt: the base wait,
// doubled for each attempt after the first.
func retryBackoff(base time.Duration, attempt int) time.Duration {
	return base << min(attempt-1, maxBatchRetryBackoffSteps)
}

// IsTransient reports whether a failed gnosis call is worth sending again:
// timeouts, server errors and network errors are, while a request gnosis
// refused (unauthorized, invalid, partly rejected) is not. An error that did
// not come from the client counts as transient.
func IsTransient(err error) bool {
	var memoryErr *Error
	if errors.As(err, &memoryErr) {
		return memoryErr.Kind == ErrorKindTimeout || memoryErr.Kind == ErrorKindServer || memoryErr.Kind == ErrorKindUnexpected
	}
	return true
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
