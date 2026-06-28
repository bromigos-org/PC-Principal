package discordevent

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

func (n Normalizer) clientEvent(eventType memory.EventType, occurredAt time.Time, actor memory.ClientEventActor, subject memory.ClientEventSubject, payload memory.JsonObject, discord memory.DiscordEventContext, scope memory.Scope) memory.ClientEvent {
	actor = completeActor(actor)
	scope = n.completeScope(scope, actor, discord)
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

func completeActor(actor memory.ClientEventActor) memory.ClientEventActor {
	if actor.ID != "" {
		return actor
	}
	return memory.ClientEventActor{ID: "discord", DisplayName: "Discord", IsBot: true}
}

func (n Normalizer) completeScope(scope memory.Scope, actor memory.ClientEventActor, discord memory.DiscordEventContext) memory.Scope {
	if scope.TenantID == "" {
		scope.TenantID = n.config.TenantID
	}
	if scope.AgentID == "" {
		scope.AgentID = n.config.AgentID
	}
	if scope.UserID == "" {
		scope.UserID = actor.ID
	}
	if scope.SessionID == "" {
		scope.SessionID = sessionID(scope, discord)
	}
	return scope
}

func sessionID(scope memory.Scope, discord memory.DiscordEventContext) string {
	if scope.ChannelID != "" {
		if scope.GuildID == "" {
			return "dm:" + scope.ChannelID
		}
		return "guild:" + scope.GuildID + ":channel:" + scope.ChannelID
	}
	if discord.ChannelID != "" {
		if discord.GuildID == "" {
			return "dm:" + discord.ChannelID
		}
		return "guild:" + discord.GuildID + ":channel:" + discord.ChannelID
	}
	if scope.GuildID != "" {
		return "guild:" + scope.GuildID
	}
	if discord.GuildID != "" {
		return "guild:" + discord.GuildID
	}
	return "agent:" + scope.AgentID
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
