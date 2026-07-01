package memory

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_GetMemoryContext_sendsScopedRequestAndDecodesSections(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/memory/context" {
			t.Fatalf("expected POST /v1/memory/context, got %s %s", r.Method, r.URL.Path)
		}
		var body MemoryContextRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode memory context request: %v", err)
		}
		if body.Query != "channel memory" || body.MaxItems != 5 || body.GraphLimit != 3 {
			t.Fatalf("expected combined context limits, got %#v", body)
		}
		if !body.IncludeShortTerm || !body.IncludeLongTerm || !body.IncludeReasoning || !body.IncludeGraph {
			t.Fatalf("expected all context sources enabled, got %#v", body)
		}
		if body.Scope.AgentID != "pc-principal" || body.Scope.Visibility != VisibilityChannel {
			t.Fatalf("expected scoped memory request, got %#v", body.Scope)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sections":[{"source":"short_term","content":"recent channel note","facts":[{"kind":"message"}]},{"source":"graph","content":"graph relation","facts":[{"kind":"channel"}]}]}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "placeholder"}, server.Client())

	// When
	got, err := client.GetMemoryContext(context.Background(), MemoryContextRequest{
		Scope:            testScope(),
		Query:            "channel memory",
		IncludeShortTerm: true,
		IncludeLongTerm:  true,
		IncludeReasoning: true,
		IncludeGraph:     true,
		MaxItems:         5,
		GraphLimit:       3,
	})

	// Then
	if err != nil {
		t.Fatalf("expected combined context request to succeed, got %v", err)
	}
	if len(got.Sections) != 2 || got.Sections[0].Source != "short_term" || got.Sections[1].Facts[0]["kind"] != "channel" {
		t.Fatalf("expected labeled memory sections with facts, got %#v", got)
	}
}

func TestHTTPClientReasoningLifecyclePaths(t *testing.T) {
	// Given
	seenPaths := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths[r.URL.Path] = true
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/reasoning/traces":
			var body ReasoningTraceStartRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode trace start request: %v", err)
			}
			if body.Task != "answer safely" || body.Metadata["sensitive_key"] != "placeholder" {
				t.Fatalf("expected inert trace payload, got %#v", body)
			}
			_, _ = w.Write([]byte(`{"trace_id":"trace-1","session_id":"session-1","task":"answer safely"}`))
		case "/v1/reasoning/traces/trace-1/steps":
			var body ReasoningStepRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode reasoning step request: %v", err)
			}
			if body.Scope.AgentID != "pc-principal" || body.Scope.Visibility != VisibilityChannel {
				t.Fatalf("expected scoped reasoning step request, got %#v", body.Scope)
			}
			_, _ = w.Write([]byte(`{"step_id":"step-1","trace_id":"trace-1","step_number":1}`))
		case "/v1/reasoning/steps/step-1/tool-calls":
			var body ReasoningToolCallRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode tool call request: %v", err)
			}
			if body.Scope.AgentID != "pc-principal" || body.Scope.Visibility != VisibilityChannel {
				t.Fatalf("expected scoped reasoning tool call request, got %#v", body.Scope)
			}
			if body.ToolName != "memory.search" || body.Arguments["password"] != "placeholder" || body.Result != "redacted placeholder" {
				t.Fatalf("expected inert tool payload, got %#v", body)
			}
			_, _ = w.Write([]byte(`{"tool_call_id":"tool-1","trace_id":"trace-1","step_id":"step-1"}`))
		case "/v1/reasoning/traces/trace-1/complete":
			var body ReasoningTraceCompleteRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode reasoning complete request: %v", err)
			}
			if body.Scope.AgentID != "pc-principal" || body.Scope.Visibility != VisibilityChannel {
				t.Fatalf("expected scoped reasoning complete request, got %#v", body.Scope)
			}
			_, _ = w.Write([]byte(`{"trace_id":"trace-1","success":true,"outcome":"answered","completed_at":"2026-06-28T00:00:00Z"}`))
		case "/v1/reasoning/context":
			_, _ = w.Write([]byte(`{"context":"No similar reasoning traces found.","traces":[{"trace_id":"trace-1"}]}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "placeholder"}, server.Client())
	success := true

	// When
	trace, traceErr := client.StartReasoningTrace(context.Background(), testReasoningTraceStartRequest())
	step, stepErr := client.AddReasoningStep(context.Background(), testReasoningStepRequest())
	toolCall, toolErr := client.RecordReasoningToolCall(context.Background(), testReasoningToolCallRequest())
	complete, completeErr := client.CompleteReasoningTrace(context.Background(), ReasoningTraceCompleteRequest{Scope: testScope(), TraceID: "trace-1", Outcome: "answered", Success: &success, Metadata: JsonObject{"result": "ok"}})
	reasoningContext, contextErr := client.GetReasoningContext(context.Background(), testReasoningContextRequest())

	// Then
	if traceErr != nil || stepErr != nil || toolErr != nil || completeErr != nil || contextErr != nil {
		t.Fatalf("expected reasoning lifecycle to succeed, got trace=%v step=%v tool=%v complete=%v context=%v", traceErr, stepErr, toolErr, completeErr, contextErr)
	}
	if trace.TraceID != "trace-1" || step.StepID != "step-1" || toolCall.ToolCallID != "tool-1" || complete.Success == nil || !*complete.Success || len(reasoningContext.Traces) != 1 {
		t.Fatalf("expected decoded reasoning responses, got trace=%#v step=%#v tool=%#v complete=%#v context=%#v", trace, step, toolCall, complete, reasoningContext)
	}
	for _, path := range []string{"/v1/reasoning/traces", "/v1/reasoning/traces/trace-1/steps", "/v1/reasoning/steps/step-1/tool-calls", "/v1/reasoning/traces/trace-1/complete", "/v1/reasoning/context"} {
		if !seenPaths[path] {
			t.Fatalf("expected path %s to be called", path)
		}
	}
}

func TestHTTPClientReasoningToolCallUnauthorized(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"redacted placeholder"}`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "placeholder"}, server.Client())

	// When
	_, err := client.RecordReasoningToolCall(context.Background(), testReasoningToolCallRequest())

	// Then
	if err == nil {
		t.Fatal("expected unauthorized reasoning tool-call error")
	}
	var memoryErr *Error
	if !errors.As(err, &memoryErr) || memoryErr.Kind != ErrorKindUnauthorized || memoryErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized typed error, got %T %[1]v", err)
	}
	if strings.Contains(err.Error(), "redacted placeholder") {
		t.Fatalf("expected sanitized error, got %v", err)
	}
}

