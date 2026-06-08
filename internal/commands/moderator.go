package commands

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("mpost", "@PC-Principal mpost #channel <message>", "Post a message to a channel as the bot.", PostToChannel)
}

func PostToChannel(s *discordgo.Session, m *discordgo.MessageCreate) {
	rgx := regexp.MustCompile(`<#(\d+)>`)
	matches := rgx.FindStringSubmatch(m.Content)
	if len(matches) < 2 {
		s.ChannelMessageSend(m.ChannelID, "Bro, you need to mention a channel. Like: `mpost #channel your message`")
		return
	}
	channelID := matches[1]

	mention := fmt.Sprintf("<#%s>", channelID)
	mentionEnd := strings.Index(m.Content, mention) + len(mention)
	messageContent := strings.TrimSpace(m.Content[mentionEnd:])
	if messageContent == "" {
		s.ChannelMessageSend(m.ChannelID, "Bro, you need to include a message after the channel.")
		return
	}

	if _, err := s.ChannelMessageSend(channelID, messageContent); err != nil {
		fmt.Printf("mpost: error sending message: %v\n", err)
	}
}
