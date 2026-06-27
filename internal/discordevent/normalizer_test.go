package discordevent

import (
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func TestNormalizeMessageCreateStableEvent(t *testing.T) {
	// Given
	message := &discordgo.Message{
		ID:        "message-1",
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Content:   "Howdy https://example.com/docs?token=secret",
		Timestamp: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC),
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
		Attachments: []*discordgo.MessageAttachment{{
			ID:          "attachment-1",
			URL:         "https://cdn.example.com/a.png",
			ProxyURL:    "https://media.example.com/a.png",
			Filename:    "a.png",
			ContentType: "image/png",
			Size:        42,
			Width:       800,
			Height:      600,
		}},
	}
	messageBackfill := *message
	live := New(Config{TenantID: "bromigos", AgentID: "pc-principal", SourceMarker: SourceMarkerLive, ObservedAt: time.Date(2026, 6, 27, 0, 0, 1, 0, time.UTC)})
	backfill := New(Config{TenantID: "bromigos", AgentID: "pc-principal", SourceMarker: SourceMarkerBackfill, ObservedAt: time.Date(2026, 6, 27, 0, 1, 0, 0, time.UTC)})

	// When
	liveEvents := live.NormalizeMessageCreate(&messageBackfill)
	backfillEvents := backfill.NormalizeMessageCreate(message)

	// Then
	if len(liveEvents) != 3 || len(backfillEvents) != 3 {
		t.Fatalf("expected message, attachment, and link events, got live=%d backfill=%d", len(liveEvents), len(backfillEvents))
	}
	if liveEvents[0].EventID != backfillEvents[0].EventID || liveEvents[0].IdempotencyKey != backfillEvents[0].IdempotencyKey {
		t.Fatalf("expected stable message identity, got live=%#v backfill=%#v", liveEvents[0], backfillEvents[0])
	}
	if liveEvents[0].Payload["source_marker"] != string(SourceMarkerLive) || backfillEvents[0].Payload["source_marker"] != string(SourceMarkerBackfill) {
		t.Fatalf("expected source markers in payload, got live=%#v backfill=%#v", liveEvents[0].Payload, backfillEvents[0].Payload)
	}
	if got := liveEvents[1].Payload["filename"]; got != "a.png" {
		t.Fatalf("expected attachment metadata, got %#v", liveEvents[1].Payload)
	}
	if liveEvents[1].Payload["content_type"] != "image/png" || liveEvents[1].Payload["size"] != 42 || liveEvents[1].Payload["width"] != 800 || liveEvents[1].Payload["height"] != 600 {
		t.Fatalf("expected attachment size and type metadata, got %#v", liveEvents[1].Payload)
	}
	if liveEvents[1].Payload["spoiler"] != false || liveEvents[1].Payload["url"] != "https://cdn.example.com/a.png" || liveEvents[1].Payload["proxy_url"] != "https://media.example.com/a.png" {
		t.Fatalf("expected attachment URLs and spoiler metadata, got %#v", liveEvents[1].Payload)
	}
	if liveEvents[1].Payload["message_id"] != "message-1" || liveEvents[1].Payload["channel_id"] != "channel-1" || liveEvents[1].Payload["guild_id"] != "guild-1" {
		t.Fatalf("expected attachment relation metadata, got %#v", liveEvents[1].Payload)
	}
	if got := liveEvents[2].Payload["url"]; got != "https://example.com/docs" {
		t.Fatalf("expected sanitized link metadata, got %#v", liveEvents[2].Payload)
	}
	if liveEvents[2].Payload["message_id"] != "message-1" || liveEvents[2].Payload["channel_id"] != "channel-1" || liveEvents[2].Payload["guild_id"] != "guild-1" {
		t.Fatalf("expected link relation metadata, got %#v", liveEvents[2].Payload)
	}
	if liveEvents[0].Scope.Visibility != memory.VisibilityChannel || liveEvents[0].Scope.GuildID != "guild-1" {
		t.Fatalf("expected guild message scope, got %#v", liveEvents[0].Scope)
	}
}

