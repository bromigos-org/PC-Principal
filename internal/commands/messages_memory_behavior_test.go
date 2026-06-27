package commands

import "testing"

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
