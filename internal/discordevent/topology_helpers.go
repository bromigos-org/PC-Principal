package discordevent

import (
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

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

func applyPreviousChannelPayload(payload memory.JsonObject, before *discordgo.Channel) {
	if before == nil {
		return
	}
	payload["previous_name"] = before.Name
	payload["previous_topic"] = before.Topic
	payload["previous_parent_id"] = before.ParentID
	if before.ThreadMetadata != nil {
		payload["previous_archived"] = before.ThreadMetadata.Archived
		payload["previous_locked"] = before.ThreadMetadata.Locked
		payload["previous_invitable"] = before.ThreadMetadata.Invitable
	}
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

func applyPreviousRolePayload(payload memory.JsonObject, before *discordgo.Role) {
	if before == nil {
		return
	}
	payload["previous_name"] = before.Name
	payload["previous_mentionable"] = before.Mentionable
	payload["previous_managed"] = before.Managed
	payload["previous_hoist"] = before.Hoist
	payload["previous_position"] = before.Position
	payload["previous_permissions"] = before.Permissions
}

func applyPreviousMemberPayload(payload memory.JsonObject, before *discordgo.Member) {
	if before == nil {
		return
	}
	payload["previous_nickname"] = before.Nick
	payload["previous_roles"] = append([]string(nil), before.Roles...)
	payload["previous_pending"] = before.Pending
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
