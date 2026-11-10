package commands

import (
	"context"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/bromigos-org/pc-principal/internal/llm"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

// What the bot's brain is wired to in this process. The brain itself lives
// in internal/core; this package is its Discord front end.
var (
	assistantClient            llm.Client    = llm.NewLiteLLMClient(nil)
	conversationMemory         memory.Client = memory.NewClient(memory.Config{}, nil)
	conversationMemoryTenantID               = "bromigos"
)

func ConfigureMemory(client memory.Client) {
	conversationMemory = client
	if conversationMemory == nil {
		conversationMemory = memory.NewClient(memory.Config{}, nil)
	}
}

func ConfigureMemoryTenant(tenantID string) {
	if strings.TrimSpace(tenantID) != "" {
		conversationMemoryTenantID = tenantID
	}
}

// conversation returns the brain's conversation, wired to what the process
// is configured with at this moment. It is built for each message because
// the wiring above can be replaced while the bot runs, and building it
// costs nothing.
func conversation() *core.Conversation {
	return &core.Conversation{
		Persona:  core.Persona,
		LLM:      assistantClient,
		Memory:   conversationMemory,
		TenantID: conversationMemoryTenantID,
	}
}

// dispatcher returns the brain's dispatcher, wired like conversation.
//
// BotMention, HeyThreadHandler and AmbientReply each call one of its
// handlers. The live message handler offers every message to all three in
// turn, as it always has, so a mention written in a chat thread is still
// answered by two of them. core.Dispatcher.Dispatch would pick one; moving
// Discord to it changes behaviour and is left for a change of its own.
func dispatcher() *core.Dispatcher {
	return &core.Dispatcher{Conversation: conversation(), Ambient: ambientManager}
}

// mentionCommands are the registered commands in the form the dispatcher
// looks them up in, bound to one message. The handlers stay Discord's: each
// one gets the session and the message, as before.
func mentionCommands(s *discordgo.Session, m *discordgo.MessageCreate) []core.Command {
	commands := make([]core.Command, 0, len(registry))
	for _, cmd := range registry {
		commands = append(commands, core.Command{
			Name: cmd.Name,
			Run:  func(context.Context, core.Inbound, core.Responder) { cmd.Handler(s, m) },
		})
	}
	return commands
}
