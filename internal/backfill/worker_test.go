package backfill

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestWorker_Run_ingests_paginated_messages_and_advances_cursor_after_success(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	discord.channels["guild-1"] = []*discordgo.Channel{{ID: "channel-1", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText}}
	discord.messages["channel-1"] = [][]*discordgo.Message{
		messages(100, 101),
		{message("m-1")},
	}
	cursors := newFakeCursorStore()
	memoryClient := &fakeMemoryClient{}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: memoryClient, Cursors: cursors}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 2, MaxMessagesPerChannel: 101, MemoryBatchSize: 50})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.ChannelsVisited != 1 || summary.MessagesIngested != 101 {
		t.Fatalf("expected one channel and 101 messages, got %#v", summary)
	}
	if got := cursors.values[CursorKey{GuildID: "guild-1", ChannelID: "channel-1"}]; got != "m-1" {
		t.Fatalf("expected cursor to oldest ingested message m-1, got %q", got)
	}
	if len(memoryClient.batches) != 3 {
		t.Fatalf("expected bounded batches of 50, 50, then 1, got %d", len(memoryClient.batches))
	}
	if discord.messageCalls[1].beforeID != "m-2" {
		t.Fatalf("expected second page before oldest fetched message m-2, got %q", discord.messageCalls[1].beforeID)
	}
	if marker, ok := memoryClient.batches[0][0].Payload["source_marker"].(string); !ok || marker != "backfill" {
		t.Fatalf("expected backfill source marker, got %#v", memoryClient.batches[0][0].Payload)
	}
}

func TestWorker_Run_skips_permission_denied_channels(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	discord.channels["guild-1"] = []*discordgo.Channel{
		{ID: "denied", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
		{ID: "allowed", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
	}
	discord.messages["allowed"] = [][]*discordgo.Message{{message("m-1")}}
	discord.messageErrs["denied"] = &discordgo.RESTError{Response: &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden"}}
	memoryClient := &fakeMemoryClient{}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: memoryClient, Cursors: newFakeCursorStore()}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 5, MaxMessagesPerChannel: 5, MemoryBatchSize: 5})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.ChannelsSkipped != 1 || summary.MessagesIngested != 1 {
		t.Fatalf("expected one denied skip and one ingested message, got %#v", summary)
	}
}

func TestWorker_Run_does_not_advance_cursor_when_memory_ingest_fails(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	discord.channels["guild-1"] = []*discordgo.Channel{{ID: "channel-1", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText}}
	discord.messages["channel-1"] = [][]*discordgo.Message{{message("m-2"), message("m-1")}}
	cursors := newFakeCursorStore()
	memoryClient := &fakeMemoryClient{err: errors.New("memory unavailable")}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: memoryClient, Cursors: cursors}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 1, MaxMessagesPerChannel: 10, MemoryBatchSize: 10, MaxAttempts: 1})

	// When
	_, err := worker.Run(context.Background())

	// Then
	if err == nil {
		t.Fatal("expected memory ingest failure")
	}
	if got := cursors.values[CursorKey{GuildID: "guild-1", ChannelID: "channel-1"}]; got != "" {
		t.Fatalf("expected cursor not to advance after failed ingest, got %q", got)
	}
}

func TestWorker_Run_resumes_from_saved_cursor_and_honors_channel_limit(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	discord.channels["guild-1"] = []*discordgo.Channel{
		{ID: "channel-1", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
		{ID: "channel-2", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
	}
	discord.messages["channel-1"] = [][]*discordgo.Message{{message("m-1")}}
	cursors := newFakeCursorStore()
	cursors.values[CursorKey{GuildID: "guild-1", ChannelID: "channel-1"}] = "cursor-9"
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: &fakeMemoryClient{}, Cursors: cursors}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 1, MaxMessagesPerChannel: 1, MemoryBatchSize: 1})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.ChannelsVisited != 1 || len(discord.messageCalls) != 1 {
		t.Fatalf("expected channel limit to stop after one channel, got summary %#v calls %d", summary, len(discord.messageCalls))
	}
	if discord.messageCalls[0].beforeID != "cursor-9" {
		t.Fatalf("expected saved cursor as before id, got %q", discord.messageCalls[0].beforeID)
	}
}

