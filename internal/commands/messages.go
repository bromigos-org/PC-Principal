package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/ambient"
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
	if ambientManager != nil {
		decision, err := ambientManager.Decide(context.Background(), ambientMessage(s, m, true))
		if err != nil {
			fmt.Printf("ambient: state error: %v\n", err)
		}
		if decision.Stop {
			return
		}
	}

	parts := strings.Fields(m.Content)
	if len(parts) < 2 {
		reactPC(s, m)
		if err := handleMentionConversation(s, m); err != nil {
			fmt.Printf("mention: conversation error: %v\n", err)
			s.ChannelMessageSend(m.ChannelID, "Bro, LiteLLM is not cooperating right now. Totally unacceptable.")
		}
		return
	}

	name := strings.ToLower(parts[1])
	for _, cmd := range registry {
		if cmd.Name == name && name != "hey" {
			reactPC(s, m)
			cmd.Handler(s, m)
			return
		}
	}

	reactPC(s, m)
	if err := handleMentionConversation(s, m); err != nil {
		fmt.Printf("mention: conversation error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "Bro, LiteLLM is not cooperating right now. Totally unacceptable.")
	}
}

func AmbientReply(s *discordgo.Session, m *discordgo.MessageCreate) {
	if ambientManager == nil || m.Author == nil || m.Author.ID == s.State.User.ID || m.Author.Bot {
		return
	}
	if !hasAllowedRole(m.Member) || mentionsBot(s, m) {
		return
	}
	decision, err := ambientManager.Decide(context.Background(), ambientMessage(s, m, false))
	if err != nil {
		fmt.Printf("ambient: state error: %v\n", err)
		return
	}
	if !decision.Reply {
		return
	}
	if err := handleAmbientConversation(s, m); err != nil {
		fmt.Printf("ambient: conversation error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "Bro, LiteLLM is not cooperating right now. Totally unacceptable.")
	}
}

func ambientMessage(s *discordgo.Session, m *discordgo.MessageCreate, botMentioned bool) ambient.Message {
	return ambient.Message{ChannelID: m.ChannelID, GuildID: m.GuildID, UserID: m.Author.ID, Content: m.Content, BotMentioned: botMentioned, ReferencesBot: referencesBot(s, m)}
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
