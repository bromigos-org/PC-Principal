package run

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

type fakeLiveMemoryClient struct {
	ingestErr error
	events    []memory.ClientEvent
}

func (c *fakeLiveMemoryClient) GetContext(ctx context.Context, query memory.ContextQuery) (string, error) {
	return "", nil
}

func (c *fakeLiveMemoryClient) AddMessage(ctx context.Context, message memory.Message) error {
	return nil
}

func (c *fakeLiveMemoryClient) IngestEvent(ctx context.Context, event memory.ClientEvent) error {
	c.events = append(c.events, event)
	return c.ingestErr
}

func (c *fakeLiveMemoryClient) IngestEvents(ctx context.Context, events []memory.ClientEvent) (memory.ClientEventBatchResponse, error) {
	c.events = append(c.events, events...)
	return memory.ClientEventBatchResponse{}, c.ingestErr
}

func (c *fakeLiveMemoryClient) GetGraphContext(ctx context.Context, request memory.GraphContextRequest) (memory.GraphContextResponse, error) {
	return memory.GraphContextResponse{}, nil
}

func (c *fakeLiveMemoryClient) ListSkills(ctx context.Context, request memory.SkillListRequest) (memory.SkillListResponse, error) {
	return memory.SkillListResponse{}, nil
}

func (c *fakeLiveMemoryClient) ProposeSkill(ctx context.Context, proposal memory.SkillProposal) (memory.SkillProposal, error) {
	return memory.SkillProposal{}, nil
}

func (c *fakeLiveMemoryClient) RecordSkillUsage(ctx context.Context, usage memory.SkillUsage) error {
	return nil
}

func TestLiveMessageHandler_Ingests_non_mention_without_discord_reply(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	recorder := &runDiscordRecorder{}
	s, message := runSessionAndMessage(t, "quiet channel chatter")
	message.Mentions = nil
	s.Client = recorder.httpClient(t)

	// When
	liveMessageHandler(s, message)

	// Then
	if len(memoryClient.events) != 2 {
		t.Fatalf("expected message and user metadata events, got %d", len(memoryClient.events))
	}
	if memoryClient.events[0].EventType != memory.EventTypeMessageCreated || memoryClient.events[0].TenantID != "tenant-1" {
		t.Fatalf("expected normalized live message event, got %#v", memoryClient.events[0])
	}
	if len(recorder.sent) != 0 {
		t.Fatalf("expected no Discord replies for non-mentions, got %#v", recorder.sent)
	}
}

func TestLiveMessageHandler_Ingests_before_mention_command_routing(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	recorder := &runDiscordRecorder{}
	s, message := runSessionAndMessage(t, runMentionToken("bot-1")+" ping")
	s.Client = recorder.httpClient(t)

	// When
	liveMessageHandler(s, message)

	// Then
	if len(memoryClient.events) != 2 {
		t.Fatalf("expected mention message and user metadata ingest before routing, got %d events", len(memoryClient.events))
	}
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "Pong!" {
		t.Fatalf("expected ping command to win over conversation fallback, got %#v", recorder.sent)
	}
}

func TestLiveMessageHandler_Command_routing_continues_when_ingest_fails(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{ingestErr: errors.New("memory offline")}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	recorder := &runDiscordRecorder{}
	s, message := runSessionAndMessage(t, runMentionToken("bot-1")+" ping")
	s.Client = recorder.httpClient(t)

	// When
	liveMessageHandler(s, message)

	// Then
	if len(memoryClient.events) != 2 {
		t.Fatalf("expected failed ingest attempt to record message and user metadata events, got %d", len(memoryClient.events))
	}
	if len(recorder.sent) != 1 || recorder.sent[0].Content != "Pong!" {
		t.Fatalf("expected Discord command reply despite memory error, got %#v", recorder.sent)
	}
}

func TestLiveMessageHandler_Ingest_skips_bot_messages_and_replies(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	recorder := &runDiscordRecorder{}
	s, message := runSessionAndMessage(t, runMentionToken("bot-1")+" ping")
	message.Author = &discordgo.User{ID: "bot-2", Username: "other bot", Bot: true}
	s.Client = recorder.httpClient(t)

	// When
	liveMessageHandler(s, message)

	// Then
	if len(memoryClient.events) != 0 {
		t.Fatalf("expected bot message to skip ingestion, got %#v", memoryClient.events)
	}
	if len(recorder.sent) != 0 {
		t.Fatalf("expected bot message to receive no replies, got %#v", recorder.sent)
	}
}

func runSessionAndMessage(t *testing.T, content string) (*discordgo.Session, *discordgo.MessageCreate) {
	t.Helper()
	s := runSession(t)
	message := &discordgo.MessageCreate{Message: &discordgo.Message{
		ID:        "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Content:   content,
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
		Mentions:  []*discordgo.User{{ID: "bot-1", Username: "PC Principal", Bot: true}},
		Member:    &discordgo.Member{Roles: nil},
	}}
	return s, message
}

func runSession(t *testing.T) *discordgo.Session {
	t.Helper()
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	s.State.User = &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}
	return s
}

type runDiscordRecorder struct {
	sent []discordgo.MessageSend
}

func (r *runDiscordRecorder) httpClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: runRoundTripFunc(func(req *http.Request) (*http.Response, error) {
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

type runRoundTripFunc func(*http.Request) (*http.Response, error)

func (f runRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func runMentionToken(botID string) string {
	return "<@" + botID + ">"
}
