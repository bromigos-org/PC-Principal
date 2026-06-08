package commands

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("mpost", "@PC-Principal mpost #channel <message>", "Post a message to a channel as the bot (from #moderator-only).", PostToChannel)
}

const moderatorChannelName = "moderator-only"

func PostToChannel(s *discordgo.Session, m *discordgo.MessageCreate) {
	channel, err := s.Channel(m.ChannelID)
	if err != nil {
		fmt.Printf("Error retrieving channel: %v\n", err)
		return
	}

	if channel.Name == moderatorChannelName {
		rgx := regexp.MustCompile(`<#(\d+)>`)
		matches := rgx.FindStringSubmatch(m.Content)
		if len(matches) < 2 {
			fmt.Println("Invalid channel mention format")
			return
		}
		channelID := matches[1]

		mentionEndIndex := strings.Index(m.Content, fmt.Sprintf("<#%s>", channelID)) + len(fmt.Sprintf("<#%s>", channelID))
		if mentionEndIndex == -1 {
			fmt.Println("Channel mention not found")
			return
		}

		messageContent := strings.TrimSpace(m.Content[mentionEndIndex:])
		if messageContent == "" {
			fmt.Println("No message content provided")
			return
		}

		_, err := s.ChannelMessageSend(channelID, messageContent)
		if err != nil {
			fmt.Printf("Error sending message: %v\n", err)
		}
	}
}