func TestNewAttachmentCopyConfigDefaultsDisabled(t *testing.T) {
	// Given
	normalizer := New(Config{})

	// When
	copyConfig := normalizer.config.AttachmentCopy

	// Then
	if copyConfig.Enabled {
		t.Fatalf("expected attachment copy to be disabled by default, got %#v", copyConfig)
	}
	if copyConfig.Bucket != "pc-principal-discord-attachments" {
		t.Fatalf("expected bucket placeholder, got %#v", copyConfig)
	}
	if copyConfig.MaxSizeBytes != 25_000_000 {
		t.Fatalf("expected max size placeholder, got %#v", copyConfig)
	}
	if len(copyConfig.ContentTypeAllowlist) != 3 || copyConfig.ContentTypeAllowlist[0] != "image/png" || copyConfig.ContentTypeAllowlist[1] != "image/jpeg" || copyConfig.ContentTypeAllowlist[2] != "image/gif" {
		t.Fatalf("expected content-type allowlist placeholder, got %#v", copyConfig)
	}
	if copyConfig.allows(&discordgo.MessageAttachment{ContentType: "image/png", Size: 128, Filename: "a.png"}) {
		t.Fatal("expected disabled copy policy to reject attachments")
	}
}

func TestNormalizeMessageCreateLinkSanitizationStripsQueryAndFragment(t *testing.T) {
	// Given
	normalizer := New(Config{TenantID: "bromigos", AgentID: "pc-principal", SourceMarker: SourceMarkerLive, ObservedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)})
	message := &discordgo.Message{ID: "message-2", ChannelID: "channel-1", GuildID: "guild-1", Content: "visit https://example.com/path?token=secret#fragment", Timestamp: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), Author: &discordgo.User{ID: "user-1", Username: "blackflame"}}

	// When
	events := normalizer.NormalizeMessageCreate(message)

	// Then
	if len(events) != 2 {
		t.Fatalf("expected message and link events, got %d", len(events))
	}
	if events[1].Payload["url"] != "https://example.com/path" {
		t.Fatalf("expected sanitized link metadata, got %#v", events[1].Payload)
	}
}

func TestNormalizeMessageDoesNotLeakAcrossScope(t *testing.T) {
	// Given
	normalizer := New(Config{TenantID: "bromigos", AgentID: "pc-principal", SourceMarker: SourceMarkerLive, ObservedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)})
	dmMessage := &discordgo.Message{ID: "message-dm", ChannelID: "dm-channel-1", Content: "hello", Timestamp: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), Author: &discordgo.User{ID: "user-1", Username: "blackflame"}}
	guildMessage := &discordgo.Message{ID: "message-guild", ChannelID: "channel-1", GuildID: "guild-1", Content: "hello", Timestamp: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), Author: &discordgo.User{ID: "user-1", Username: "blackflame"}}

	// When
	dmEvent := normalizer.NormalizeMessageCreate(dmMessage)[0]
	guildEvent := normalizer.NormalizeMessageCreate(guildMessage)[0]

	// Then
	if dmEvent.Scope.Visibility != memory.VisibilityPrivateUser || dmEvent.Scope.GuildID != "" {
		t.Fatalf("expected DM privacy scope, got %#v", dmEvent.Scope)
	}
	if guildEvent.Scope.Visibility != memory.VisibilityChannel || guildEvent.Scope.GuildID != "guild-1" {
		t.Fatalf("expected guild channel scope, got %#v", guildEvent.Scope)
	}
	if dmEvent.Discord.GuildID != "" || dmEvent.Payload["guild_id"] != "" {
		t.Fatalf("expected DM payload to stay unguilded, got %#v", dmEvent)
	}
}

