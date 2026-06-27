package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPClientIngestEventsSuccess(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/events/batch" {
			t.Fatalf("expected POST /v1/events/batch, got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		var body ClientEventBatchRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode event batch request: %v", err)
		}
		if len(body.Events) != 1 {
			t.Fatalf("expected one event, got %#v", body.Events)
		}
		event := body.Events[0]
		if event.SourceClient != SourceClientDiscord || event.EventType != EventTypeMessageCreated {
			t.Fatalf("expected discord message event, got %#v", event)
		}
		if event.Payload["content"] != "Howdy" || event.Discord.MessageID != "message-1" {
			t.Fatalf("expected payload and discord context, got %#v", event)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"event_id":"event-1","status":"accepted"}]}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	got, err := client.IngestEvents(context.Background(), []ClientEvent{testClientEvent()})

	// Then
	if err != nil {
		t.Fatalf("expected batch ingest to succeed, got %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Status != EventIngestStatusAccepted {
		t.Fatalf("expected accepted result, got %#v", got)
	}
}

func TestHTTPClientIngestEventsPartialFailure(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"event_id":"event-1","status":"accepted"},{"event_id":"event-2","status":"failed","reason":"tenant mismatch"}]}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())
	failed := testClientEvent()
	failed.EventID = "event-2"

	// When
	got, err := client.IngestEvents(context.Background(), []ClientEvent{testClientEvent(), failed})

	// Then
	if err == nil {
		t.Fatal("expected partial batch error")
	}
	var memoryErr *Error
	if !errors.As(err, &memoryErr) || memoryErr.Kind != ErrorKindPartialBatch {
		t.Fatalf("expected partial batch typed error, got %T %[1]v", err)
	}
	if len(memoryErr.Results) != 2 || memoryErr.Results[1].Reason != "tenant mismatch" {
		t.Fatalf("expected decoded partial results on error, got %#v", memoryErr.Results)
	}
	if len(got.Results) != 2 || got.Results[1].Status != EventIngestStatusFailed {
		t.Fatalf("expected partial response to be returned, got %#v", got)
	}
}

func TestHTTPClientIngestEventUnauthorized(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"secret token scope details"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	err := client.IngestEvent(context.Background(), testClientEvent())

	// Then
	if err == nil {
		t.Fatal("expected unauthorized error")
	}
	var memoryErr *Error
	if !errors.As(err, &memoryErr) || memoryErr.Kind != ErrorKindUnauthorized || memoryErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized typed error, got %T %[1]v", err)
	}
	if strings.Contains(err.Error(), "secret token scope details") {
		t.Fatalf("expected sanitized error, got %v", err)
	}
}

func TestHTTPClientIngestEventsTimeout(t *testing.T) {
	// Given
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})
	client := NewClient(
		Config{Enabled: true, BaseURL: "http://memory.example", Token: "test-token"},
		&http.Client{Transport: transport, Timeout: time.Second},
	)

	// When
	_, err := client.IngestEvents(context.Background(), []ClientEvent{testClientEvent()})

	// Then
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var memoryErr *Error
	if !errors.As(err, &memoryErr) || memoryErr.Kind != ErrorKindTimeout {
		t.Fatalf("expected timeout typed error, got %T %[1]v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
