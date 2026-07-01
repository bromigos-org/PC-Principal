package backfill

import (
	"context"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestWorker_Run_uses_guild_list_id_for_topology_when_channel_guild_id_is_wrong(t *testing.T) {
	// Given
	discord := newFakeDiscordClient()
	discord.guilds = []*discordgo.UserGuild{{ID: "guild-actual"}}
	channel := &discordgo.Channel{ID: "channel-1", GuildID: "channel-1", Name: "general", Type: discordgo.ChannelTypeGuildText}
	thread := &discordgo.Channel{ID: "thread-1", GuildID: "channel-1", ParentID: channel.ID, Type: discordgo.ChannelTypeGuildPublicThread}
	discord.channels["guild-actual"] = []*discordgo.Channel{channel}
	discord.threads["guild-actual"] = []*discordgo.Channel{thread}
	memoryClient := &fakeMemoryClient{}
	worker := NewWorker(WorkerDeps{Discord: discord, Memory: memoryClient, Cursors: newFakeCursorStore()}, Config{Enabled: true, TenantID: "tenant-1", AgentID: "agent-1", MaxChannelsPerRun: 0, MaxMessagesPerChannel: 0, MemoryBatchSize: 10})

	// When
	_, err := worker.Run(context.Background())

	// Then
	if err != nil {
		t.Fatalf("run backfill: %v", err)
	}
	checked := 0
	for _, event := range memoryClient.events() {
		if event.EventType != memory.EventTypeChannelCreated && event.EventType != memory.EventTypeThreadCreated {
			continue
		}
		checked++
		if got := event.Payload["guild_id"]; got != "guild-actual" {
			t.Fatalf("expected topology event %s to use trusted guild id, got payload %#v", event.EventType, event.Payload)
		}
		if event.Discord.GuildID != "guild-actual" {
			t.Fatalf("expected topology event %s discord context to use trusted guild id, got %#v", event.EventType, event.Discord)
		}
		if event.Scope.GuildID != "guild-actual" {
			t.Fatalf("expected topology event %s scope to use trusted guild id, got %#v", event.EventType, event.Scope)
		}
	}
	if checked != 2 {
		t.Fatalf("expected channel and thread topology events, checked %d events from %#v", checked, eventTypes(memoryClient.events()))
	}
}
