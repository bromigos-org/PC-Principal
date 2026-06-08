package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("mdelete", "@PC-Principal mdelete <n>", "Delete the last N messages in this channel.", DeleteMessages)
}

func DeleteMessages(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) != 3 {
		s.ChannelMessageSend(m.ChannelID, "Bro, usage: `mdelete <number>`")
		return
	}

	numMessages, err := strconv.Atoi(parts[2])
	if err != nil || numMessages < 1 {
		s.ChannelMessageSend(m.ChannelID, "Bro, that's not a valid number.")
		return
	}

	messages, err := s.ChannelMessages(m.ChannelID, numMessages, "", "", "")
	if err != nil {
		fmt.Printf("mdelete: error retrieving messages: %v\n", err)
		return
	}

	for _, msg := range messages {
		if err := s.ChannelMessageDelete(m.ChannelID, msg.ID); err != nil {
			fmt.Printf("mdelete: error deleting message %s: %v\n", msg.ID, err)
		}
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Deleted %d messages. Totally clean, bro.", numMessages))
}
