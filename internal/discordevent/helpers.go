package discordevent

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func (n Normalizer) normalizeAttachment(message *discordgo.Message, attachment *discordgo.MessageAttachment, scope memory.Scope, occurredAt time.Time) memory.ClientEvent {
	return n.clientEvent(
		memory.EventTypeAttachmentDiscovered,
		occurredAt,
		actorForUser(message.Author),
		memory.ClientEventSubject{ID: attachment.ID, Type: "attachment", ParentID: message.ID},
		memory.JsonObject{
			"message_id":    message.ID,
			"channel_id":    message.ChannelID,
			"guild_id":      message.GuildID,
			"attachment_id": attachment.ID,
			"filename":      attachment.Filename,
			"content_type":  attachment.ContentType,
			"size":          attachment.Size,
			"width":         attachment.Width,
			"height":        attachment.Height,
			"url":           sanitizeURL(attachment.URL),
			"proxy_url":     sanitizeURL(attachment.ProxyURL),
			"source_marker": string(n.config.SourceMarker),
		},
		discordContext(message.GuildID, message.ChannelID, message.ID),
		scope,
	)
}

func (n Normalizer) normalizeLink(message *discordgo.Message, link string, scope memory.Scope, occurredAt time.Time) memory.ClientEvent {
	return n.clientEvent(
		memory.EventTypeLinkDiscovered,
		occurredAt,
		actorForUser(message.Author),
		memory.ClientEventSubject{ID: hashID("link", message.ID, link), Type: "link", ParentID: message.ID},
		memory.JsonObject{
			"message_id":    message.ID,
			"channel_id":    message.ChannelID,
			"guild_id":      message.GuildID,
			"url":           link,
			"source_marker": string(n.config.SourceMarker),
		},
		discordContext(message.GuildID, message.ChannelID, message.ID),
		scope,
	)
}

func (n Normalizer) normalizeReaction(eventType memory.EventType, reaction *discordgo.MessageReaction, member *discordgo.Member) memory.ClientEvent {
	actor := actorForReaction(reaction, member)
	return n.clientEvent(
		eventType,
		n.config.ObservedAt,
		actor,
		memory.ClientEventSubject{ID: reactionIdentity(reaction), Type: "reaction", ParentID: reaction.MessageID},
		memory.JsonObject{
			"message_id":     reaction.MessageID,
			"channel_id":     reaction.ChannelID,
			"guild_id":       reaction.GuildID,
			"user_id":        reaction.UserID,
			"emoji_id":       reaction.Emoji.ID,
			"emoji_name":     reaction.Emoji.Name,
			"emoji_animated":  reaction.Emoji.Animated,
			"source_marker":   string(n.config.SourceMarker),
		},
		discordContext(reaction.GuildID, reaction.ChannelID, reaction.MessageID),
		reactionScope(reaction.GuildID, reaction.ChannelID),
	)
}

func (n Normalizer) normalizeChannel(channel *discordgo.Channel, eventType memory.EventType, scope memory.Scope, categoryID string, parentID string) memory.ClientEvent {
	payload := memory.JsonObject{
		"channel_id":    channel.ID,
		"guild_id":      channel.GuildID,
		"name":          channel.Name,
		"topic":         channel.Topic,
		"parent_id":     parentID,
		"category_id":   categoryID,
		"channel_type":  channel.Type,
		"source_marker": string(n.config.SourceMarker),
	}
	if channel.ThreadMetadata != nil {
		payload["archived"] = channel.ThreadMetadata.Archived
		payload["auto_archive_duration"] = channel.ThreadMetadata.AutoArchiveDuration
		payload["locked"] = channel.ThreadMetadata.Locked
		payload["invitable"] = channel.ThreadMetadata.Invitable
	}
	return n.clientEvent(eventType, n.config.ObservedAt, memory.ClientEventActor{}, memory.ClientEventSubject{ID: channel.ID, Type: channelSubjectType(channel), ParentID: parentID}, payload, discordContext(channel.GuildID, channel.ID, ""), scope)
}

func (n Normalizer) normalizeMember(member *discordgo.Member) memory.ClientEvent {
	actor := actorForUser(member.User)
	return n.clientEvent(
		memory.EventTypeMemberUpdated,
		n.config.ObservedAt,
		actor,
		memory.ClientEventSubject{ID: actor.ID, Type: "member", ParentID: member.GuildID},
		memory.JsonObject{
			"guild_id":  member.GuildID,
			"user_id":   actor.ID,
			"nickname":  member.Nick,
			"roles":     append([]string(nil), member.Roles...),
			"pending":   member.Pending,
			"joined_at": member.JoinedAt,
		},
		discordContext(member.GuildID, "", ""),
		guildScope(member.GuildID),
	)
}

