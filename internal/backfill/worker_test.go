package backfill

import (
	"context"
	"errors"
	"github.com/bwmarrin/discordgo"
	"net/http"
	"testing"
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

func TestWorker_Run_ingests_backfill_without_discord_reply_surface(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	discord.channels["guild-1"] = []*discordgo.Channel{{ID: "channel-1", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText}}
	discord.messages["channel-1"] = [][]*discordgo.Message{{message("m-1")}}
	memoryClient := &fakeMemoryClient{}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: memoryClient, Cursors: newFakeCursorStore()}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 1, MaxMessagesPerChannel: 1, MemoryBatchSize: 1})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.MessagesIngested != 1 || len(memoryClient.batches) != 1 {
		t.Fatalf("expected backfill to ingest exactly one message, got summary=%#v batches=%d", summary, len(memoryClient.batches))
	}
	if len(discord.sent) != 0 {
		t.Fatalf("expected backfill to avoid Discord reply sends, got %#v", discord.sent)
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

func TestBackfillResumesAfterPartialFailure(t *testing.T) {
	// Given
	cursors := newFakeCursorStore()
	firstDiscord := newFakeDiscordClient()
	firstDiscord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	firstDiscord.channels["guild-1"] = []*discordgo.Channel{{ID: "channel-1", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText}}
	firstDiscord.messages["channel-1"] = [][]*discordgo.Message{{message("m-2"), message("m-1")}}
	worker := NewWorker(WorkerDeps{Discord: firstDiscord, Memory: &fakeMemoryClient{err: errors.New("memory unavailable")}, Cursors: cursors}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 1, MaxMessagesPerChannel: 10, MemoryBatchSize: 10, MaxAttempts: 1})

	// When
	_, err := worker.Run(context.Background())

	// Then
	if err == nil {
		t.Fatal("expected memory ingest failure")
	}
	if got := cursors.values[CursorKey{GuildID: "guild-1", ChannelID: "channel-1"}]; got != "" {
		t.Fatalf("expected cursor to stay unchanged after failed ingest, got %q", got)
	}

	// Given
	secondDiscord := newFakeDiscordClient()
	secondDiscord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	secondDiscord.channels["guild-1"] = []*discordgo.Channel{{ID: "channel-1", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText}}
	secondDiscord.messages["channel-1"] = [][]*discordgo.Message{{message("m-2"), message("m-1")}}
	resumeWorker := NewWorker(WorkerDeps{Discord: secondDiscord, Memory: &fakeMemoryClient{}, Cursors: cursors}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 1, MaxMessagesPerChannel: 10, MemoryBatchSize: 10, MaxAttempts: 1})

	// When
	resumeSummary, err := resumeWorker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("resume backfill: %v", err)
	}
	if resumeSummary.MessagesIngested != 2 {
		t.Fatalf("expected resumed run to ingest the original page, got %#v", resumeSummary)
	}
	if got := cursors.values[CursorKey{GuildID: "guild-1", ChannelID: "channel-1"}]; got != "m-1" {
		t.Fatalf("expected cursor to advance only after successful ingest, got %q", got)
	}
	if len(secondDiscord.messageCalls) == 0 || secondDiscord.messageCalls[0].beforeID != "" {
		t.Fatalf("expected resumed run to start from the original cursor, got %#v", secondDiscord.messageCalls)
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
