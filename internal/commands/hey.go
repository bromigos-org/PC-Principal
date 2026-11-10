package commands

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

func init() {
	register("hey", "@PC-Principal hey <message>", "Ask PC Principal something.", Hey)
}

func Hey(s *discordgo.Session, m *discordgo.MessageCreate) {
	text, ok := mentionConversationText(m.Content, s.State.User.ID)
	if !ok || text == "" {
		s.ChannelMessageSend(m.ChannelID, "Bro, you gotta actually say something after 'hey'. That's how it works, dude.")
		return
	}
	if err := handleMentionConversation(s, m); err != nil {
		fmt.Printf("hey: LiteLLM error: %v\n", err)
		s.ChannelMessageSend(m.ChannelID, "Bro, LiteLLM is not cooperating right now. Totally unacceptable.")
	}
}
