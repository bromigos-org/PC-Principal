package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

// fakeGnosis stands in for the gnosis event API. Like the real service it
// keeps one record per idempotency key, answers "duplicate" for a key it
// already has, and finishes a request even when the caller has stopped
// waiting for the answer.
type fakeGnosis struct {
	t      *testing.T
	server *httptest.Server
	// respond decides what request number n (from 1) does. landBefore is how
	// long the events take to be stored and answerAfter how long the answer
	// is held back after that. A status other than 200 stores nothing.
	respond func(n int) (status int, landBefore time.Duration, answerAfter time.Duration)

	mu         sync.Mutex
	landed     map[string]int
	batchSizes []int
	arrivals   []time.Time
}

func newFakeGnosis(t *testing.T, respond func(n int) (int, time.Duration, time.Duration)) *fakeGnosis {
	t.Helper()
	gnosis := &fakeGnosis{t: t, respond: respond, landed: map[string]int{}}
	gnosis.server = httptest.NewServer(http.HandlerFunc(gnosis.serve))
	t.Cleanup(gnosis.server.Close)
	return gnosis
}

func (g *fakeGnosis) serve(w http.ResponseWriter, r *http.Request) {
	var body ClientEventBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		g.t.Errorf("decode event batch: %v", err)
	}
	g.mu.Lock()
	g.arrivals = append(g.arrivals, time.Now())
	g.batchSizes = append(g.batchSizes, len(body.Events))
	number := len(g.arrivals)
	g.mu.Unlock()

	status, landBefore, answerAfter := http.StatusOK, time.Duration(0), time.Duration(0)
	if g.respond != nil {
		status, landBefore, answerAfter = g.respond(number)
	}
	if status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	time.Sleep(landBefore)
	results := make([]EventIngestResult, 0, len(body.Events))
	g.mu.Lock()
	for _, event := range body.Events {
		result := EventIngestResult{EventID: event.EventID, Status: EventIngestStatusAccepted}
		if g.landed[event.IdempotencyKey] > 0 {
			result.Status = EventIngestStatusDuplicate
		} else {
			g.landed[event.IdempotencyKey]++
		}
		results = append(results, result)
	}
	g.mu.Unlock()
	time.Sleep(answerAfter)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ClientEventBatchResponse{Results: results})
}

func (g *fakeGnosis) requests() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.arrivals)
}

// client returns a client for the fake with short timeouts: base for one
// request, plus perEvent for each further event of a batch.
func (g *fakeGnosis) client(base time.Duration, perEvent time.Duration) *HTTPClient {
	client := NewClient(Config{Enabled: true, BaseURL: g.server.URL, Token: "test-token"}, nil)
	client.requestTimeout = base
	client.batchTimeoutPerEvent = perEvent
	return client
}

func testEvents(count int) []ClientEvent {
	events := make([]ClientEvent, 0, count)
	for id := 1; id <= count; id++ {
		event := testClientEvent()
		event.EventID = "event-" + strconv.Itoa(id)
		event.IdempotencyKey = "message_created:" + strconv.Itoa(id)
		events = append(events, event)
	}
	return events
}

func assertTimeout(t *testing.T, err error) {
	t.Helper()
	var memoryErr *Error
	if !errors.As(err, &memoryErr) || memoryErr.Kind != ErrorKindTimeout {
		t.Fatalf("error = %T %[1]v, want a timeout", err)
	}
}

// The failure this guards against: gnosis needed longer for a batch than the
// client's one short timeout, so every batch was abandoned while gnosis was
// still working on it.
func TestIngestEvents_Batch_is_allowed_more_time_than_a_single_request(t *testing.T) {
	gnosis := newFakeGnosis(t, func(int) (int, time.Duration, time.Duration) {
		return http.StatusOK, 400 * time.Millisecond, 0
	})
	client := gnosis.client(100*time.Millisecond, 100*time.Millisecond)

	// Ten events may take 100ms + 9 x 100ms. The answer arrives after 400ms.
	response, err := client.IngestEvents(context.Background(), testEvents(10))
	if err != nil {
		t.Fatalf("batch of ten: %v", err)
	}
	if len(response.Results) != 10 {
		t.Fatalf("results = %d, want 10", len(response.Results))
	}

	// One event keeps the short timeout, as every other call does.
	started := time.Now()
	_, err = client.IngestEvents(context.Background(), testEvents(1))
	assertTimeout(t, err)
	if elapsed := time.Since(started); elapsed > 350*time.Millisecond {
		t.Fatalf("single event gave up after %s, want about 100ms", elapsed)
	}
}

