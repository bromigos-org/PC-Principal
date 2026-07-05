package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestMentionConversationIncludesScopedGraphContext(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{
		memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{
			{Source: "long_term_facts", Content: "Long-term recall: blackflame likes homelab memory"},
			{Source: "graph", Content: "Graph fact: #general discussed Dragonfly yesterday"},
		}},
	}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" what did we say about memory?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected graph-backed conversation to succeed, got %v", err)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one combined memory context call, got %d", len(memoryClient.memoryContextCalls))
	}
	memoryCall := memoryClient.memoryContextCalls[0]
	if memoryCall.Query != "what did we say about memory?" || memoryCall.GraphLimit != graphContextLimit || memoryCall.MaxItems != memoryContextLimit {
		t.Fatalf("expected combined memory query and limits to pass through, got %#v", memoryCall)
	}
	if !memoryCall.IncludeShortTerm || !memoryCall.IncludeLongTerm || !memoryCall.IncludeReasoning || !memoryCall.IncludeGraph {
		t.Fatalf("expected combined memory request to include all reviewed context sources, got %#v", memoryCall)
	}
	if memoryCall.Scope.Visibility != memory.VisibilityGuild || memoryCall.Scope.GuildID != "guild-1" || memoryCall.Scope.ChannelID != "" {
		t.Fatalf("expected guild-scoped memory request, got %#v", memoryCall.Scope)
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	for _, want := range []string{"Recent Dragonfly conversation history:", "Relevant reviewed memory context:", "Long-term recall: blackflame likes homelab memory", "Graph fact: #general discussed Dragonfly yesterday"} {
		if !strings.Contains(joinedPrompt, want) {
			t.Fatalf("expected assistant prompt to contain %q, got %#v", want, assistant.messages)
		}
	}
}

func TestMentionConversationDoesNotDuplicateGraphAndLongTermSections(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{
		{Source: "long_term_facts", Content: "Long-term recall: blackflame likes homelab memory"},
		{Source: "graph", Content: "Graph fact: #general discussed Dragonfly yesterday"},
	}}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" what did we say about memory?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected combined graph conversation to succeed, got %v", err)
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	if strings.Count(joinedPrompt, "Relevant reviewed memory context:") != 1 {
		t.Fatalf("expected one combined memory section, got prompt %#v", assistant.messages)
	}
	for _, blocked := range []string{"Relevant long-term recall:", "Relevant Discord graph facts:"} {
		if strings.Contains(joinedPrompt, blocked) {
			t.Fatalf("expected no legacy section %q after combined context success, got %#v", blocked, assistant.messages)
		}
	}
	if strings.Count(joinedPrompt, "Long-term recall: blackflame likes homelab memory") != 1 || strings.Count(joinedPrompt, "Graph fact: #general discussed Dragonfly yesterday") != 1 {
		t.Fatalf("expected graph and long-term content exactly once, got %#v", assistant.messages)
	}
	if len(memoryClient.graphCalls) != 0 {
		t.Fatalf("expected no legacy graph calls after combined memory success, got graph=%d", len(memoryClient.graphCalls))
	}
}

func TestMentionConversationContinuesWhenGraphContextFails(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{memoryContextErr: errors.New("combined memory unavailable"), graphErr: errors.New("graph unavailable")}
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
		t.Fatalf("expected graph context errors to stay non-fatal, got %v", err)
	}
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "I'm PC, Texas A&M!" {
		t.Fatalf("expected Discord reply despite graph errors, got %#v", recorder.sent)
	}
	if strings.Contains(assistantPromptText(assistant.messages), "graph unavailable") {
		t.Fatalf("expected graph error details to stay out of prompt, got %#v", assistant.messages)
	}
}

func TestMentionConversationAllowsSameGuildGraphContext(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{Source: "graph", Content: "Graph fact: sibling channel secret"}}}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" what is scoped here?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected scoped graph conversation to succeed, got %v", err)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one combined memory context call, got %d", len(memoryClient.memoryContextCalls))
	}
	requestScope := memoryClient.memoryContextCalls[0].Scope
	if requestScope.SessionID != "guild:guild-1" || requestScope.ChannelID != "" || requestScope.Visibility != memory.VisibilityGuild {
		t.Fatalf("expected graph request to use guild memory scope, got %#v", requestScope)
	}
}

