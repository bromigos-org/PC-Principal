package commands

import (
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestBotMention_preserves_allowed_role_gate_for_conversation(t *testing.T) {
	// Given
	previousAllowedRoles := allowedRoles
	allowedRoles = map[string]struct{}{"role-allowed": {}}
	t.Cleanup(func() { allowedRoles = previousAllowedRoles })
	previousAssistant := assistantClient
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(&fakeMemoryClient{})
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" explain graph memory")
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 0 {
		t.Fatalf("expected denied role to suppress conversation replies, got %#v", recorder.sent)
	}
	if len(assistant.messages) != 0 {
		t.Fatalf("expected denied role to skip assistant call, got %#v", assistant.messages)
	}
}

func TestBotMention_queries_memory_when_another_user_mention_precedes_bot(t *testing.T) {
	// Given
	previousAssistant := assistantClient
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	memoryClient := &fakeMemoryClient{}
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, "<@user-2> said memory is broken "+mentionToken("bot-1")+" what do you know about dave?")
	m.Mentions = []*discordgo.User{
		{ID: "user-2", Username: "dave"},
		{ID: "bot-1", Username: "PC Principal", Bot: true},
	}
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 1 {
		t.Fatalf("expected mention conversation reply, got %#v", recorder.sent)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one gnosis memory context query, got %d", len(memoryClient.memoryContextCalls))
	}
	if !strings.Contains(memoryClient.memoryContextCalls[0].Query, "dave") {
		t.Fatalf("expected memory query to preserve other-user context, got %q", memoryClient.memoryContextCalls[0].Query)
	}
	if len(assistant.messages) == 0 {
		t.Fatal("expected assistant conversation to run")
	}
}

func TestBotMention_treats_hey_as_optional_conversation_filler(t *testing.T) {
	// Given
	previousAssistant := assistantClient
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	memoryClient := &fakeMemoryClient{}
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" hey what channel is <@user-2> most active in??")
	m.Mentions = []*discordgo.User{
		{ID: "bot-1", Username: "PC Principal", Bot: true},
		{ID: "user-2", Username: "BlackDave"},
	}
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 1 {
		t.Fatalf("expected mention conversation reply, got %#v", recorder.sent)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one gnosis memory context query, got %d", len(memoryClient.memoryContextCalls))
	}
	query := memoryClient.memoryContextCalls[0].Query
	if strings.HasPrefix(strings.ToLower(query), "hey ") {
		t.Fatalf("expected hey filler removed from memory query, got %q", query)
	}
	if !strings.Contains(query, "<@user-2>") {
		t.Fatalf("expected memory query to preserve BlackDave mention, got %q", query)
	}
}

func TestBotMention_strips_hey_after_later_bot_mention(t *testing.T) {
	// Given
	previousAssistant := assistantClient
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	memoryClient := &fakeMemoryClient{}
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, "<@user-2> "+mentionToken("bot-1")+" hey what channel is user-2 most active in?")
	m.Mentions = []*discordgo.User{
		{ID: "user-2", Username: "BlackDave"},
		{ID: "bot-1", Username: "PC Principal", Bot: true},
	}
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 1 {
		t.Fatalf("expected mention conversation reply, got %#v", recorder.sent)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one gnosis memory context query, got %d", len(memoryClient.memoryContextCalls))
	}
	query := memoryClient.memoryContextCalls[0].Query
	if strings.Contains(strings.ToLower(query), " hey ") {
		t.Fatalf("expected hey filler removed from later bot mention query, got %q", query)
	}
	if !strings.Contains(query, "<@user-2>") || !strings.Contains(query, "most active") {
		t.Fatalf("expected query to preserve user activity question, got %q", query)
	}
}

func TestBotMention_queries_guild_memory_for_user_activity_question(t *testing.T) {
	// Given
	previousAssistant := assistantClient
	assistant := &fakeAssistantClient{reply: "BlackDave is most active in #general, bro."}
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	memoryClient := &fakeMemoryClient{memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{
		Source:  "graph",
		Content: "Graph fact: BlackDave is most active in #general across this guild.",
	}}}}
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" what channel is <@user-2> most active in??")
	m.Mentions = []*discordgo.User{
		{ID: "bot-1", Username: "PC Principal", Bot: true},
		{ID: "user-2", Username: "BlackDave"},
	}
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 1 {
		t.Fatalf("expected mention conversation reply, got %#v", recorder.sent)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one gnosis memory context query, got %d", len(memoryClient.memoryContextCalls))
	}
	memoryCall := memoryClient.memoryContextCalls[0]
	if memoryCall.Query != "what channel is <@user-2> most active in??" {
		t.Fatalf("expected memory query to preserve BlackDave question, got %q", memoryCall.Query)
	}
	if memoryCall.Scope.Visibility != memory.VisibilityGuild || memoryCall.Scope.SessionID != "guild:guild-1" || memoryCall.Scope.ChannelID != "" {
		t.Fatalf("expected guild-scoped gnosis memory request, got %#v", memoryCall.Scope)
	}
	if !strings.Contains(assistantPromptText(assistant.messages), "BlackDave is most active in #general") {
		t.Fatalf("expected guild graph memory in assistant prompt, got %#v", assistant.messages)
	}
}