func TestWorker_Run_fetches_text_channels_and_active_threads_only(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	discord.channels["guild-1"] = []*discordgo.Channel{
		{ID: "text", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
		{ID: "forum", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildForum},
		{ID: "voice", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildVoice},
	}
	discord.threads["guild-1"] = []*discordgo.Channel{{ID: "thread", GuildID: "guild-1", ParentID: "forum", Type: discordgo.ChannelTypeGuildPublicThread}}
	discord.messages["text"] = [][]*discordgo.Message{{messageInChannel("text", "m-2")}}
	discord.messages["thread"] = [][]*discordgo.Message{{messageInChannel("thread", "m-1")}}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: &fakeMemoryClient{}, Cursors: newFakeCursorStore()}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 5, MaxMessagesPerChannel: 1, MemoryBatchSize: 1})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.ChannelsVisited != 2 || summary.MessagesIngested != 2 {
		t.Fatalf("expected text channel and active thread only, got %#v", summary)
	}
	if got := calledChannels(discord.messageCalls); got != "text,thread" {
		t.Fatalf("expected only text and active thread fetches, got %q", got)
	}
}

func TestDefaultConfig_is_disabled_and_bounded(t *testing.T) {
	// When
	config := DefaultConfig()

	// Then
	if config.Enabled {
		t.Fatal("expected backfill disabled by default")
	}
	if config.MaxChannelsPerRun <= 0 || config.MaxMessagesPerChannel <= 0 || config.MemoryBatchSize <= 0 || config.RequestDelay < 0 || config.Backoff <= 0 {
		t.Fatalf("expected bounded positive defaults, got %#v", config)
	}
}

type fakeDiscordClient struct {
	guilds       []*discordgo.UserGuild
	channels     map[string][]*discordgo.Channel
	threads      map[string][]*discordgo.Channel
	messages     map[string][][]*discordgo.Message
	messageErrs  map[string]error
	messageCalls []messageCall
}

type messageCall struct {
	channelID string
	limit     int
	beforeID  string
}

func newFakeDiscordClient() *fakeDiscordClient {
	return &fakeDiscordClient{channels: map[string][]*discordgo.Channel{}, threads: map[string][]*discordgo.Channel{}, messages: map[string][][]*discordgo.Message{}, messageErrs: map[string]error{}}
}

func (c *fakeDiscordClient) UserGuilds(ctx context.Context, limit int, beforeID string) ([]*discordgo.UserGuild, error) {
	return c.guilds, nil
}

func (c *fakeDiscordClient) GuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	return c.channels[guildID], nil
}

func (c *fakeDiscordClient) GuildThreadsActive(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	return c.threads[guildID], nil
}

func (c *fakeDiscordClient) ChannelMessages(ctx context.Context, channelID string, limit int, beforeID string) ([]*discordgo.Message, error) {
	c.messageCalls = append(c.messageCalls, messageCall{channelID: channelID, limit: limit, beforeID: beforeID})
	if err := c.messageErrs[channelID]; err != nil {
		return nil, err
	}
	pages := c.messages[channelID]
	if len(pages) == 0 {
		return nil, nil
	}
	c.messages[channelID] = pages[1:]
	return pages[0], nil
}

type fakeMemoryClient struct {
	err     error
	batches [][]memory.ClientEvent
}

func (c *fakeMemoryClient) IngestEvents(ctx context.Context, events []memory.ClientEvent) (memory.ClientEventBatchResponse, error) {
	c.batches = append(c.batches, append([]memory.ClientEvent(nil), events...))
	return memory.ClientEventBatchResponse{}, c.err
}

type fakeCursorStore struct {
	values map[CursorKey]string
}

func newFakeCursorStore() *fakeCursorStore {
	return &fakeCursorStore{values: map[CursorKey]string{}}
}

func (s *fakeCursorStore) Get(ctx context.Context, key CursorKey) (string, error) {
	return s.values[key], nil
}

func (s *fakeCursorStore) Save(ctx context.Context, key CursorKey, beforeID string) error {
	s.values[key] = beforeID
	return nil
}

func message(id string) *discordgo.Message {
	return messageInChannel("channel-1", id)
}

func messageInChannel(channelID string, id string) *discordgo.Message {
	return &discordgo.Message{ID: id, ChannelID: channelID, GuildID: "guild-1", Content: "hello " + id, Timestamp: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), Author: &discordgo.User{ID: "user-1", Username: "Alex"}}
}

func messages(count int, newest int) []*discordgo.Message {
	result := make([]*discordgo.Message, 0, count)
	for id := newest; id > newest-count; id-- {
		result = append(result, message("m-"+strconv.Itoa(id)))
	}
	return result
}

func calledChannels(calls []messageCall) string {
	ids := make([]string, 0, len(calls))
	for _, call := range calls {
		ids = append(ids, call.channelID)
	}
	return strings.Join(ids, ",")
}
