package discordevent

import (
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

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
			"emoji_animated": reaction.Emoji.Animated,
			"source_marker":  string(n.config.SourceMarker),
		},
		discordContext(reaction.GuildID, reaction.ChannelID, reaction.MessageID),
		reactionScope(reaction.GuildID, reaction.ChannelID),
	)
}
