package commands

import (
	"context"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("ping", "@PC-Principal ping", "Test the bot's responsiveness.", func(s *discordgo.Session, m *discordgo.MessageCreate) {
		s.ChannelMessageSend(m.ChannelID, "Pong!")
	})
}

func BotMention(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID || m.Author.Bot {
		return
	}

	if !mentionsBot(s, m) {
		return
	}

	if !hasAllowedRole(m.Member) {
		return
	}
	d := dispatcher()
	d.Commands = mentionCommands(s, m)
	d.HandleAddressed(context.Background(), inboundFromMessage(s, m), discordDescriber{session: s, message: m}, respondTo(s, m))
}

func handleMentionConversation(s *discordgo.Session, m *discordgo.MessageCreate) error {
	return dispatcher().AddressedConversation(context.Background(), inboundFromMessage(s, m), discordDescriber{session: s, message: m}, respondTo(s, m))
}

func AmbientReply(s *discordgo.Session, m *discordgo.MessageCreate) {
	if ambientManager == nil || m.Author == nil || m.Author.ID == s.State.User.ID || m.Author.Bot {
		return
	}
	if !hasAllowedRole(m.Member) || mentionsBot(s, m) {
		return
	}
	dispatcher().HandleAmbient(context.Background(), inboundFromMessage(s, m), discordDescriber{session: s, message: m}, respondTo(s, m))
}

func mentionsBot(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			return true
		}
	}
	return false
}

func referencesBot(s *discordgo.Session, m *discordgo.MessageCreate) bool {
	return m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil && m.ReferencedMessage.Author.ID == s.State.User.ID
}

// reactPC reacts to the triggering message with the :PC: custom emoji.
// Checks application emojis first (set via the Discord developer portal),
// then falls back to guild emojis.
func reactPC(s *discordgo.Session, m *discordgo.MessageCreate) {
	if appEmojis, err := s.ApplicationEmojis(s.State.User.ID); err == nil {
		for _, e := range appEmojis {
			if strings.EqualFold(e.Name, "PC") {
				s.MessageReactionAdd(m.ChannelID, m.ID, e.APIName())
				return
			}
		}
	}
	if guildEmojis, err := s.GuildEmojis(m.GuildID); err == nil {
		for _, e := range guildEmojis {
			if strings.EqualFold(e.Name, "PC") {
				s.MessageReactionAdd(m.ChannelID, m.ID, e.APIName())
				return
			}
		}
	}
}
