package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
)

func TestMentionConversationRendersEnrichedMemoryTypeSectionsInServiceOrder(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{memoryContext: memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{
		{MemoryType: "short_term", Content: "Continue the current Discord conversation thread."},
		{MemoryType: "long_term", Content: "Durable fact: blackflame prefers memory routing by durability."},
		{MemoryType: "entities", Content: "Entity: PC Principal is the Discord agent."},
		{MemoryType: "preferences", Content: "Preference: keep answers concise unless asked for depth."},
		{MemoryType: "dedup_notice", Content: "Dedup notice: graph overlap removed from long-term facts."},
		{MemoryType: "reasoning", Content: "Prior successful pattern: inspect memory contracts before changing prompts."},
		{MemoryType: "similar_traces", Content: "Similar trace: fallback handling succeeded previously."},
	}}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" how should you use memory?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected enriched memory conversation to succeed, got %v", err)
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	ordered := []string{
		"Source: short_term",
		"Continue the current Discord conversation thread.",
		"Source: long_term",
		"Durable fact: blackflame prefers memory routing by durability.",
		"Source: entities",
		"Entity: PC Principal is the Discord agent.",
		"Source: preferences",
		"Preference: keep answers concise unless asked for depth.",
		"Source: dedup_notice",
		"Dedup notice: graph overlap removed from long-term facts.",
		"Source: reasoning",
		"Prior successful pattern: inspect memory contracts before changing prompts.",
		"Source: similar_traces",
		"Similar trace: fallback handling succeeded previously.",
	}
	assertPromptOrder(t, joinedPrompt, ordered)
	if strings.Contains(joinedPrompt, "Recent Dragonfly conversation history:") {
		t.Fatalf("expected memory_type short-term section to suppress local short-term fallback, got %#v", assistant.messages)
	}
}

func TestMentionConversationContinuesWithoutLegacyContextWhenCombinedContextFails(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{
		memoryContextErr: errors.New("combined memory unavailable"),
		graphText:        "legacy graph fact",
	}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" use fallback memory")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected conversation to continue without legacy fallback, got %v", err)
	}
	if len(memoryClient.memoryContextCalls) != 1 || len(memoryClient.graphCalls) != 0 {
		t.Fatalf("expected only combined gnosis memory query, got combined=%d graph=%d", len(memoryClient.memoryContextCalls), len(memoryClient.graphCalls))
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	for _, blocked := range []string{"legacy short-term continuity", "legacy graph fact", "Relevant reviewed memory context:"} {
		if strings.Contains(joinedPrompt, blocked) {
			t.Fatalf("expected failed combined recall not to add %q to prompt, got %#v", blocked, assistant.messages)
		}
	}
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "I'm PC, Texas A&M!" {
		t.Fatalf("expected Discord reply without memory context, got %#v", recorder.sent)
	}
}

func assertPromptOrder(t *testing.T, prompt string, ordered []string) {
	t.Helper()
	previousIndex := -1
	for _, want := range ordered {
		index := strings.Index(prompt, want)
		if index == -1 {
			t.Fatalf("expected assistant prompt to contain %q, got %q", want, prompt)
		}
		if index <= previousIndex {
			t.Fatalf("expected %q to preserve service ordering in prompt %q", want, prompt)
		}
		previousIndex = index
	}
}
