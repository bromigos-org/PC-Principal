package run

import (
	"errors"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestLiveReactionAddHandler_Ingests_reaction_add(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s := runSession(t)
	reaction := &discordgo.MessageReactionAdd{MessageReaction: &discordgo.MessageReaction{
		UserID:    "user-1",
		MessageID: "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Emoji:     discordgo.Emoji{Name: "bromigo"},
	}}

	// When
	liveReactionAddHandler(s, reaction)

	// Then
	if len(memoryClient.events) != 1 {
		t.Fatalf("expected one reaction add event, got %d", len(memoryClient.events))
	}
	if memoryClient.events[0].EventType != memory.EventTypeReactionAdded || memoryClient.events[0].TenantID != "tenant-1" {
		t.Fatalf("expected normalized live reaction add event, got %#v", memoryClient.events[0])
	}
}

func TestLiveReactionAddHandler_Continues_when_ingest_fails(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{ingestErr: errors.New("memory offline")}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s := runSession(t)
	reaction := &discordgo.MessageReactionAdd{MessageReaction: &discordgo.MessageReaction{
		UserID:    "user-1",
		MessageID: "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Emoji:     discordgo.Emoji{Name: "bromigo"},
	}}

	// When
	liveReactionAddHandler(s, reaction)

	// Then
	if len(memoryClient.events) != 1 {
		t.Fatalf("expected failed ingest attempt to record one reaction event, got %d", len(memoryClient.events))
	}
}

func TestLiveReactionRemoveHandler_Ingests_reaction_remove(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s := runSession(t)
	reaction := &discordgo.MessageReactionRemove{MessageReaction: &discordgo.MessageReaction{
		UserID:    "user-1",
		MessageID: "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Emoji:     discordgo.Emoji{Name: "party"},
	}}

	// When
	liveReactionRemoveHandler(s, reaction)

	// Then
	if len(memoryClient.events) != 1 {
		t.Fatalf("expected one reaction remove event, got %d", len(memoryClient.events))
	}
	if memoryClient.events[0].EventType != memory.EventTypeReactionRemoved || memoryClient.events[0].Payload["emoji_name"] != "party" {
		t.Fatalf("expected normalized live reaction remove event, got %#v", memoryClient.events[0])
	}
}
