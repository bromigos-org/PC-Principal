package commands

import (
	"fmt"
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

	if len(m.Mentions) == 0 || m.Mentions[0].ID != s.State.User.ID {
		return
	}

	if !hasAllowedRole(m.Member) {
		return
	}

	parts := strings.Fields(m.Content)
	if len(parts) < 2 {
		if err := handleMentionConversation(s, m); err != nil {
			fmt.Printf("mention: conversation error: %v\n", err)
			s.ChannelMessageSend(m.ChannelID, "Bro, LiteLLM is not cooperating right now. Totally unacceptable.")
		}
		return
	}

	name := strings.ToLower(parts[1])
	for _, cmd := range registry {
		if cmd.Name == name {
			reactPC(s, m)
			cmd.Handler(s, m)
			return
		}
	}

	if err := handleMentionConversation(s, m); err != nil {
		fmt.Printf("mention: conversation error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "Bro, LiteLLM is not cooperating right now. Totally unacceptable.")
	}
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
