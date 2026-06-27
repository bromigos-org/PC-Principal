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
		contextText: "Long-term recall: blackflame likes homelab memory",
		graphText:   "Graph fact: #general discussed Dragonfly yesterday",
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
	if len(memoryClient.graphCalls) != 1 {
		t.Fatalf("expected one graph context call, got %d", len(memoryClient.graphCalls))
	}
	graphCall := memoryClient.graphCalls[0]
	if graphCall.Query != "what did we say about memory?" || graphCall.Limit != graphContextLimit {
		t.Fatalf("expected graph query and limit to pass through, got %#v", graphCall)
	}
	if graphCall.Scope.Visibility != memory.VisibilityChannel || graphCall.Scope.GuildID != "guild-1" || graphCall.Scope.ChannelID != "channel-1" {
		t.Fatalf("expected channel-scoped graph request, got %#v", graphCall.Scope)
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	for _, want := range []string{"Recent Dragonfly conversation history:", "Relevant long-term recall:", "Relevant Discord graph facts:", "Long-term recall: blackflame likes homelab memory", "Graph fact: #general discussed Dragonfly yesterday"} {
		if !strings.Contains(joinedPrompt, want) {
			t.Fatalf("expected assistant prompt to contain %q, got %#v", want, assistant.messages)
		}
	}
}

func TestMentionConversationContinuesWhenGraphContextFails(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{graphErr: errors.New("graph unavailable")}
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

func TestMentionConversationDoesNotLeakOtherChannelGraphContext(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{graphText: "Graph fact: sibling channel secret"}
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
	if len(memoryClient.graphCalls) != 1 {
		t.Fatalf("expected one graph context call, got %d", len(memoryClient.graphCalls))
	}
	requestScope := memoryClient.graphCalls[0].Scope
	if requestScope.SessionID != "guild:guild-1:channel:channel-1" || requestScope.ChannelID != "channel-1" || requestScope.Visibility != memory.VisibilityChannel {
		t.Fatalf("expected graph request to stay in current channel scope, got %#v", requestScope)
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
	if guildScope.Visibility != memory.VisibilityChannel || guildScope.GuildID != "guild-1" || guildScope.ChannelID != "channel-1" || guildScope.SessionID != "guild:guild-1:channel:channel-1" {
		t.Fatalf("expected guild scope to stay channel-bound, got %#v", guildScope)
	}
	if dmScope.SpaceID == guildScope.SpaceID || dmScope.SessionID == guildScope.SessionID {
		t.Fatalf("expected DM and guild scopes to be isolated, got dm=%#v guild=%#v", dmScope, guildScope)
	}
}
