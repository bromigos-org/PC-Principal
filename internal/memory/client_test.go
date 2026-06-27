package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_GetContext_sendsScopedAuthorizedRequest(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/context" {
			t.Fatalf("expected POST /v1/context, got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		var body contextRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode context request: %v", err)
		}
		if body.Query != "what does blackflame like?" || body.Limit != 6 {
			t.Fatalf("expected query and limit to pass through, got %#v", body)
		}
		if body.Scope.AgentID != "pc-principal" || body.Scope.Visibility != VisibilityChannel {
			t.Fatalf("expected PC Principal channel scope, got %#v", body.Scope)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"context":"blackflame likes homelab memory"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	got, err := client.GetContext(context.Background(), ContextQuery{
		Scope: testScope(),
		Query: "what does blackflame like?",
		Limit: 6,
	})

	// Then
	if err != nil {
		t.Fatalf("expected context request to succeed, got %v", err)
	}
	if got != "blackflame likes homelab memory" {
		t.Fatalf("expected recalled context, got %q", got)
	}
}

func TestClient_AddMessage_sendsScopedAuthorizedRequest(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Fatalf("expected POST /v1/messages, got %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Fatalf("expected bearer token, got %q", got)
		}
		var body messageWriteRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode message request: %v", err)
		}
		if body.Role != RoleAssistant || body.Content != "I'm PC, Texas A&M!" {
			t.Fatalf("expected assistant message payload, got %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	err := client.AddMessage(context.Background(), Message{
		Scope:   testScope(),
		Role:    RoleAssistant,
		Content: "I'm PC, Texas A&M!",
	})

	// Then
	if err != nil {
		t.Fatalf("expected message write to succeed, got %v", err)
	}
}

func TestClient_Noops_whenDisabled(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("disabled client should not call agents-memory")
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: false, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	contextText, contextErr := client.GetContext(context.Background(), ContextQuery{
		Scope: testScope(),
		Query: "query",
		Limit: 4,
	})
	messageErr := client.AddMessage(context.Background(), Message{
		Scope:   testScope(),
		Role:    RoleUser,
		Content: "hello",
	})

	// Then
	if contextErr != nil || messageErr != nil {
		t.Fatalf("expected disabled client to no-op, got contextErr=%v messageErr=%v", contextErr, messageErr)
	}
	if contextText != "" {
		t.Fatalf("expected disabled context to be empty, got %q", contextText)
	}
}

func TestClient_Noops_whenTokenMissing(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("client without token should not call agents-memory")
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL}, server.Client())

	// When
	contextText, contextErr := client.GetContext(context.Background(), ContextQuery{
		Scope: testScope(),
		Query: "query",
		Limit: 4,
	})
	messageErr := client.AddMessage(context.Background(), Message{
		Scope:   testScope(),
		Role:    RoleUser,
		Content: "hello",
	})

	// Then
	if contextErr != nil || messageErr != nil {
		t.Fatalf("expected missing token client to no-op, got contextErr=%v messageErr=%v", contextErr, messageErr)
	}
	if contextText != "" {
		t.Fatalf("expected missing token context to be empty, got %q", contextText)
	}
}

func TestClient_GetContext_sanitizesErrorResponseBody(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"secret token scope details"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	_, err := client.GetContext(context.Background(), ContextQuery{
		Scope: testScope(),
		Query: "query",
		Limit: 4,
	})

	// Then
	if err == nil {
		t.Fatal("expected context request to fail")
	}
	if strings.Contains(err.Error(), "secret token scope details") {
		t.Fatalf("expected sanitized error, got %v", err)
	}
	if !strings.Contains(err.Error(), "agents-memory returned 403") {
		t.Fatalf("expected status code in error, got %v", err)
	}
}

func testScope() Scope {
	return Scope{
		TenantID:   "bromigos",
		SpaceID:    "guild-1",
		AgentID:    "pc-principal",
		SessionID:  "guild:guild-1:channel:channel-1",
		UserID:     "user-1",
		Visibility: VisibilityChannel,
		GuildID:    "guild-1",
		ChannelID:  "channel-1",
	}
}
