package backfill

import (
	"context"
	"errors"
	"net/http"
	"testing"

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
	if len(memoryClient.batches) != 4 {
		t.Fatalf("expected topology batch plus bounded message batches of 50, 50, then 1, got %d", len(memoryClient.batches))
	}
	if discord.messageCalls[1].beforeID != "m-2" {
		t.Fatalf("expected second page before oldest fetched message m-2, got %q", discord.messageCalls[1].beforeID)
	}
	if marker, ok := memoryClient.batches[1][0].Payload["source_marker"].(string); !ok || marker != "backfill" {
		t.Fatalf("expected backfill source marker, got %#v", memoryClient.batches[1][0].Payload)
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
	if summary.MessagesIngested != 1 || len(memoryClient.batches) != 2 {
		t.Fatalf("expected topology and message ingest without reply surface, got summary=%#v batches=%d", summary, len(memoryClient.batches))
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

func TestWorker_Run_traverses_all_channels_and_messages_when_limits_are_zero(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	discord.channels["guild-1"] = []*discordgo.Channel{
		{ID: "channel-1", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
		{ID: "channel-2", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
		{ID: "channel-3", GuildID: "guild-1", Type: discordgo.ChannelTypeGuildText},
	}
	discord.messages["channel-1"] = [][]*discordgo.Message{messagesInChannel("channel-1", 100, 300), messagesInChannel("channel-1", 100, 200), messagesInChannel("channel-1", 100, 100), {messageInChannel("channel-1", "m-0")}}
	discord.messages["channel-2"] = [][]*discordgo.Message{{messageInChannel("channel-2", "m-1")}}
	discord.messages["channel-3"] = [][]*discordgo.Message{{messageInChannel("channel-3", "m-1")}}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: &fakeMemoryClient{}, Cursors: newFakeCursorStore()}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 0, MaxMessagesPerChannel: 0, MemoryBatchSize: 75})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.ChannelsVisited != 3 || summary.MessagesIngested != 303 {
		t.Fatalf("expected unlimited run to visit all channels and messages, got %#v", summary)
	}
	for _, call := range discord.messageCalls {
		if call.limit > discordPageLimit {
			t.Fatalf("expected Discord page limit <= %d, got %#v", discordPageLimit, discord.messageCalls)
		}
	}
}

func TestWorker_Run_emits_guild_topology_before_history_when_discord_data_available(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-1"}}
	category := &discordgo.Channel{ID: "category-1", GuildID: "guild-1", Name: "Projects", Type: discordgo.ChannelTypeGuildCategory}
	channel := &discordgo.Channel{ID: "channel-1", GuildID: "guild-1", Name: "general", ParentID: category.ID, Type: discordgo.ChannelTypeGuildText}
	role := &discordgo.Role{ID: "role-1", Name: "Admins"}
	member := &discordgo.Member{GuildID: "guild-1", User: &discordgo.User{ID: "user-1", Username: "Alex", Bot: true}, Roles: []string{role.ID}}
	discord.channels["guild-1"] = []*discordgo.Channel{category, channel}
	discord.roles["guild-1"] = []*discordgo.Role{role}
	discord.members["guild-1"] = [][]*discordgo.Member{{member}}
	discord.messages["channel-1"] = [][]*discordgo.Message{{messageInChannel("channel-1", "m-1")}}
	memoryClient := &fakeMemoryClient{}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: memoryClient, Cursors: newFakeCursorStore()}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 0, MaxMessagesPerChannel: 1, MemoryBatchSize: 10})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.ChannelsVisited != 1 || summary.MessagesIngested != 1 {
		t.Fatalf("expected history channel plus topology ingest, got %#v", summary)
	}
	if !hasEventType(memoryClient.events(), memory.EventTypeChannelCreated) || !hasEventType(memoryClient.events(), memory.EventTypeRoleCreated) || !hasEventType(memoryClient.events(), memory.EventTypeMemberUpdated) || !hasEventType(memoryClient.events(), memory.EventTypeUserDiscovered) || !hasEventType(memoryClient.events(), memory.EventTypeMemberRoleAssigned) {
		t.Fatalf("expected channel, role, member, user, and assignment topology events, got %#v", eventTypes(memoryClient.events()))
	}
}

func hasEventType(events []memory.ClientEvent, eventType memory.EventType) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func eventTypes(events []memory.ClientEvent) []memory.EventType {
	result := make([]memory.EventType, 0, len(events))
	for _, event := range events {
		result = append(result, event.EventType)
	}
	return result
}
