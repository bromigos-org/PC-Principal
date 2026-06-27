package commands

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
)

func TestMentionConversationText_routes_non_command_mentions(t *testing.T) {
	// Given
	content := mentionToken("bot-1") + " prove you're pc"

	// When
	got, ok := mentionConversationText(content, "bot-1")

	// Then
	if !ok {
		t.Fatal("expected natural mention text to route to conversation")
	}
	if got != "prove you're pc" {
		t.Fatalf("expected mention text %q, got %q", "prove you're pc", got)
	}
}

func TestMentionConversationText_preserves_hey_command_text(t *testing.T) {
	// Given
	content := mentionToken("bot-1") + " hey prove you're pc"

	// When
	got, ok := mentionConversationText(content, "bot-1")

	// Then
	if !ok {
		t.Fatal("expected hey mention text to route to conversation")
	}
	if got != "prove you're pc" {
		t.Fatalf("expected hey text %q, got %q", "prove you're pc", got)
	}
}

func TestMentionConversationText_returns_empty_for_mention_only(t *testing.T) {
	// Given
	content := mentionToken("bot-1")

	// When
	got, ok := mentionConversationText(content, "bot-1")

	// Then
	if !ok {
		t.Fatal("expected mention-only content to be recognized")
	}
	if got != "" {
		t.Fatalf("expected empty mention text, got %q", got)
	}
}

func TestChunkDiscordMessage_splits_long_response_under_limit(t *testing.T) {
	// Given
	text := strings.Repeat("a", discordMessageLimit+25)

	// When
	chunks := chunkDiscordMessage(text)

	// Then
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if len(chunk) > discordSafeMessageLimit {
			t.Fatalf("expected chunk <= %d, got %d", discordSafeMessageLimit, len(chunk))
		}
	}
	if strings.Join(chunks, "") != text {
		t.Fatal("expected chunks to preserve response content")
	}
}

func TestChunkDiscordMessage_handles_empty_response(t *testing.T) {
	// Given
	text := "   "

	// When
	chunks := chunkDiscordMessage(text)

	// Then
	if len(chunks) != 1 {
		t.Fatalf("expected 1 fallback chunk, got %d", len(chunks))
	}
	if chunks[0] != emptyAssistantResponse {
		t.Fatalf("expected empty response fallback %q, got %q", emptyAssistantResponse, chunks[0])
	}
}

func TestMentionConversation_uses_bounded_channel_memory_without_redis(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"I'm PC, Texas A&M! You PC, bro?"}}]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LITELLM_BASE_URL", server.URL)
	t.Setenv("LITELLM_API_KEY", "test-key")

	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" prove you're pc")

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected conversation without Redis, got %v", err)
	}
}

func TestMentionConversation_allows_all_discord_mention_types(t *testing.T) {
	// Given
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"<@user-2> <@&role-1> @everyone @here"}}]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("LITELLM_BASE_URL", server.URL)
	t.Setenv("LITELLM_API_KEY", "test-key")
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" prove you're pc")
	s.Client = recorder.httpClient(t)

	// When
	err := handleMentionConversation(s, m)

	// Then
	if err != nil {
		t.Fatalf("expected conversation to send safely, got %v", err)
	}
	if len(recorder.sent) != 1 {
		t.Fatalf("expected 1 Discord message, got %d", len(recorder.sent))
	}
	allowed := recorder.sent[0].AllowedMentions
	if allowed == nil || !allowedMentionParseIncludes(allowed.Parse, discordgo.AllowedMentionTypeUsers) || !allowedMentionParseIncludes(allowed.Parse, discordgo.AllowedMentionTypeRoles) || !allowedMentionParseIncludes(allowed.Parse, discordgo.AllowedMentionTypeEveryone) {
		t.Fatalf("expected model output to allow users, roles, everyone, and here, got %#v", allowed)
	}
	if recorder.sent[0].Content != "<@user-2> <@&role-1> @everyone @here" {
		t.Fatalf("expected content preserved without ping parsing, got %q", recorder.sent[0].Content)
	}
}

func allowedMentionParseIncludes(parse []discordgo.AllowedMentionType, want discordgo.AllowedMentionType) bool {
	for _, got := range parse {
		if got == want {
			return true
		}
	}
	return false
}

func TestStoreCallsAreSafe_without_initialized_redis(t *testing.T) {
	// When
	exists, existsErr := store.Exists(context.Background(), "channel-1")
	history, getErr := store.Get(context.Background(), "channel-1")
	saveErr := store.Save(context.Background(), "channel-1", nil)

	// Then
	if existsErr != nil || exists {
		t.Fatalf("expected safe missing store exists=false nil error, got exists=%v err=%v", exists, existsErr)
	}
	if getErr != nil || history != nil {
		t.Fatalf("expected safe missing store history=nil nil error, got history=%v err=%v", history, getErr)
	}
	if saveErr != nil {
		t.Fatalf("expected safe missing store save nil error, got %v", saveErr)
	}
}
