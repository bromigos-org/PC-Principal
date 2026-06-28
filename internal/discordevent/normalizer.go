package discordevent

import (
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

type SourceMarker string

const (
	SourceMarkerLive     SourceMarker = "live"
	SourceMarkerBackfill SourceMarker = "backfill"
)

type Snapshot struct {
	Channels map[string]*discordgo.Channel
}

type AttachmentCopyConfig struct {
	Enabled              bool
	Bucket               string
	MaxSizeBytes         int
	ContentTypeAllowlist []string
}

type Config struct {
	TenantID       string
	AgentID        string
	SourceMarker   SourceMarker
	ObservedAt     time.Time
	Snapshot       Snapshot
	AttachmentCopy AttachmentCopyConfig
}

type Normalizer struct {
	config Config
}

func New(config Config) Normalizer {
	if config.AttachmentCopy.Bucket == "" {
		config.AttachmentCopy.Bucket = "pc-principal-discord-attachments"
	}
	if config.AttachmentCopy.MaxSizeBytes == 0 {
		config.AttachmentCopy.MaxSizeBytes = 25_000_000
	}
	if len(config.AttachmentCopy.ContentTypeAllowlist) == 0 {
		config.AttachmentCopy.ContentTypeAllowlist = []string{"image/png", "image/jpeg", "image/gif"}
	}
	return Normalizer{config: config}
}

func (n Normalizer) NormalizeMessageCreate(message *discordgo.Message) []memory.ClientEvent {
	scope := scopeForMessage(n.config.TenantID, n.config.AgentID, message)
	occurredAt := timestampOrObserved(message.Timestamp, n.config.ObservedAt)
	content := sanitizeMessageContent(message.Content)
	basePayload := memory.JsonObject{
		"message_id":       message.ID,
		"channel_id":       message.ChannelID,
		"guild_id":         message.GuildID,
		"content":          content,
		"source_marker":    string(n.config.SourceMarker),
		"attachment_count": len(message.Attachments),
	}
	events := []memory.ClientEvent{n.clientEvent(
		memory.EventTypeMessageCreated,
		occurredAt,
		memory.ClientEventActor{ID: userID(message.Author), DisplayName: displayName(message.Author), IsBot: message.Author != nil && message.Author.Bot},
		memory.ClientEventSubject{ID: message.ID, Type: "message", ParentID: message.ChannelID},
		basePayload,
		discordContext(message.GuildID, message.ChannelID, message.ID),
		scope,
	)}
	for _, attachment := range message.Attachments {
		events = append(events, n.normalizeAttachment(message, attachment, scope, occurredAt))
	}
	for _, link := range extractLinks(content) {
		events = append(events, n.normalizeLink(message, link, scope, occurredAt))
	}
	if message.Author != nil && n.config.SourceMarker == SourceMarkerLive {
		events = append(events, n.normalizeUser(message.GuildID, message.Author))
	}
	return events
}

func (n Normalizer) NormalizeChannelCreate(channel *discordgo.Channel) []memory.ClientEvent {
	return []memory.ClientEvent{n.normalizeChannel(channel, memory.EventTypeChannelCreated, channelScope(channel), n.categoryID(channel), channel.ParentID)}
}

func (n Normalizer) NormalizeChannelUpdate(channel *discordgo.Channel, before *discordgo.Channel) []memory.ClientEvent {
	event := n.normalizeChannel(channel, memory.EventTypeChannelUpdated, channelScope(channel), n.categoryID(channel), channel.ParentID)
	applyPreviousChannelPayload(event.Payload, before)
	return []memory.ClientEvent{event}
}

func (n Normalizer) NormalizeChannelDelete(channel *discordgo.Channel) []memory.ClientEvent {
	event := n.normalizeChannel(channel, memory.EventTypeChannelDeleted, channelScope(channel), n.categoryID(channel), channel.ParentID)
	event.Payload["deleted"] = true
	return []memory.ClientEvent{event}
}

func (n Normalizer) NormalizeThreadCreate(channel *discordgo.Channel) []memory.ClientEvent {
	return []memory.ClientEvent{n.normalizeChannel(channel, memory.EventTypeThreadCreated, guildScope(channel.GuildID), n.categoryIDByParent(channel.ParentID), channel.ParentID)}
}

func (n Normalizer) NormalizeThreadUpdate(channel *discordgo.Channel, before *discordgo.Channel) []memory.ClientEvent {
	event := n.normalizeChannel(channel, memory.EventTypeThreadUpdated, guildScope(channel.GuildID), n.categoryIDByParent(channel.ParentID), channel.ParentID)
	applyPreviousChannelPayload(event.Payload, before)
	return []memory.ClientEvent{event}
}

func (n Normalizer) NormalizeThreadDelete(channel *discordgo.Channel) []memory.ClientEvent {
	event := n.normalizeChannel(channel, memory.EventTypeThreadDeleted, guildScope(channel.GuildID), n.categoryIDByParent(channel.ParentID), channel.ParentID)
	event.Payload["deleted"] = true
	return []memory.ClientEvent{event}
}

func (n Normalizer) NormalizeReactionAdd(reaction *discordgo.MessageReaction, member *discordgo.Member) []memory.ClientEvent {
	return []memory.ClientEvent{n.normalizeReaction(memory.EventTypeReactionAdded, reaction, member)}
}

func (n Normalizer) NormalizeReactionRemove(reaction *discordgo.MessageReaction) []memory.ClientEvent {
	return []memory.ClientEvent{n.normalizeReaction(memory.EventTypeReactionRemoved, reaction, nil)}
}

func (n Normalizer) NormalizeRoleCreate(guildID string, role *discordgo.Role) []memory.ClientEvent {
	return []memory.ClientEvent{n.normalizeRole(memory.EventTypeRoleCreated, guildID, role)}
}

func (n Normalizer) NormalizeRoleUpdate(guildID string, role *discordgo.Role, before *discordgo.Role) []memory.ClientEvent {
	event := n.normalizeRole(memory.EventTypeRoleUpdated, guildID, role)
	applyPreviousRolePayload(event.Payload, before)
	return []memory.ClientEvent{event}
}

func (n Normalizer) NormalizeRoleDelete(guildID string, roleID string, before *discordgo.Role) []memory.ClientEvent {
	role := before
	if role == nil {
		role = &discordgo.Role{ID: roleID}
	}
	event := n.normalizeRole(memory.EventTypeRoleDeleted, guildID, role)
	event.Payload["deleted"] = true
	return []memory.ClientEvent{event}
}

func (n Normalizer) normalizeRole(eventType memory.EventType, guildID string, role *discordgo.Role) memory.ClientEvent {
	return n.clientEvent(
		eventType,
		n.config.ObservedAt,
		memory.ClientEventActor{},
		memory.ClientEventSubject{ID: role.ID, Type: "role", ParentID: guildID},
		memory.JsonObject{
			"guild_id":    guildID,
			"role_id":     role.ID,
			"name":        role.Name,
			"mentionable": role.Mentionable,
			"managed":     role.Managed,
			"hoist":       role.Hoist,
			"position":    role.Position,
			"permissions": role.Permissions,
		},
		memory.DiscordEventContext{GuildID: guildID},
		guildScope(guildID),
	)
}

func (n Normalizer) NormalizeMemberUpdate(member *discordgo.Member) []memory.ClientEvent {
	return n.normalizeMemberEvents(member, nil)
}

func (n Normalizer) NormalizeMemberUpdateWithPrevious(member *discordgo.Member, before *discordgo.Member) []memory.ClientEvent {
	events := n.normalizeMemberEvents(member, before)
	if len(events) > 0 {
		applyPreviousMemberPayload(events[0].Payload, before)
	}
	return events
}