func TestEventBatchTimeout_grows_with_the_batch_and_is_capped(t *testing.T) {
	client := NewClient(Config{}, nil)
	cases := map[int]time.Duration{
		0:   5 * time.Second,
		1:   5 * time.Second,
		2:   6 * time.Second,
		25:  29 * time.Second,
		100: 104 * time.Second,
		500: 2 * time.Minute,
	}
	for events, want := range cases {
		if got := client.eventBatchTimeout(events); got != want {
			t.Errorf("eventBatchTimeout(%d) = %s, want %s", events, got, want)
		}
	}
}

func TestNewClient_Other_calls_keep_the_short_timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(400 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, nil)
	client.requestTimeout = 100 * time.Millisecond

	started := time.Now()
	_, err := client.GetGraphContext(context.Background(), GraphContextRequest{Scope: testScope(), Query: "q"})

	assertTimeout(t, err)
	if elapsed := time.Since(started); elapsed > 350*time.Millisecond {
		t.Fatalf("graph context gave up after %s, want about 100ms", elapsed)
	}
}

// A batch whose answer comes too late did land. Sending it again must not
// store anything twice, and must count as delivered.
func TestIngestEventsInBatches_Retry_after_a_timeout_does_not_ingest_twice(t *testing.T) {
	gnosis := newFakeGnosis(t, func(n int) (int, time.Duration, time.Duration) {
		if n == 1 {
			return http.StatusOK, 0, 600 * time.Millisecond
		}
		return http.StatusOK, 0, 0
	})
	// Five events may take 100ms + 4 x 25ms = 200ms.
	client := gnosis.client(100*time.Millisecond, 25*time.Millisecond)

	summary, err := IngestEventsInBatches(context.Background(), client, testEvents(10), BatchIngestConfig{BatchSize: 5, MaxAttempts: 3, Backoff: 10 * time.Millisecond})

	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if want := (BatchIngestSummary{Events: 10, Accepted: 5, Duplicates: 5, Batches: 2, Retries: 1}); summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
	if got := gnosis.requests(); got != 3 {
		t.Fatalf("requests = %d, want 3: the first batch twice, the second once", got)
	}
	gnosis.mu.Lock()
	defer gnosis.mu.Unlock()
	if len(gnosis.landed) != 10 {
		t.Fatalf("stored events = %d, want 10", len(gnosis.landed))
	}
	for key, count := range gnosis.landed {
		if count != 1 {
			t.Fatalf("event %s was stored %d times, want once", key, count)
		}
	}
}

