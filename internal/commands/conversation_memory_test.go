package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
)

type fakeAssistantClient struct {
	messages []store.Message
	reply    string
}

func (c *fakeAssistantClient) Generate(ctx context.Context, messages []store.Message) (string, error) {
	c.messages = append([]store.Message(nil), messages...)
	return c.reply, nil
}

type fakeMemoryClient struct {
	contextText string
	contextErr  error
	writeErr    error
	queries     []memory.ContextQuery
	messages    []memory.Message
}

func (c *fakeMemoryClient) GetContext(ctx context.Context, query memory.ContextQuery) (string, error) {
	c.queries = append(c.queries, query)
	return c.contextText, c.contextErr
}

func (c *fakeMemoryClient) AddMessage(ctx context.Context, message memory.Message) error {
	c.messages = append(c.messages, message)
	return c.writeErr
}

func TestMentionConversation_adds_recalled_memory_to_llm_prompt(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{contextText: "blackflame likes expansive graph memory"}
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
	if len(memoryClient.queries) != 1 {
		t.Fatalf("expected one memory query, got %d", len(memoryClient.queries))
	}
	if memoryClient.queries[0].Scope.Visibility != memory.VisibilityChannel || memoryClient.queries[0].Scope.AgentID != "pc-principal" {
		t.Fatalf("expected PC Principal channel memory scope, got %#v", memoryClient.queries[0].Scope)
	}
	if len(assistant.messages) < 2 || !strings.Contains(assistant.messages[1].Content, "blackflame likes expansive graph memory") {
		t.Fatalf("expected recalled memory in assistant prompt, got %#v", assistant.messages)
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
	memoryClient := &fakeMemoryClient{contextErr: errors.New("memory unavailable"), writeErr: errors.New("write failed")}
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
