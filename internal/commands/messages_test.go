package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestCommandLookupCaseInsensitive(t *testing.T) {
	register("pctest", "", "", nil)

	cases := []string{"pctest", "PCTEST", "PcTest", "PCTest"}
	for _, input := range cases {
		lower := strings.ToLower(input)
		found := false
		for _, cmd := range registry {
			if cmd.Name == lower {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command %q not found after ToLower → %q", input, lower)
		}
	}
}

func TestBotMention_preserves_registered_command_precedence_for_ping(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" ping")
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 1 {
		t.Fatalf("expected 1 Discord message, got %d", len(recorder.sent))
	}
	if recorder.sent[0].Content != "Pong!" {
		t.Fatalf("expected ping command to win over conversation fallback, got %q", recorder.sent[0].Content)
	}
}

func TestBotMention_preserves_registered_command_precedence_for_hey(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" hey")
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 1 {
		t.Fatalf("expected 1 Discord message, got %d", len(recorder.sent))
	}
	if !strings.Contains(recorder.sent[0].Content, "say something after 'hey'") {
		t.Fatalf("expected hey command prompt to win over mention fallback, got %q", recorder.sent[0].Content)
	}
}

func TestBotMention_preserves_allowed_role_gate_for_commands(t *testing.T) {
	// Given
	previousAllowedRoles := allowedRoles
	allowedRoles = map[string]struct{}{"role-allowed": {}}
	t.Cleanup(func() { allowedRoles = previousAllowedRoles })
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, mentionToken("bot-1")+" ping")
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 0 {
		t.Fatalf("expected denied role to suppress command replies, got %#v", recorder.sent)
	}
}

func mentionSessionAndMessage(t *testing.T, content string) (*discordgo.Session, *discordgo.MessageCreate) {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	s.State.User = &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}
	s.Client = (&discordMessageRecorder{}).httpClient(t)
	m := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Content:   content,
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
		Mentions:  []*discordgo.User{{ID: "bot-1", Username: "PC Principal", Bot: true}},
		Member:    &discordgo.Member{Roles: nil},
	}}
	return s, m
}

type discordMessageRecorder struct {
	sent []discordgo.MessageSend
}

func (r *discordMessageRecorder) httpClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusInternalServerError
		body := `{"message":"test failure"}`
		if req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/channels/channel-1/messages") {
			var sent discordgo.MessageSend
			if err := json.NewDecoder(req.Body).Decode(&sent); err != nil {
				t.Fatalf("decode Discord message send: %v", err)
			}
			r.sent = append(r.sent, sent)
			status = http.StatusOK
			body = `{"id":"sent-1","channel_id":"channel-1","content":"ok"}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func mentionToken(botID string) string {
	return "<@" + botID + ">"
}
