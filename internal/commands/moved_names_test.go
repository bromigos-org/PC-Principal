package commands

import (
	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

// The tests in this package were written when the conversation lived here.
// The names below are the ones they use for things that moved to
// internal/core, bound to the same things, so that the tests read as they
// did and still check the Discord front end end to end.
const (
	memoryContextLimit = core.MemoryContextLimit
	graphContextLimit  = core.GraphContextLimit
)

func conversationMemoryScope(m *discordgo.MessageCreate) memory.Scope {
	s := &discordgo.Session{State: discordgo.NewState()}
	s.State.User = &discordgo.User{ID: "bot-1", Username: "PC Principal", Bot: true}
	return conversation().MemoryScope(inboundFromMessage(s, m))
}
