package commands

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("help", "@PC-Principal help", "Display this help message.", Help)
}

func Help(s *discordgo.Session, m *discordgo.MessageCreate) {
	var sb strings.Builder
	sb.WriteString("**Available commands:**\n")
	for _, cmd := range registry {
		sb.WriteString(fmt.Sprintf("• `%s` — %s\n", cmd.Usage, cmd.Description))
	}

	channel, err := s.UserChannelCreate(m.Author.ID)
	if err != nil {
		fmt.Println("error creating DM channel:", err)
		s.ChannelMessageSend(m.ChannelID, "Something went wrong while sending the DM!")
		return
	}
	_, err = s.ChannelMessageSend(channel.ID, sb.String())
	if err != nil {
		fmt.Println("error sending DM:", err)
		s.ChannelMessageSend(m.ChannelID, "Failed to send you a DM. Did you disable DMs in your privacy settings?")
	}
}