func TestHTTPClientGetReasoningContextMalformedResponse(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"context":`))
	}))
	t.Cleanup(server.Close)
	client := NewClient(Config{Enabled: true, BaseURL: server.URL, Token: "placeholder"}, server.Client())

	// When
	_, err := client.GetReasoningContext(context.Background(), testReasoningContextRequest())

	// Then
	if err == nil {
		t.Fatal("expected malformed reasoning context response to fail")
	}
	if !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("expected decode response error, got %v", err)
	}
}

func testReasoningTraceStartRequest() ReasoningTraceStartRequest {
	return ReasoningTraceStartRequest{
		Scope:                testScope(),
		SessionID:            "session-1",
		Task:                 "answer safely",
		Metadata:             JsonObject{"sensitive_key": "placeholder"},
		TriggeredByMessageID: "message-1",
		UserIdentifier:       "user-1",
	}
}

func testReasoningStepRequest() ReasoningStepRequest {
	return ReasoningStepRequest{
		Scope:       testScope(),
		TraceID:     "trace-1",
		Action:      "consult memory",
		Observation: "memory returned context",
		StepNumber:  1,
		Metadata:    JsonObject{"kind": "observation"},
	}
}

func testReasoningToolCallRequest() ReasoningToolCallRequest {
	return ReasoningToolCallRequest{
		Scope:      testScope(),
		TraceID:    "trace-1",
		StepID:     "step-1",
		ToolName:   "memory.search",
		Arguments:  JsonObject{"password": "placeholder"},
		Result:     "redacted placeholder",
		Status:     "success",
		DurationMs: 12,
		MessageID:  "message-1",
		TouchedEntities: []TouchedEntityRef{
			{ID: "entity-1", Name: "Blackflame", Type: "user"},
		},
	}
}

func testReasoningContextRequest() ReasoningContextRequest {
	return ReasoningContextRequest{
		Scope:            testScope(),
		Query:            "prior reasoning",
		IncludeTraces:    true,
		IncludeSteps:     true,
		IncludeToolCalls: true,
		MaxItems:         4,
	}
}