func TestIngestEventsInBatches_Request_timeout_overrides_the_time_a_batch_would_get(t *testing.T) {
	gnosis := newFakeGnosis(t, func(int) (int, time.Duration, time.Duration) {
		return http.StatusOK, 400 * time.Millisecond, 0
	})
	// The client alone would allow ten events 2s.
	client := gnosis.client(200*time.Millisecond, 200*time.Millisecond)

	started := time.Now()
	_, err := IngestEventsInBatches(context.Background(), client, testEvents(10), BatchIngestConfig{MaxAttempts: 1, RequestTimeout: 100 * time.Millisecond})

	assertTimeout(t, err)
	if elapsed := time.Since(started); elapsed > 350*time.Millisecond {
		t.Fatalf("gave up after %s, want about 100ms", elapsed)
	}
	if got := gnosis.requests(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestIngestEventsInBatches_Gives_up_after_bounded_attempts_and_sends_no_more(t *testing.T) {
	gnosis := newFakeGnosis(t, func(int) (int, time.Duration, time.Duration) {
		return http.StatusServiceUnavailable, 0, 0
	})
	client := gnosis.client(time.Second, time.Second)

	summary, err := IngestEventsInBatches(context.Background(), client, testEvents(60), BatchIngestConfig{BatchSize: 25, MaxAttempts: 3, Backoff: 40 * time.Millisecond})

	var memoryErr *Error
	if !errors.As(err, &memoryErr) || memoryErr.Kind != ErrorKindServer {
		t.Fatalf("error = %T %[1]v, want a server error", err)
	}
	if want := (BatchIngestSummary{Retries: 2}); summary != want {
		t.Fatalf("summary = %+v, want %+v", summary, want)
	}
	gnosis.mu.Lock()
	defer gnosis.mu.Unlock()
	if len(gnosis.arrivals) != 3 {
		t.Fatalf("requests = %d, want 3 attempts at the first batch and nothing after it", len(gnosis.arrivals))
	}
	// The wait doubles: 40ms before the second attempt, 80ms before the third.
	if gap := gnosis.arrivals[1].Sub(gnosis.arrivals[0]); gap < 40*time.Millisecond {
		t.Fatalf("second attempt came after %s, want at least 40ms", gap)
	}
	if gap := gnosis.arrivals[2].Sub(gnosis.arrivals[1]); gap < 80*time.Millisecond {
		t.Fatalf("third attempt came after %s, want at least 80ms", gap)
	}
}

func TestIngestEventsInBatches_Does_not_resend_a_batch_gnosis_refused(t *testing.T) {
	gnosis := newFakeGnosis(t, func(int) (int, time.Duration, time.Duration) {
		return http.StatusUnauthorized, 0, 0
	})
	client := gnosis.client(time.Second, time.Second)

	_, err := IngestEventsInBatches(context.Background(), client, testEvents(3), BatchIngestConfig{MaxAttempts: 3, Backoff: time.Millisecond})

	var memoryErr *Error
	if !errors.As(err, &memoryErr) || memoryErr.Kind != ErrorKindUnauthorized {
		t.Fatalf("error = %T %[1]v, want unauthorized", err)
	}
	if got := gnosis.requests(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestIngestEventsInBatches_Splits_events_into_bounded_batches(t *testing.T) {
	cases := []struct {
		name      string
		events    int
		batchSize int
		want      []int
	}{
		{name: "default size", events: 60, batchSize: 0, want: []int{25, 25, 10}},
		{name: "configured size", events: 101, batchSize: 50, want: []int{50, 50, 1}},
		{name: "size above the API limit", events: 250, batchSize: 500, want: []int{100, 100, 50}},
		{name: "no events", events: 0, batchSize: 10, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gnosis := newFakeGnosis(t, nil)
			client := gnosis.client(time.Second, time.Second)

			summary, err := IngestEventsInBatches(context.Background(), client, testEvents(tc.events), BatchIngestConfig{BatchSize: tc.batchSize})

			if err != nil {
				t.Fatalf("ingest: %v", err)
			}
			if summary.Events != tc.events || summary.Accepted != tc.events || summary.Batches != len(tc.want) {
				t.Fatalf("summary = %+v, want %d events in %d batches", summary, tc.events, len(tc.want))
			}
			if len(gnosis.batchSizes) != len(tc.want) {
				t.Fatalf("batch sizes = %v, want %v", gnosis.batchSizes, tc.want)
			}
			for i, want := range tc.want {
				if gnosis.batchSizes[i] != want {
					t.Fatalf("batch sizes = %v, want %v", gnosis.batchSizes, tc.want)
				}
			}
		})
	}
}

func TestIngestEventsInBatches_Stops_waiting_when_the_context_ends(t *testing.T) {
	gnosis := newFakeGnosis(t, func(int) (int, time.Duration, time.Duration) {
		return http.StatusServiceUnavailable, 0, 0
	})
	client := gnosis.client(time.Second, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := IngestEventsInBatches(ctx, client, testEvents(1), BatchIngestConfig{MaxAttempts: 5, Backoff: time.Minute})

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want the context's deadline", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("returned after %s, want it to stop when the context ended", elapsed)
	}
	if got := gnosis.requests(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestRetryBackoff_doubles_and_stays_bounded(t *testing.T) {
	cases := map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 7: 128 * time.Second, 50: 128 * time.Second}
	for attempt, want := range cases {
		if got := retryBackoff(2*time.Second, attempt); got != want {
			t.Errorf("retryBackoff(2s, %d) = %s, want %s", attempt, got, want)
		}
	}
}

func TestIsTransient(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&Error{Kind: ErrorKindTimeout}, true},
		{&Error{Kind: ErrorKindServer}, true},
		{&Error{Kind: ErrorKindUnexpected}, true},
		{&Error{Kind: ErrorKindUnauthorized}, false},
		{&Error{Kind: ErrorKindValidation}, false},
		{&Error{Kind: ErrorKindPartialBatch}, false},
		{errors.New("connection refused"), true},
	}
	for _, tc := range cases {
		if got := IsTransient(tc.err); got != tc.want {
			t.Errorf("IsTransient(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
