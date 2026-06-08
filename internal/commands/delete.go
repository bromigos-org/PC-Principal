package commands

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("mdelete", "@PC-Principal mdelete <n>", "Delete the last N messages in this channel (admin only).", DeleteMessages)
}

func DeleteMessages(s *discordgo.Session, m *discordgo.MessageCreate) {
	parts := strings.Fields(m.Content)
	if len(parts) != 3 {
		s.ChannelMessageSend(m.ChannelID, "Usage: @PC-Principal mdelete <number>")
		return
	}

	numMessages, err := strconv.Atoi(parts[2])
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, "Invalid number of messages to delete.")
		return
	}

	member, err := s.GuildMember(m.GuildID, m.Author.ID)
	if err != nil {
		fmt.Printf("Error retrieving guild member: %v\n", err)
		return
	}

	hasAdminPermission := false
	for _, roleID := range member.Roles {
		role, err := s.State.Role(m.GuildID, roleID)
		if err != nil {
			fmt.Printf("Error retrieving role: %v\n", err)
			continue
		}
		if role.Permissions&discordgo.PermissionAdministrator != 0 {
			hasAdminPermission = true
			break
		}
	}

	if !hasAdminPermission {
		s.ChannelMessageSend(m.ChannelID, "You do not have permission to use this command.")
		return
	}

	messages, err := s.ChannelMessages(m.ChannelID, numMessages, "", "", "")
	if err != nil {
		fmt.Printf("Error retrieving messages: %v\n", err)
		return
	}

	for _, message := range messages {
		if err := s.ChannelMessageDelete(m.ChannelID, message.ID); err != nil {
			fmt.Printf("Error deleting message: %v\n", err)
		}
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Deleted the last %d messages.", numMessages))
}
