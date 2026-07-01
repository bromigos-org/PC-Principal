package backfill

import (
	"context"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestWorker_Run_uses_guild_list_id_for_history_when_channel_guild_id_is_wrong(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-actual"}}
	discord.channels["guild-actual"] = []*discordgo.Channel{{ID: "channel-1", GuildID: "channel-1", Type: discordgo.ChannelTypeGuildText}}
	discord.messages["channel-1"] = [][]*discordgo.Message{{{ID: "m-1", ChannelID: "channel-1", GuildID: "channel-1", Content: "hello", Author: &discordgo.User{ID: "user-1", Username: "Alex"}}}}
	cursors := newFakeCursorStore()
	memoryClient := &fakeMemoryClient{}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: memoryClient, Cursors: cursors}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 1, MaxMessagesPerChannel: 1, MemoryBatchSize: 10})

	// When
	summary, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	if summary.MessagesIngested != 1 {
		t.Fatalf("expected one history message, got %#v", summary)
	}
	if got := cursors.values[CursorKey{GuildID: "guild-actual", ChannelID: "channel-1"}]; got != "m-1" {
		t.Fatalf("expected trusted guild cursor to advance, got %q", got)
	}
	if got := cursors.values[CursorKey{GuildID: "channel-1", ChannelID: "channel-1"}]; got != "" {
		t.Fatalf("expected poisoned guild cursor to stay empty, got %q", got)
	}
	messageEvent := firstEventOfType(memoryClient.events(), memory.EventTypeMessageCreated)
	if messageEvent == nil {
		t.Fatalf("expected message-created event, got %#v", eventTypes(memoryClient.events()))
	}
	if messageEvent.Payload["guild_id"] != "guild-actual" {
		t.Fatalf("expected message payload to use trusted guild id, got %#v", messageEvent.Payload)
	}
	if messageEvent.Discord.GuildID != "guild-actual" {
		t.Fatalf("expected message discord context to use trusted guild id, got %#v", messageEvent.Discord)
	}
	if messageEvent.Scope.GuildID != "guild-actual" {
		t.Fatalf("expected message scope to use trusted guild id, got %#v", messageEvent.Scope)
	}
}

func firstEventOfType(events []memory.ClientEvent, eventType memory.EventType) *memory.ClientEvent {
	for i := range events {
		if events[i].EventType == eventType {
			return &events[i]
		}
	}
	return nil
}
