package commands

import (
	"errors"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
)

func TestMentionConversationIncludesApprovedSkills(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{skills: []memory.SkillRecord{
		{
			SkillID:     "skill-approved",
			TenantID:    "bromigos",
			AgentID:     "pc-principal",
			Name:        "Summarize channel",
			Description: "Summarize visible Discord channel context for review.",
			Status:      memory.SkillStatusApproved,
			Scope:       memory.VisibilityAgentShared,
			Metadata:    memory.JsonObject{"reviewed": true},
		},
	}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" what skills can you use?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected skill-backed conversation to succeed, got %v", err)
	}
	if len(memoryClient.skillCalls) != 1 {
		t.Fatalf("expected one skill list call, got %d", len(memoryClient.skillCalls))
	}
	if memoryClient.skillCalls[0].TenantID != "bromigos" || memoryClient.skillCalls[0].AgentID != "pc-principal" {
		t.Fatalf("expected PC Principal skill list request, got %#v", memoryClient.skillCalls[0])
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	for _, want := range []string{"Reviewed non-executable skills:", "Summarize channel", "Summarize visible Discord channel context for review."} {
		if !strings.Contains(joinedPrompt, want) {
			t.Fatalf("expected assistant prompt to contain %q, got %#v", want, assistant.messages)
		}
	}
}

func TestMentionConversationOmitsUnapprovedSkills(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{skills: []memory.SkillRecord{
		{
			SkillID:     "skill-proposed",
			TenantID:    "bromigos",
			AgentID:     "pc-principal",
			Name:        "Draft shell helper",
			Description: "Unreviewed proposal that must not become runnable.",
			Status:      memory.SkillStatusProposed,
			Scope:       memory.VisibilityAgentShared,
			Metadata:    memory.JsonObject{"reviewed": false},
		},
	}}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(memoryClient)
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" can you run the draft?")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected conversation to ignore unapproved skills, got %v", err)
	}
	joinedPrompt := assistantPromptText(assistant.messages)
	for _, blocked := range []string{"Draft shell helper", "Unreviewed proposal", "skill-proposed"} {
		if strings.Contains(joinedPrompt, blocked) {
			t.Fatalf("expected unapproved skill %q to stay out of prompt, got %#v", blocked, assistant.messages)
		}
	}
}

func TestMentionConversationContinuesWhenSkillListFails(t *testing.T) {
	// Given
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	memoryClient := &fakeMemoryClient{skillsErr: errors.New("skills unavailable")}
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
		t.Fatalf("expected skill list errors to stay non-fatal, got %v", err)
	}
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "I'm PC, Texas A&M!" {
		t.Fatalf("expected Discord reply despite skill list errors, got %#v", recorder.sent)
	}
	if strings.Contains(assistantPromptText(assistant.messages), "skills unavailable") {
		t.Fatalf("expected skill error details to stay out of prompt, got %#v", assistant.messages)
	}
}
