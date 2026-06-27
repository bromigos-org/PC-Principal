package discordevent

import (
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
			"spoiler":       attachmentSpoiler(attachment),
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

func attachmentSpoiler(attachment *discordgo.MessageAttachment) bool {
	return strings.HasPrefix(attachment.Filename, "SPOILER_")
}

func (c AttachmentCopyConfig) allows(attachment *discordgo.MessageAttachment) bool {
	if !c.Enabled {
		return false
	}
	if attachment.Size > c.MaxSizeBytes {
		return false
	}
	for _, contentType := range c.ContentTypeAllowlist {
		if contentType == attachment.ContentType {
			return true
		}
	}
	return false
}
