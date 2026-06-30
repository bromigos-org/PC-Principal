package commands

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/ambient"
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

func TestBotMentionCommandPrecedenceStillWins(t *testing.T) {
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

func TestNonMentionMessageIngestsWithoutReply(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	s, m := mentionSessionAndMessage(t, "quiet channel chatter")
	m.Mentions = nil
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, m)

	// Then
	if len(recorder.sent) != 0 {
		t.Fatalf("expected non-mention message to stay silent, got %#v", recorder.sent)
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

func TestAmbientReply_activates_after_successful_mention_conversation(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	store := newCommandAmbientStore()
	manager := ambient.NewManager(ambient.Config{Enabled: true}, store, commandAmbientClock{now: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)})
	ConfigureAmbient(manager)
	t.Cleanup(func() { ConfigureAmbient(nil) })
	assistant := &fakeAssistantClient{reply: "I'm PC, Texas A&M!"}
	previousAssistant := assistantClient
	assistantClient = assistant
	t.Cleanup(func() { assistantClient = previousAssistant })
	previousMemory := conversationMemory
	ConfigureMemory(&fakeMemoryClient{})
	t.Cleanup(func() { ConfigureMemory(previousMemory) })
	s, mention := mentionSessionAndMessage(t, mentionToken("bot-1")+" are you pc?")
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, mention)
	AmbientReply(s, ambientMessageCreate("I am also PC", nil))

	// Then
	if len(recorder.sent) != 2 {
		t.Fatalf("expected mention reply and ambient reply, got %#v", recorder.sent)
	}
	if len(assistant.messages) == 0 || !strings.Contains(assistantPromptText(assistant.messages), "I am also PC") {
		t.Fatalf("expected ambient reply to reuse conversation prompt path, got %#v", assistant.messages)
	}
}

func TestAmbientReply_does_not_reply_outside_active_session(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	ConfigureAmbient(ambient.NewManager(ambient.Config{Enabled: true}, newCommandAmbientStore(), commandAmbientClock{now: time.Now().UTC()}))
	t.Cleanup(func() { ConfigureAmbient(nil) })
	s, message := mentionSessionAndMessage(t, "random visible chatter")
	message.Mentions = nil
	s.Client = recorder.httpClient(t)

	// When
	AmbientReply(s, message)

	// Then
	if len(recorder.sent) != 0 {
		t.Fatalf("expected no ambient reply outside active session, got %#v", recorder.sent)
	}
}

func TestBotMention_stop_phrase_clears_ambient_without_conversation_reply(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	store := newCommandAmbientStore()
	manager := ambient.NewManager(ambient.Config{Enabled: true}, store, commandAmbientClock{now: time.Now().UTC()})
	ConfigureAmbient(manager)
	t.Cleanup(func() { ConfigureAmbient(nil) })
	requireCommandNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))
	s, message := mentionSessionAndMessage(t, mentionToken("bot-1")+" stop")
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, message)

	// Then
	if len(recorder.sent) != 0 {
		t.Fatalf("expected stop phrase to suppress mention reply, got %#v", recorder.sent)
	}
	if _, ok := store.states[ambient.Key("channel-1")]; ok {
		t.Fatal("expected stop phrase to clear ambient state")
	}
}

func TestAmbientReply_preserves_allowed_role_gate(t *testing.T) {
	// Given
	previousAllowedRoles := allowedRoles
	allowedRoles = map[string]struct{}{"role-allowed": {}}
	t.Cleanup(func() { allowedRoles = previousAllowedRoles })
	recorder := &discordMessageRecorder{}
	manager := ambient.NewManager(ambient.Config{Enabled: true}, newCommandAmbientStore(), commandAmbientClock{now: time.Now().UTC()})
	ConfigureAmbient(manager)
	t.Cleanup(func() { ConfigureAmbient(nil) })
	requireCommandNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))
	s, message := mentionSessionAndMessage(t, "ambient denied by role")
	message.Mentions = nil
	s.Client = recorder.httpClient(t)

	// When
	AmbientReply(s, message)

	// Then
	if len(recorder.sent) != 0 {
		t.Fatalf("expected denied role to suppress ambient reply, got %#v", recorder.sent)
	}
}

func TestBotMention_preserves_command_precedence_when_ambient_active(t *testing.T) {
	// Given
	recorder := &discordMessageRecorder{}
	manager := ambient.NewManager(ambient.Config{Enabled: true}, newCommandAmbientStore(), commandAmbientClock{now: time.Now().UTC()})
	ConfigureAmbient(manager)
	t.Cleanup(func() { ConfigureAmbient(nil) })
	requireCommandNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))
	s, message := mentionSessionAndMessage(t, mentionToken("bot-1")+" ping")
	s.Client = recorder.httpClient(t)

	// When
	BotMention(s, message)

	// Then
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "Pong!" {
		t.Fatalf("expected command to bypass ambient conversation, got %#v", recorder.sent)
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

func ambientMessageCreate(content string, referenced *discordgo.Message) *discordgo.MessageCreate {
	return &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:                "message-ambient",
		ChannelID:         "channel-1",
		GuildID:           "guild-1",
		Content:           content,
		Author:            &discordgo.User{ID: "user-1", Username: "blackflame"},
		Member:            &discordgo.Member{Roles: nil},
		ReferencedMessage: referenced,
	}}
}

func requireCommandNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

type commandAmbientClock struct {
	now time.Time
}

func (c commandAmbientClock) Now() time.Time { return c.now }

type commandAmbientStore struct {
	states map[string]ambient.State
}

func newCommandAmbientStore() *commandAmbientStore {
	return &commandAmbientStore{states: map[string]ambient.State{}}
}

func (s *commandAmbientStore) Load(ctx context.Context, key string) (ambient.State, bool, error) {
	state, ok := s.states[key]
	return state, ok, nil
}

func (s *commandAmbientStore) Save(ctx context.Context, key string, state ambient.State, ttl time.Duration) error {
	s.states[key] = state
	return nil
}

func (s *commandAmbientStore) Delete(ctx context.Context, key string) error {
	delete(s.states, key)
	return nil
}
