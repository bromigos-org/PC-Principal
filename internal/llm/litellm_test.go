package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/store"
)

func TestLiteLLMClientGenerate_sends_chat_completion_contract(t *testing.T) {
	// Given
	var gotPath string
	var gotAuth string
	var gotRequest liteLLMRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"You PC, bro?"}}]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LITELLM_BASE_URL", server.URL)
	t.Setenv("LITELLM_API_KEY", "test-key")
	client := NewLiteLLMClient(server.Client())
	messages := []store.Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "prove it"},
	}

	// When
	got, err := client.Generate(context.Background(), messages)

	// Then
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if got != "You PC, bro?" {
		t.Fatalf("expected assistant content, got %q", got)
	}
	if gotPath != "/chat/completions" {
		t.Fatalf("expected /chat/completions path, got %q", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("expected bearer auth header, got %q", gotAuth)
	}
	if gotRequest.Model != defaultLiteLLMModel {
		t.Fatalf("expected model %q, got %q", defaultLiteLLMModel, gotRequest.Model)
	}
	if len(gotRequest.Messages) != len(messages) || gotRequest.Messages[1].Content != "prove it" {
		t.Fatalf("expected message payload to be preserved, got %#v", gotRequest.Messages)
	}
}

func TestLiteLLMClientGenerate_reports_non_ok_response(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`upstream down`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LITELLM_BASE_URL", server.URL)
	client := NewLiteLLMClient(server.Client())

	// When
	_, err := client.Generate(context.Background(), []store.Message{{Role: "user", Content: "hi"}})

	// Then
	if err == nil || !strings.Contains(err.Error(), "LiteLLM returned 502: upstream down") {
		t.Fatalf("expected status/body error, got %v", err)
	}
}

func TestLiteLLMClientGenerate_reports_missing_choices(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LITELLM_BASE_URL", server.URL)
	client := NewLiteLLMClient(server.Client())

	// When
	_, err := client.Generate(context.Background(), []store.Message{{Role: "user", Content: "hi"}})

	// Then
	if err == nil || !strings.Contains(err.Error(), "no choices in response") {
		t.Fatalf("expected no choices error, got %v", err)
	}
}

func TestNewLiteLLMClient_uses_default_timeout_when_http_client_is_nil(t *testing.T) {
	// Given / When
	client := NewLiteLLMClient(nil)

	// Then
	if client.httpClient.Timeout != defaultRequestTimeout {
		t.Fatalf("expected default timeout %s, got %s", defaultRequestTimeout, client.httpClient.Timeout)
	}
}
