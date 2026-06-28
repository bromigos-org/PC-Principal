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
	if channel.Type == discordgo.ChannelTypeGuildCategory {
		payload["category_id"] = channel.ID
		payload["category_name"] = channel.Name
		payload["group_type"] = "category"
		scope = guildScope(channel.GuildID)
	}
	if channel.ThreadMetadata != nil {
		payload["archived"] = channel.ThreadMetadata.Archived
		payload["auto_archive_duration"] = channel.ThreadMetadata.AutoArchiveDuration
		payload["locked"] = channel.ThreadMetadata.Locked
		payload["invitable"] = channel.ThreadMetadata.Invitable
	}
	return n.clientEvent(eventType, n.config.ObservedAt, memory.ClientEventActor{}, memory.ClientEventSubject{ID: channel.ID, Type: channelSubjectType(channel), ParentID: parentID}, payload, discordContext(channel.GuildID, channel.ID, ""), scope)
}

func (n Normalizer) normalizeUser(guildID string, user *discordgo.User) memory.ClientEvent {
	userType := "user"
	if user.Bot {
		userType = "bot"
	}
	return n.clientEvent(
		memory.EventTypeUserDiscovered,
		n.config.ObservedAt,
		actorForUser(user),
		memory.ClientEventSubject{ID: user.ID, Type: userType, ParentID: guildID},
		memory.JsonObject{
			"guild_id":      guildID,
			"user_id":       user.ID,
			"username":      user.Username,
			"display_name":  displayName(user),
			"global_name":   user.GlobalName,
			"discriminator": user.Discriminator,
			"is_bot":        user.Bot,
			"is_system":     user.System,
			"user_type":     userType,
			"source_marker": string(n.config.SourceMarker),
		},
		discordContext(guildID, "", ""),
		guildScope(guildID),
	)
}

func (n Normalizer) normalizeMemberEvents(member *discordgo.Member, before *discordgo.Member) []memory.ClientEvent {
	capacity := 1 + len(member.Roles)
	if member.User != nil {
		capacity++
	}
	events := make([]memory.ClientEvent, 0, capacity)
	events = append(events, n.normalizeMember(member))
	if member.User != nil {
		events = append(events, n.normalizeUser(member.GuildID, member.User))
	}
	events = append(events, n.normalizeRoleAssignments(member, before)...)
	return events
}

func (n Normalizer) normalizeRoleAssignments(member *discordgo.Member, before *discordgo.Member) []memory.ClientEvent {
	current := roleSet(member.Roles)
	previous := map[string]struct{}{}
	if before != nil {
		previous = roleSet(before.Roles)
	}
	events := make([]memory.ClientEvent, 0, len(member.Roles))
	for roleID := range current {
		if _, ok := previous[roleID]; !ok {
			events = append(events, n.normalizeMemberRole(memory.EventTypeMemberRoleAssigned, member, roleID))
		}
	}
	for roleID := range previous {
		if _, ok := current[roleID]; !ok {
			events = append(events, n.normalizeMemberRole(memory.EventTypeMemberRoleUnassigned, member, roleID))
		}
	}
	return events
}

func (n Normalizer) normalizeMemberRole(eventType memory.EventType, member *discordgo.Member, roleID string) memory.ClientEvent {
	actor := actorForUser(member.User)
	subjectID := hashID(string(eventType), member.GuildID, actor.ID, roleID)
	return n.clientEvent(
		eventType,
		n.config.ObservedAt,
		actor,
		memory.ClientEventSubject{ID: subjectID, Type: "member_role_assignment", ParentID: actor.ID},
		memory.JsonObject{
			"guild_id":      member.GuildID,
			"user_id":       actor.ID,
			"member_id":     actor.ID,
			"role_id":       roleID,
			"source_marker": string(n.config.SourceMarker),
		},
		discordContext(member.GuildID, "", ""),
		guildScope(member.GuildID),
	)
}

func roleSet(roles []string) map[string]struct{} {
	result := make(map[string]struct{}, len(roles))
	for _, roleID := range roles {
		result[roleID] = struct{}{}
	}
	return result
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
	userType := "user"
	if actor.IsBot {
		userType = "bot"
	}
	return n.clientEvent(
		memory.EventTypeMemberUpdated,
		n.config.ObservedAt,
		actor,
		memory.ClientEventSubject{ID: actor.ID, Type: "member", ParentID: member.GuildID},
		memory.JsonObject{
			"guild_id":  member.GuildID,
			"user_id":   actor.ID,
			"is_bot":    actor.IsBot,
			"user_type": userType,
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