func (n Normalizer) clientEvent(eventType memory.EventType, occurredAt time.Time, actor memory.ClientEventActor, subject memory.ClientEventSubject, payload memory.JsonObject, discord memory.DiscordEventContext, scope memory.Scope) memory.ClientEvent {
	return memory.ClientEvent{
		TenantID:       n.config.TenantID,
		SourceClient:   memory.SourceClientDiscord,
		AgentID:        n.config.AgentID,
		EventID:        eventID(eventType, subject.ID),
		EventType:      eventType,
		OccurredAt:     occurredAt.UTC().Format(time.RFC3339Nano),
		ObservedAt:     n.config.ObservedAt.UTC().Format(time.RFC3339Nano),
		IdempotencyKey: idempotencyKey(eventType, subject.ID),
		Scope:          scope,
		Actor:          actor,
		Subject:        subject,
		Payload:        payload,
		Discord:        discord,
	}
}

func eventID(eventType memory.EventType, subjectID string) string {
	return string(eventType) + ":" + subjectID
}

func idempotencyKey(eventType memory.EventType, subjectID string) string {
	return eventID(eventType, subjectID)
}

func reactionIdentity(reaction *discordgo.MessageReaction) string {
	return hashID("reaction", reaction.MessageID, reaction.UserID, reaction.Emoji.APIName())
}

func hashID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(h[:])
}

func timestampOrObserved(timestamp time.Time, observedAt time.Time) time.Time {
	if !timestamp.IsZero() {
		return timestamp
	}
	return observedAt
}

func scopeForMessage(tenantID string, agentID string, message *discordgo.Message) memory.Scope {
	if message.GuildID == "" {
		return memory.Scope{TenantID: tenantID, SpaceID: "dm", AgentID: agentID, SessionID: "dm:" + message.ChannelID, UserID: userID(message.Author), Visibility: memory.VisibilityPrivateUser, ChannelID: message.ChannelID}
	}
	return memory.Scope{TenantID: tenantID, SpaceID: message.GuildID, AgentID: agentID, SessionID: "guild:" + message.GuildID + ":channel:" + message.ChannelID, UserID: userID(message.Author), Visibility: memory.VisibilityChannel, GuildID: message.GuildID, ChannelID: message.ChannelID}
}

func guildScope(guildID string) memory.Scope {
	return memory.Scope{Visibility: memory.VisibilityGuild, SpaceID: guildID, GuildID: guildID}
}

func channelScope(channel *discordgo.Channel) memory.Scope {
	if channel.GuildID == "" {
		return memory.Scope{Visibility: memory.VisibilityPrivateUser, SpaceID: "dm", ChannelID: channel.ID}
	}
	return memory.Scope{Visibility: memory.VisibilityChannel, SpaceID: channel.GuildID, GuildID: channel.GuildID, ChannelID: channel.ID}
}

func reactionScope(guildID string, channelID string) memory.Scope {
	if guildID == "" {
		return memory.Scope{Visibility: memory.VisibilityPrivateUser, SpaceID: "dm", ChannelID: channelID}
	}
	return memory.Scope{Visibility: memory.VisibilityChannel, SpaceID: guildID, GuildID: guildID, ChannelID: channelID}
}

func discordContext(guildID string, channelID string, messageID string) memory.DiscordEventContext {
	return memory.DiscordEventContext{GuildID: guildID, ChannelID: channelID, MessageID: messageID}
}

func userID(user *discordgo.User) string {
	if user == nil {
		return ""
	}
	return user.ID
}

func actorForUser(user *discordgo.User) memory.ClientEventActor {
	return memory.ClientEventActor{ID: userID(user), DisplayName: displayName(user), IsBot: user != nil && user.Bot}
}

func actorForReaction(reaction *discordgo.MessageReaction, member *discordgo.Member) memory.ClientEventActor {
	if member != nil && member.User != nil {
		return memory.ClientEventActor{ID: reaction.UserID, DisplayName: displayName(member.User), IsBot: member.User.Bot}
	}
	return memory.ClientEventActor{ID: reaction.UserID, DisplayName: "", IsBot: false}
}

func displayName(user *discordgo.User) string {
	if user == nil {
		return ""
	}
	if user.GlobalName != "" {
		return user.GlobalName
	}
	return user.Username
}

func channelSubjectType(channel *discordgo.Channel) string {
	if channel.IsThread() {
		return "thread"
	}
	if channel.Type == discordgo.ChannelTypeGuildCategory {
		return "category"
	}
	return "channel"
}

func (n Normalizer) categoryID(channel *discordgo.Channel) string {
	return n.categoryIDByParent(channel.ParentID)
}

func (n Normalizer) categoryIDByParent(parentID string) string {
	if parentID == "" || n.config.Snapshot.Channels == nil {
		return ""
	}
	parent := n.config.Snapshot.Channels[parentID]
	if parent == nil {
		return ""
	}
	if parent.Type == discordgo.ChannelTypeGuildCategory {
		return parent.ID
	}
	if parent.ParentID == "" {
		return ""
	}
	return n.categoryIDByParent(parent.ParentID)
}