func TestNormalizeChannelAndThreadParentCategoryMapping(t *testing.T) {
	// Given
	category := &discordgo.Channel{ID: "category-1", GuildID: "guild-1", Name: "Projects", Type: discordgo.ChannelTypeGuildCategory}
	parent := &discordgo.Channel{ID: "channel-1", GuildID: "guild-1", Name: "general", ParentID: "category-1", Type: discordgo.ChannelTypeGuildText, Topic: "project chat"}
	thread := &discordgo.Channel{ID: "thread-1", GuildID: "guild-1", Name: "thread", ParentID: "channel-1", Type: discordgo.ChannelTypeGuildPublicThread, ThreadMetadata: &discordgo.ThreadMetadata{Archived: true, AutoArchiveDuration: 1440, Locked: true, Invitable: false}}
	normalizer := New(Config{TenantID: "bromigos", AgentID: "pc-principal", SourceMarker: SourceMarkerLive, ObservedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC), Snapshot: Snapshot{Channels: map[string]*discordgo.Channel{category.ID: category, parent.ID: parent}}})

	// When
	channelEvent := normalizer.NormalizeChannelCreate(parent)[0]
	threadEvent := normalizer.NormalizeThreadCreate(thread)[0]

	// Then
	if channelEvent.Payload["parent_id"] != "category-1" || channelEvent.Payload["category_id"] != "category-1" {
		t.Fatalf("expected category mapping for channel, got %#v", channelEvent.Payload)
	}
	if threadEvent.Payload["parent_id"] != "channel-1" || threadEvent.Payload["category_id"] != "category-1" {
		t.Fatalf("expected parent and category mapping for thread, got %#v", threadEvent.Payload)
	}
	if threadEvent.Payload["archived"] != true || threadEvent.Payload["locked"] != true {
		t.Fatalf("expected thread lifecycle metadata, got %#v", threadEvent.Payload)
	}
}

func TestNormalizeReactionAddAndRemoveIncludeEmojiMetadata(t *testing.T) {
	// Given
	normalizer := New(Config{TenantID: "bromigos", AgentID: "pc-principal", SourceMarker: SourceMarkerLive, ObservedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)})
	reaction := &discordgo.MessageReaction{UserID: "user-1", MessageID: "message-1", ChannelID: "channel-1", GuildID: "guild-1", Emoji: discordgo.Emoji{ID: "emoji-1", Name: "party", Animated: true}}
	member := &discordgo.Member{User: &discordgo.User{ID: "user-1", Username: "blackflame"}}

	// When
	added := normalizer.NormalizeReactionAdd(reaction, member)[0]
	removed := normalizer.NormalizeReactionRemove(reaction)[0]

	// Then
	if added.EventType != memory.EventTypeReactionAdded || removed.EventType != memory.EventTypeReactionRemoved {
		t.Fatalf("expected reaction event types, got added=%s removed=%s", added.EventType, removed.EventType)
	}
	if added.Payload["emoji_name"] != "party" || added.Payload["emoji_id"] != "emoji-1" || added.Payload["emoji_animated"] != true {
		t.Fatalf("expected emoji metadata on add, got %#v", added.Payload)
	}
	if added.Subject.ID != removed.Subject.ID {
		t.Fatalf("expected add/remove reaction identity to match, got add=%#v remove=%#v", added.Subject, removed.Subject)
	}
}

func TestNormalizeRoleAndMemberEventsCaptureGuildTopology(t *testing.T) {
	// Given
	normalizer := New(Config{TenantID: "bromigos", AgentID: "pc-principal", SourceMarker: SourceMarkerLive, ObservedAt: time.Date(2026, 6, 27, 0, 0, 0, 0, time.UTC)})
	role := &discordgo.Role{ID: "role-1", Name: "Admin", Mentionable: true, Managed: false, Hoist: true, Position: 4, Permissions: 8}
	member := &discordgo.Member{GuildID: "guild-1", User: &discordgo.User{ID: "user-1", Username: "blackflame"}, Roles: []string{"role-1"}, Nick: "Blackflame", Pending: true}

	// When
	roleEvent := normalizer.NormalizeRoleCreate("guild-1", role)[0]
	memberEvent := normalizer.NormalizeMemberUpdate(member)[0]

	// Then
	if roleEvent.Scope.Visibility != memory.VisibilityGuild || memberEvent.Scope.Visibility != memory.VisibilityGuild {
		t.Fatalf("expected guild visibility for topology events, got role=%#v member=%#v", roleEvent.Scope, memberEvent.Scope)
	}
	if roleEvent.Payload["name"] != "Admin" || roleEvent.Payload["mentionable"] != true {
		t.Fatalf("expected role metadata, got %#v", roleEvent.Payload)
	}
	if memberEvent.Payload["user_id"] != "user-1" || memberEvent.Payload["nickname"] != "Blackflame" {
		t.Fatalf("expected member metadata, got %#v", memberEvent.Payload)
	}
}
