package run

import (
	"errors"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestLiveChannelLifecycleHandler_ingests_update_and_delete_topology(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	s.State.User = &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}
	category := &discordgo.Channel{ID: "category-1", GuildID: "guild-1", Name: "Projects", Type: discordgo.ChannelTypeGuildCategory}
	channel := &discordgo.Channel{ID: "channel-1", GuildID: "guild-1", Name: "renamed", ParentID: category.ID, Type: discordgo.ChannelTypeGuildText, Topic: "new topic"}
	if err := s.State.GuildAdd(&discordgo.Guild{ID: "guild-1", Channels: []*discordgo.Channel{category, channel}}); err != nil {
		t.Fatalf("seed state guild: %v", err)
	}

	// When
	liveChannelUpdateHandler(s, &discordgo.ChannelUpdate{Channel: channel, BeforeUpdate: &discordgo.Channel{ID: channel.ID, GuildID: channel.GuildID, Name: "old", ParentID: "category-old", Type: channel.Type, Topic: "old topic"}})
	liveChannelDeleteHandler(s, &discordgo.ChannelDelete{Channel: channel})

	// Then
	if len(memoryClient.events) != 2 {
		t.Fatalf("expected update and delete events, got %d", len(memoryClient.events))
	}
	if memoryClient.events[0].EventType != memory.EventTypeChannelUpdated || memoryClient.events[0].Payload["topic"] != "new topic" {
		t.Fatalf("expected channel update topic payload, got %#v", memoryClient.events[0])
	}
	if memoryClient.events[0].Payload["category_id"] != category.ID || memoryClient.events[0].Payload["previous_parent_id"] != "category-old" {
		t.Fatalf("expected channel move metadata, got %#v", memoryClient.events[0].Payload)
	}
	if memoryClient.events[1].EventType != memory.EventTypeChannelDeleted || memoryClient.events[1].Payload["deleted"] != true {
		t.Fatalf("expected channel delete tombstone, got %#v", memoryClient.events[1])
	}
}

func TestLiveThreadLifecycleHandler_ingests_archive_state(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	thread := &discordgo.Channel{ID: "thread-1", GuildID: "guild-1", Name: "thread", ParentID: "channel-1", Type: discordgo.ChannelTypeGuildPublicThread, ThreadMetadata: &discordgo.ThreadMetadata{Archived: true, AutoArchiveDuration: 1440, Locked: true}}

	// When
	liveThreadUpdateHandler(s, &discordgo.ThreadUpdate{Channel: thread, BeforeUpdate: &discordgo.Channel{ID: thread.ID, GuildID: thread.GuildID, ParentID: thread.ParentID, Type: thread.Type, ThreadMetadata: &discordgo.ThreadMetadata{Archived: false}}})

	// Then
	if len(memoryClient.events) != 1 {
		t.Fatalf("expected one thread update event, got %d", len(memoryClient.events))
	}
	if memoryClient.events[0].EventType != memory.EventTypeThreadUpdated || memoryClient.events[0].Payload["archived"] != true || memoryClient.events[0].Payload["previous_archived"] != false {
		t.Fatalf("expected archive transition payload, got %#v", memoryClient.events[0])
	}
}

func TestLiveRoleAndMemberHandlers_ingest_updates_nonfatally(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{ingestErr: errors.New("memory offline")}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	role := &discordgo.Role{ID: "role-1", Name: "Moderators", Mentionable: true, Permissions: 8}
	member := &discordgo.Member{GuildID: "guild-1", User: &discordgo.User{ID: "user-1", Username: "blackflame"}, Roles: []string{role.ID}, Nick: "Blackflame"}

	// When
	liveRoleUpdateHandler(s, &discordgo.GuildRoleUpdate{GuildRole: &discordgo.GuildRole{GuildID: "guild-1", Role: role}, BeforeUpdate: &discordgo.Role{ID: role.ID, Name: "Members"}})
	liveMemberUpdateHandler(s, &discordgo.GuildMemberUpdate{Member: member, BeforeUpdate: &discordgo.Member{GuildID: "guild-1", User: member.User, Roles: nil, Nick: "BF"}})

	// Then
	if len(memoryClient.events) != 2 {
		t.Fatalf("expected role and member ingest attempts despite errors, got %d", len(memoryClient.events))
	}
	if memoryClient.events[0].EventType != memory.EventTypeRoleUpdated || memoryClient.events[0].Payload["previous_name"] != "Members" {
		t.Fatalf("expected role update payload, got %#v", memoryClient.events[0])
	}
	if memoryClient.events[1].EventType != memory.EventTypeMemberUpdated || memoryClient.events[1].Payload["previous_nickname"] != "BF" {
		t.Fatalf("expected member update payload, got %#v", memoryClient.events[1])
	}
}

func TestIngestStartupSnapshot_enumerates_topology_under_caps(t *testing.T) {
	// Given
	memoryClient := &fakeLiveMemoryClient{}
	configureLiveMessageIngestion(memoryClient, "tenant-1")
	t.Cleanup(func() { configureLiveMessageIngestion(memory.NewClient(memory.Config{}, nil), "bromigos") })
	s, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	role := &discordgo.Role{ID: "role-1", Name: "Member"}
	member := &discordgo.Member{GuildID: "guild-1", User: &discordgo.User{ID: "user-1", Username: "blackflame"}, Roles: []string{role.ID}, JoinedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)}
	guild := &discordgo.Guild{ID: "guild-1", Channels: []*discordgo.Channel{{ID: "channel-1", GuildID: "guild-1", Name: "general", Type: discordgo.ChannelTypeGuildText}}, Threads: []*discordgo.Channel{{ID: "thread-1", GuildID: "guild-1", Name: "thread", ParentID: "channel-1", Type: discordgo.ChannelTypeGuildPublicThread}}, Roles: []*discordgo.Role{role}, Members: []*discordgo.Member{member}}
	if err := s.State.GuildAdd(guild); err != nil {
		t.Fatalf("seed state guild: %v", err)
	}

	// When
	ingestStartupSnapshot(s, &discordgo.Ready{Guilds: []*discordgo.Guild{{ID: guild.ID}}})

	// Then
	if len(memoryClient.events) != 4 {
		t.Fatalf("expected channel, thread, role, and member snapshot events, got %d: %#v", len(memoryClient.events), memoryClient.events)
	}
	if memoryClient.events[0].Payload["source_marker"] != "backfill" {
		t.Fatalf("expected snapshot backfill marker, got %#v", memoryClient.events[0].Payload)
	}
}
