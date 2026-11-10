package commands

import (
	"context"

	"github.com/bwmarrin/discordgo"
)

// HeyThreadHandler continues a PC Principal conversation inside a tracked thread.
// Every message in a thread seeded by the hey command gets a response.
func HeyThreadHandler(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author == nil || m.Author.ID == s.State.User.ID || m.Author.Bot {
		return
	}

	if !hasAllowedRole(m.Member) {
		return
	}

	dispatcher().HandleFollowUp(context.Background(), inboundFromMessage(s, m), discordDescriber{session: s, message: m}, respondTo(s, m))
}
