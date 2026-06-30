package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
)

func TestMentionConversation_adds_recalled_memory_to_llm_prompt(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{
		Source:  "long_term_facts",
		Content: "blackflame likes expansive graph memory",
	}}}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" what do I like?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected memory-backed conversation to succeed, got %v", err)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one combined memory query, got %d", len(memoryClient.memoryContextCalls))
	}
	if memoryClient.memoryContextCalls[0].Scope.Visibility != memory.VisibilityGuild || memoryClient.memoryContextCalls[0].Scope.AgentID != "pc-principal" {
		t.Fatalf("expected PC Principal guild memory scope, got %#v", memoryClient.memoryContextCalls[0].Scope)
	}
	if !strings.Contains(assistantPromptText(assistant.messages), "blackflame likes expansive graph memory") {
		t.Fatalf("expected recalled memory in assistant prompt, got %#v", assistant.messages)
	}
}

func TestMentionConversationIncludesCombinedMemoryContextOnce(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{
		{Source: "short_term", Content: "recent channel context"},
		{Source: "long_term_facts", Content: "blackflame prefers graph memory", Facts: []memory.JsonObject{{"kind": "preference", "summary": "use combined context"}}},
		{Source: "graph", Content: "Graph fact: Dragonfly links PC Principal to memory work"},
	}}, skills: []memory.SkillRecord{{
		SkillID:     "skill-approved",
		TenantID:    "bromigos",
		AgentID:     "pc-principal",
		Name:        "Review memory",
		Description: "Review memory context without executing tools.",
		Status:      memory.SkillStatusApproved,
		Scope:       memory.VisibilityAgentShared,
		Metadata:    memory.JsonObject{"reviewed": true},
	}}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" what context do you have?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected combined-memory conversation to succeed, got %v", err)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one combined memory call, got %d", len(memoryClient.memoryContextCalls))
	}
	if len(memoryClient.queries) != 0 || len(memoryClient.graphCalls) != 0 {
		t.Fatalf("expected no legacy context calls after combined memory success, got queries=%d graph=%d", len(memoryClient.queries), len(memoryClient.graphCalls))
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	for _, want := range []string{"Relevant reviewed memory context:", "Source: short_term", "recent channel context", "Source: long_term_facts", "blackflame prefers graph memory", "kind=preference", "summary=use combined context", "Reviewed non-executable skills:", "Review memory"} {
		if !strings.Contains(joinedPrompt, want) {
			t.Fatalf("expected assistant prompt to contain %q, got %#v", want, assistant.messages)
		}
	}
	if strings.Contains(joinedPrompt, "Recent Dragonfly conversation history:") {
		t.Fatalf("expected combined short-term context to replace Dragonfly history fallback, got %#v", assistant.messages)
	}
	if strings.Contains(joinedPrompt, "map[") {
		t.Fatalf("expected combined memory facts to avoid raw Go dumps, got %#v", assistant.messages)
	}
}

func TestMentionConversation_writes_user_and_assistant_messages_to_memory(t *testing.T) {
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
		t.Fatalf("expected memory writes to succeed, got %v", err)
	}
	if len(memoryClient.messages) != 2 {
		t.Fatalf("expected user and assistant memory writes, got %d", len(memoryClient.messages))
	}
	if memoryClient.messages[0].Role != memory.RoleUser || memoryClient.messages[0].Content != "prove you're pc" {
		t.Fatalf("expected user memory write, got %#v", memoryClient.messages[0])
	}
	if memoryClient.messages[1].Role != memory.RoleAssistant || memoryClient.messages[1].Content != "I'm PC, Texas A&M!" {
		t.Fatalf("expected assistant memory write, got %#v", memoryClient.messages[1])
	}
}

func TestMentionConversation_ignores_memory_errors(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{memoryContextErr: errors.New("combined memory unavailable"), contextErr: errors.New("memory unavailable"), writeErr: errors.New("write failed")}
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
		t.Fatalf("expected memory errors to stay non-fatal, got %v", err)
	}
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "I'm PC, Texas A&M!" {
		t.Fatalf("expected Discord reply despite memory errors, got %#v", recorder.sent)
	}
}