func TestMentionConversationDirectRepliesToChannelActivityFacts(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "should not be used"}
	memoryClient := &fakeMemoryClient{memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{
		Source: "graph",
		Facts: []memory.JsonObject{
			{"type": "channel_activity", "rank": 2, "user_display_name": "BlackDave", "channel_name": "server-suggestion-box", "message_count": 4},
			{"type": "channel_activity", "rank": 1, "user_display_name": "BlackDave", "channel_name": "💬┃general-chat", "message_count": 247},
			{"type": "channel_activity", "rank": 3, "user_display_name": "BlackDave", "channel_name": "🎇┃promos", "message_count": 1},
		},
	}}}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" hey what are @BlackDave top 5 most active channels??")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected channel activity conversation to succeed, got %v", err)
	}
	if len(memoryClient.memoryContextCalls) != 1 {
		t.Fatalf("expected one combined memory context call, got %d", len(memoryClient.memoryContextCalls))
	}
	if len(assistant.messages) != 0 {
		t.Fatalf("expected channel activity facts to bypass LLM, got %#v", assistant.messages)
	}
	if len(memoryClient.skillCalls) != 0 {
		t.Fatalf("expected deterministic channel activity reply to skip skill lookup, got %#v", memoryClient.skillCalls)
	}
	if len(recorder.sent) != 1 {
		t.Fatalf("expected one Discord reply, got %#v", recorder.sent)
	}
	got := recorder.sent[0].Content
	for _, want := range []string{"BlackDave's top active channels:", "1. #💬┃general-chat - 247 messages", "2. #server-suggestion-box - 4 messages", "3. #🎇┃promos - 1 message"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected deterministic reply to contain %q, got %q", want, got)
		}
	}
	if strings.Contains(got, "should not be used") {
		t.Fatalf("expected LLM reply to stay unused, got %q", got)
	}
}

func TestConversationMemoryScopeSeparatesDMFromGuild(t *testing.T) {
	// Given
	dmMessage := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-dm",
		ChannelID: "dm-channel-1",
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
	}}
	guildMessage := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-guild",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
	}}

	// When
	dmScope := conversationMemoryScope(dmMessage)
	guildScope := conversationMemoryScope(guildMessage)

	// Then
	if dmScope.Visibility != memory.VisibilityPrivateUser || dmScope.GuildID != "" || dmScope.ChannelID != "dm-channel-1" || dmScope.SessionID != "dm:dm-channel-1" {
		t.Fatalf("expected DM scope to stay private-user only, got %#v", dmScope)
	}
	if guildScope.Visibility != memory.VisibilityGuild || guildScope.GuildID != "guild-1" || guildScope.ChannelID != "" || guildScope.SessionID != "guild:guild-1" {
		t.Fatalf("expected guild scope to use guild memory boundary, got %#v", guildScope)
	}
	if dmScope.SpaceID == guildScope.SpaceID || dmScope.SessionID == guildScope.SessionID {
		t.Fatalf("expected DM and guild scopes to be isolated, got dm=%#v guild=%#v", dmScope, guildScope)
	}
}

func TestConversationMemoryScopeSharesSameGuildChannels(t *testing.T) {
	// Given
	firstChannelMessage := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-first",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
	}}
	secondChannelMessage := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-second",
		ChannelID: "channel-2",
		GuildID:   "guild-1",
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
	}}

	// When
	firstScope := conversationMemoryScope(firstChannelMessage)
	secondScope := conversationMemoryScope(secondChannelMessage)

	// Then
	if firstScope.SessionID != "guild:guild-1" || secondScope.SessionID != "guild:guild-1" {
		t.Fatalf("expected guild-level session ids, got first=%#v second=%#v", firstScope, secondScope)
	}
	if firstScope.ChannelID != "" || secondScope.ChannelID != "" || firstScope.Visibility != memory.VisibilityGuild || secondScope.Visibility != memory.VisibilityGuild {
		t.Fatalf("expected same-guild channels to share guild memory scope, got first=%#v second=%#v", firstScope, secondScope)
	}
}
