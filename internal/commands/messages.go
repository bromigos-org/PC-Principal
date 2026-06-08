package commands

import (
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("ping", "@PC-Principal ping", "Test the bot's responsiveness.", func(s *discordgo.Session, m *discordgo.MessageCreate) {
		s.ChannelMessageSend(m.ChannelID, "Pong!")
	})
}

func BotMention(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
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
		return
	}

	name := strings.ToLower(parts[1])
	for _, cmd := range registry {
		if cmd.Name == name {
			cmd.Handler(s, m)
			return
		}
	}
}
