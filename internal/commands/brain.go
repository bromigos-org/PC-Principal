package commands

import (
	"strings"

	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/bromigos-org/pc-principal/internal/llm"
	"github.com/bromigos-org/pc-principal/internal/memory"
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
