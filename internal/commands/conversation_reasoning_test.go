package commands

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
)

func TestMentionConversationRecordsSuccessfulReasoningTrace(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" prove you're pc")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected traced conversation to succeed, got %v", err)
	}
	if len(memoryClient.startedTraces) != 1 {
		t.Fatalf("expected one reasoning trace start, got %d", len(memoryClient.startedTraces))
	}
	started := memoryClient.startedTraces[0]
	if started.TriggeredByMessageID != "message-1" || started.UserIdentifier != "user-1" || started.SessionID != "guild:guild-1:channel:channel-1" {
		t.Fatalf("expected trace to reference Discord lifecycle IDs, got %#v", started)
	}
	if strings.Contains(started.Task, "prove you're pc") || strings.Contains(traceObjectText(started.Metadata), "prove you're pc") {
		t.Fatalf("expected trace start to omit raw user content, got %#v", started)
	}
	if len(memoryClient.reasoningSteps) == 0 || len(memoryClient.toolCalls) == 0 {
		t.Fatalf("expected trace steps and tool calls, got steps=%d tools=%d", len(memoryClient.reasoningSteps), len(memoryClient.toolCalls))
	}
	for _, want := range []string{
		"gnosis.memory_context",
		"gnosis.skills_list",
		"litellm.generate",
		"discord.message_send",
		"gnosis.message_write.user",
		"gnosis.message_write.assistant",
	} {
		if !hasReasoningToolCall(memoryClient.toolCalls, want, "success") {
			t.Fatalf("expected successful %s tool call, got %#v", want, memoryClient.toolCalls)
		}
	}
	if len(memoryClient.completedTraces) != 1 {
		t.Fatalf("expected one completed reasoning trace, got %d", len(memoryClient.completedTraces))
	}
	completed := memoryClient.completedTraces[0]
	if completed.Success == nil || !*completed.Success || completed.Outcome != "answered" {
		t.Fatalf("expected successful completion, got %#v", completed)
	}
	if strings.Contains(traceObjectText(completed.Metadata), "I'm PC, Texas A&M!") {
		t.Fatalf("expected completion metadata to omit raw assistant reply, got %#v", completed.Metadata)
	}
}

func TestMentionConversationRecordsMemoryContextFallbackInReasoningTrace(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{
		memoryContextErr: errors.New("combined context offline detail"),
		contextText:      "legacy memory context",
		graphText:        "legacy graph context",
	}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" fallback status probe")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected fallback conversation to succeed, got %v", err)
	}
	if len(memoryClient.queries) != 1 || len(memoryClient.graphCalls) != 1 {
		t.Fatalf("expected legacy memory fallback, got queries=%d graph=%d", len(memoryClient.queries), len(memoryClient.graphCalls))
	}
	if hasReasoningToolCall(memoryClient.toolCalls, "gnosis.memory_context", "success") {
		t.Fatalf("expected memory context fallback not to be recorded as success, got %#v", memoryClient.toolCalls)
	}
	if !hasReasoningToolCall(memoryClient.toolCalls, "gnosis.memory_context", "error") {
		t.Fatalf("expected memory context fallback error status, got %#v", memoryClient.toolCalls)
	}
	traceText := reasoningTraceText(memoryClient)
	for _, blocked := range []string{"combined context offline detail", "fallback status probe"} {
		if strings.Contains(traceText, blocked) {
			t.Fatalf("expected reasoning trace to redact %q, got %s", blocked, traceText)
		}
	}
}

func TestMentionConversationCompletesReasoningTraceOnLiteLLMFailure(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{err: errors.New("upstream rejected sensitive marker while answering hidden note")}
	memoryClient := &fakeMemoryClient{}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" hidden note marker")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err == nil {
		t.Fatal("expected LiteLLM failure to surface")
	}
	if len(memoryClient.completedTraces) != 1 {
		t.Fatalf("expected failed trace completion, got %d", len(memoryClient.completedTraces))
	}
	completed := memoryClient.completedTraces[0]
	if completed.Success == nil || *completed.Success || completed.Outcome != "llm_failed" {
		t.Fatalf("expected failed LiteLLM completion, got %#v", completed)
	}
	traceText := reasoningTraceText(memoryClient)
	for _, blocked := range []string{"hidden note marker", "sensitive marker", "upstream rejected"} {
		if strings.Contains(traceText, blocked) {
			t.Fatalf("expected reasoning trace to redact %q, got %s", blocked, traceText)
		}
	}
	if !hasReasoningToolCall(memoryClient.toolCalls, "litellm.generate", "error") {
		t.Fatalf("expected failed LiteLLM tool call, got %#v", memoryClient.toolCalls)
	}
}

func TestMentionConversationCompletesReasoningTraceOnDiscordSendFailure(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" answer safely")
	s.Client = failingDiscordSendHTTPClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err == nil {
		t.Fatal("expected Discord send failure to surface")
	}
	if len(memoryClient.completedTraces) != 1 {
		t.Fatalf("expected failed trace completion, got %d", len(memoryClient.completedTraces))
	}
	completed := memoryClient.completedTraces[0]
	if completed.Success == nil || *completed.Success || completed.Outcome != "discord_send_failed" {
		t.Fatalf("expected failed Discord completion, got %#v", completed)
	}
	if !hasReasoningToolCall(memoryClient.toolCalls, "discord.message_send", "error") {
		t.Fatalf("expected failed Discord tool call, got %#v", memoryClient.toolCalls)
	}
}

func TestMentionConversationContinuesWhenReasoningTraceFails(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{startTraceErr: errors.New("reasoning unavailable")}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" keep talking")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected trace failures to stay non-fatal, got %v", err)
	}
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "I'm PC, Texas A&M!" {
		t.Fatalf("expected Discord reply despite trace errors, got %#v", recorder.sent)
	}
}

func assistantPromptText(messages []store.Message) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		parts = append(parts, message.Content)
	}
	return strings.Join(parts, "\n")
}

func hasReasoningToolCall(calls []memory.ReasoningToolCallRequest, toolName string, status string) bool {
	for _, call := range calls {
		if call.ToolName == toolName && call.Status == status {
			return true
		}
	}
	return false
}

func reasoningTraceText(client *fakeMemoryClient) string {
	parts := []string{traceObjectText(client.startedTraces[0].Metadata)}
	for _, step := range client.reasoningSteps {
		parts = append(parts, step.Action, step.Observation, traceObjectText(step.Metadata))
	}
	for _, call := range client.toolCalls {
		parts = append(parts, call.ToolName, call.Status, call.Error, traceObjectText(call.Arguments))
	}
	for _, complete := range client.completedTraces {
		parts = append(parts, complete.Outcome, traceObjectText(complete.Metadata))
	}
	return strings.Join(parts, "\n")
}

func traceObjectText(object memory.JsonObject) string {
	parts := make([]string, 0, len(object))
	for key, value := range object {
		parts = append(parts, key+"="+fmt.Sprint(value))
	}
	return strings.Join(parts, "\n")
}

func failingDiscordSendHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"message":"send failed"}`)),
		}, nil
	})}
}
