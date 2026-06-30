package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoadConfigFromEnv_readsGnosisEnv(t *testing.T) {
	// Given
	t.Setenv("GNOSIS_ENABLED", "true")
	t.Setenv("GNOSIS_SERVICE_URL", "http://gnosis.local")
	t.Setenv("GNOSIS_SERVICE_TOKEN", "gnosis-token")
	t.Setenv("GNOSIS_TENANT_ID", "tenant-1")

	// When
	config := LoadConfigFromEnv()

	// Then
	if !config.Enabled || config.BaseURL != "http://gnosis.local" || config.Token != "gnosis-token" || config.TenantID != "tenant-1" {
		t.Fatalf("expected gnosis env config, got %#v", config)
	}
}

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

func TestClient_GetMemoryContext_decodesOptionalMemoryTypeSections(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/memory/context" {
			t.Fatalf("expected POST /v1/memory/context, got %s %s", r.Method, r.URL.Path)
		}
		var body MemoryContextRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode memory context request: %v", err)
		}
		if !body.IncludeShortTerm || !body.IncludeLongTerm || !body.IncludeReasoning || !body.IncludeGraph {
			t.Fatalf("expected request to preserve memory routing flags, got %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sections":[{"memory_type":"reasoning","content":"prior successful tool pattern","facts":[]},{"source":"graph","content":"legacy graph label","facts":[{"kind":"graph"}]}]}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "test-token"}, server.Client())

	// When
	got, err := client.GetMemoryContext(context.Background(), MemoryContextRequest{
		Scope:            testScope(),
		Query:            "what worked before?",
		IncludeShortTerm: true,
		IncludeLongTerm:  true,
		IncludeReasoning: true,
		IncludeGraph:     true,
		MaxItems:         6,
		GraphLimit:       4,
	})

	// Then
	if err != nil {
		t.Fatalf("expected memory context request to succeed, got %v", err)
	}
	if len(got.Sections) != 2 {
		t.Fatalf("expected two memory sections, got %#v", got.Sections)
	}
	if got.Sections[0].MemoryType != "reasoning" || got.Sections[0].Content != "prior successful tool pattern" {
		t.Fatalf("expected memory_type reasoning section, got %#v", got.Sections[0])
	}
	if got.Sections[1].Source != "graph" || got.Sections[1].Facts[0]["kind"] != "graph" {
		t.Fatalf("expected source-labeled graph section to remain compatible, got %#v", got.Sections[1])
	}
}

func TestClient_Noops_whenDisabled(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("disabled client should not call gnosis")
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
	ingestResponse, ingestErr := client.IngestEvents(context.Background(), []ClientEvent{testClientEvent()})
	graphResponse, graphErr := client.GetGraphContext(context.Background(), GraphContextRequest{Scope: testScope(), Query: "query"})
	memoryResponse, memoryErr := client.GetMemoryContext(context.Background(), MemoryContextRequest{Scope: testScope(), Query: "query"})
	traceResponse, traceErr := client.StartReasoningTrace(context.Background(), testReasoningTraceStartRequest())
	stepResponse, stepErr := client.AddReasoningStep(context.Background(), testReasoningStepRequest())
	toolResponse, toolErr := client.RecordReasoningToolCall(context.Background(), testReasoningToolCallRequest())
	completeResponse, completeErr := client.CompleteReasoningTrace(context.Background(), ReasoningTraceCompleteRequest{TraceID: "trace-1"})
	reasoningResponse, reasoningErr := client.GetReasoningContext(context.Background(), testReasoningContextRequest())
	skillsResponse, skillsErr := client.ListSkills(context.Background(), SkillListRequest{TenantID: "bromigos", AgentID: "pc-principal"})
	proposalResponse, proposalErr := client.ProposeSkill(context.Background(), testSkillProposal())
	usageErr := client.RecordSkillUsage(context.Background(), testSkillUsage())

	// Then
	if contextErr != nil || messageErr != nil || ingestErr != nil || graphErr != nil || memoryErr != nil || traceErr != nil || stepErr != nil || toolErr != nil || completeErr != nil || reasoningErr != nil || skillsErr != nil || proposalErr != nil || usageErr != nil {
		t.Fatalf("expected disabled client to no-op, got contextErr=%v messageErr=%v ingestErr=%v graphErr=%v memoryErr=%v traceErr=%v stepErr=%v toolErr=%v completeErr=%v reasoningErr=%v skillsErr=%v proposalErr=%v usageErr=%v", contextErr, messageErr, ingestErr, graphErr, memoryErr, traceErr, stepErr, toolErr, completeErr, reasoningErr, skillsErr, proposalErr, usageErr)
	}
	if contextText != "" || len(ingestResponse.Results) != 0 || graphResponse.Context != "" || len(memoryResponse.Sections) != 0 || traceResponse.TraceID != "" || stepResponse.StepID != "" || toolResponse.ToolCallID != "" || completeResponse.TraceID != "" || reasoningResponse.Context != "" || len(skillsResponse.Skills) != 0 || proposalResponse.ProposalID != "" {
		t.Fatalf("expected disabled empty responses, got context=%q ingest=%#v graph=%#v memory=%#v trace=%#v step=%#v tool=%#v complete=%#v reasoning=%#v skills=%#v proposal=%#v", contextText, ingestResponse, graphResponse, memoryResponse, traceResponse, stepResponse, toolResponse, completeResponse, reasoningResponse, skillsResponse, proposalResponse)
	}
}

func TestClient_Noops_whenTokenMissing(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("client without token should not call gnosis")
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
	if !strings.Contains(err.Error(), "gnosis returned 403") {
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
