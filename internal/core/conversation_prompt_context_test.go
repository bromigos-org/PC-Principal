package core

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
)

func TestHistoryWithMemoryContext_puts_context_after_the_persona(t *testing.T) {
	// Given
	history := []store.Message{
		{Role: "system", Content: "persona"},
		{Role: "user", Content: "[blackflame]: one"},
	}
	recalled := conversationMemoryPromptResult{prompt: "Relevant reviewed memory context:\nSource: graph\na fact"}
	skills := []memory.SkillRecord{{Name: "Summarize", Description: "Summarize the room.", Status: memory.SkillStatusApproved, Metadata: memory.JsonObject{"reviewed": true}}}

	// When
	got := historyWithMemoryContext(history, recalled, skills)

	// Then
	want := []store.Message{
		{Role: "system", Content: "persona"},
		{Role: "system", Content: "Recent Dragonfly conversation history:\nuser: [blackflame]: one"},
		{Role: "system", Content: "Relevant reviewed memory context:\nSource: graph\na fact"},
		{Role: "system", Content: "Reviewed non-executable skills:\nSummarize: Summarize the room."},
		{Role: "user", Content: "[blackflame]: one"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected request history:\n got %#v\nwant %#v", got, want)
	}
	if len(history) != 2 {
		t.Fatalf("expected the stored history to be left alone, got %#v", history)
	}
}

func TestHistoryWithMemoryContext_puts_context_first_when_there_is_no_persona(t *testing.T) {
	// Given
	history := []store.Message{{Role: "user", Content: "one"}}

	// When
	got := historyWithMemoryContext(history, conversationMemoryPromptResult{prompt: "recalled"}, nil)

	// Then
	want := []store.Message{
		{Role: "system", Content: "Recent Dragonfly conversation history:\nuser: one"},
		{Role: "system", Content: "recalled"},
		{Role: "user", Content: "one"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected request history:\n got %#v\nwant %#v", got, want)
	}
}

func TestHistoryWithMemoryContext_returns_the_history_itself_when_there_is_nothing_to_add(t *testing.T) {
	// Given
	history := []store.Message{{Role: "system", Content: "persona"}}

	// When
	got := historyWithMemoryContext(history, conversationMemoryPromptResult{}, nil)

	// Then
	if !reflect.DeepEqual(got, history) {
		t.Fatalf("expected the history unchanged, got %#v", got)
	}
}

func TestSkillPromptContext_lists_only_approved_reviewed_skills_with_a_name_and_a_description(t *testing.T) {
	// Given
	reviewed := memory.JsonObject{"reviewed": true}
	skills := []memory.SkillRecord{
		{Name: "Kept", Description: "Approved and reviewed.", Status: memory.SkillStatusApproved, Metadata: reviewed},
		{Name: "Proposed", Description: "Not approved.", Status: memory.SkillStatusProposed, Metadata: reviewed},
		{Name: "Unreviewed", Description: "No review mark.", Status: memory.SkillStatusApproved},
		{Name: "Text mark", Description: "Review mark is not a boolean.", Status: memory.SkillStatusApproved, Metadata: memory.JsonObject{"reviewed": "true"}},
		{Name: "  ", Description: "No name.", Status: memory.SkillStatusApproved, Metadata: reviewed},
		{Name: "No description", Description: " ", Status: memory.SkillStatusApproved, Metadata: reviewed},
		{Name: " Also kept ", Description: " Trimmed. ", Status: memory.SkillStatusApproved, Metadata: reviewed},
	}

	// When
	got := skillPromptContext(skills)

	// Then
	if want := "Kept: Approved and reviewed.\nAlso kept: Trimmed."; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestCombinedMemoryPrompt_renders_sections_in_service_order_with_sorted_facts(t *testing.T) {
	// Given
	response := memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{
		{Source: "ignored_when_type_is_set", MemoryType: "long_term", Content: " durable ", Facts: []memory.JsonObject{{"b": 2, "a": "one", "skipped": nil, " ": "blank key"}, {}}},
		{Source: "empty"},
		{Source: "graph", Content: "a graph fact"},
	}}

	// When
	got := combinedMemoryPrompt(response)

	// Then
	want := "Relevant reviewed memory context:\nSource: long_term\ndurable\nFact: a=one; b=2\n\nSource: empty\n\nSource: graph\na graph fact"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
	if combinedMemoryPrompt(memory.MemoryContextResponse{}) != "" {
		t.Fatal("expected no prompt for no sections")
	}
}

func TestChannelActivityReply_lists_at_most_five_channels_by_rank(t *testing.T) {
	// Given
	facts := make([]memory.JsonObject, 0, 7)
	for rank := 7; rank >= 1; rank-- {
		facts = append(facts, memory.JsonObject{"type": "channel_activity", "rank": rank, "user_display_name": "BlackDave", "channel_name": fmt.Sprintf("channel-%d", rank), "message_count": int64(rank)})
	}
	response := memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{Facts: facts}}}

	// When
	got := channelActivityReply(response)

	// Then
	want := "BlackDave's top active channels:\n1. #channel-1 - 1 message\n2. #channel-2 - 2 messages\n3. #channel-3 - 3 messages\n4. #channel-4 - 4 messages\n5. #channel-5 - 5 messages"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestChannelActivityReply_ignores_facts_that_are_not_complete_channel_activity(t *testing.T) {
	// Given
	response := memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{Facts: []memory.JsonObject{
		{"type": "preference", "rank": 1, "user_display_name": "BlackDave", "channel_name": "general", "message_count": 3},
		{"type": "channel_activity", "rank": 1.5, "user_display_name": "BlackDave", "channel_name": "general", "message_count": 3},
		{"type": "channel_activity", "rank": 1, "user_display_name": "BlackDave", "channel_name": "general", "message_count": "3"},
		{"type": "channel_activity", "rank": 1, "user_display_name": "BlackDave", "channel_name": "  ", "message_count": 3},
		{"type": "channel_activity", "rank": 1, "user_display_name": "", "channel_name": "general", "message_count": 3},
	}}}}

	// When
	got := channelActivityReply(response)

	// Then
	if got != "" {
		t.Fatalf("expected no deterministic reply, got %q", got)
	}
}
