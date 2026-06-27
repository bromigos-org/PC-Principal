package commands

import (
	"context"
	"fmt"
	"log"
	"strings"
	"unicode/utf8"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
)

const (
	discordMessageLimit     = 2000
	discordSafeMessageLimit = 1900
	emptyAssistantResponse  = "Bro, my brain totally blanked. Try that again."
	mentionOnlyFallback     = "Bro, I'm here. Say what you need after the mention."
	memoryContextLimit      = 8
	graphContextLimit       = 8
	shortTermSystemPrefix   = "Recent Dragonfly conversation history:\n"
	memorySystemPrefix      = "Relevant long-term recall:\n"
	graphSystemPrefix       = "Relevant Discord graph facts:\n"
)

var (
	conversationMemory         memory.Client = memory.NewClient(memory.Config{}, nil)
	conversationMemoryTenantID               = "bromigos"
)

type conversationRequest struct {
	Message     *discordgo.MessageCreate
	UserMessage string
}

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

func handleMentionConversation(s *discordgo.Session, m *discordgo.MessageCreate) error {
	userMessage, ok := mentionConversationText(m.Content, s.State.User.ID)
	if !ok {
		return nil
	}
	if userMessage == "" {
		return sendDiscordResponse(s, m.ChannelID, mentionOnlyFallback)
	}
	if err := handleConversation(s, conversationRequest{Message: m, UserMessage: userMessage}); err != nil {
		return err
	}
	return recordAmbientActivation(m)
}

func handleConversation(s *discordgo.Session, request conversationRequest) error {
	ctx := context.Background()
	m := request.Message
	userMessage := request.UserMessage
	key := channelMemoryKey(m)
	history, err := store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("get channel memory: %w", err)
	}
	if len(history) == 0 {
		history = []store.Message{{Role: "system", Content: pcPrincipalSystemPrompt}}
	}

	promptContext := buildConversationContext(s, m, fetchContextData(s, m, userMessage))
	history = append(history, store.Message{Role: "user", Content: fmt.Sprintf("[%s]: %s", promptContext, userMessage)})

	scope := conversationMemoryScope(m)
	recalledContext, err := conversationMemory.GetContext(ctx, memory.ContextQuery{
		Scope: scope,
		Query: userMessage,
		Limit: memoryContextLimit,
	})
	if err != nil {
		log.Printf("agents-memory context recall failed: %v", err)
	}
	graphContext, err := conversationMemory.GetGraphContext(ctx, memory.GraphContextRequest{
		Scope:           scope,
		Query:           userMessage,
		Limit:           graphContextLimit,
		IncludeTopology: true,
	})
	if err != nil {
		log.Printf("agents-memory graph context recall failed: %v", err)
	}
	reply, err := generateAssistant(ctx, historyWithMemoryContext(history, recalledContext, graphContext.Context))
	if err != nil {
		return fmt.Errorf("call LiteLLM: %w", err)
	}
	history = append(history, store.Message{Role: "assistant", Content: reply})
	if err := store.Save(ctx, key, history); err != nil {
		return fmt.Errorf("save channel memory: %w", err)
	}
	if err := sendDiscordResponse(s, m.ChannelID, reply); err != nil {
		return fmt.Errorf("send response: %w", err)
	}
	if err := conversationMemory.AddMessage(ctx, memory.Message{Scope: scope, Role: memory.RoleUser, Content: userMessage}); err != nil {
		log.Printf("agents-memory user write failed: %v", err)
	}
	if err := conversationMemory.AddMessage(ctx, memory.Message{Scope: scope, Role: memory.RoleAssistant, Content: reply}); err != nil {
		log.Printf("agents-memory assistant write failed: %v", err)
	}
	return nil
}

func historyWithMemoryContext(history []store.Message, recalledContext string, graphContext string) []store.Message {
	contextMessages := memoryContextMessages(history, recalledContext, graphContext)
	if len(contextMessages) == 0 {
		return history
	}
	requestHistory := make([]store.Message, 0, len(history)+len(contextMessages))
	if len(history) > 0 && history[0].Role == "system" {
		requestHistory = append(requestHistory, history[0])
		requestHistory = append(requestHistory, contextMessages...)
		requestHistory = append(requestHistory, history[1:]...)
		return requestHistory
	}
	requestHistory = append(requestHistory, contextMessages...)
	requestHistory = append(requestHistory, history...)
	return requestHistory
}

func memoryContextMessages(history []store.Message, recalledContext string, graphContext string) []store.Message {
	contextMessages := make([]store.Message, 0, 3)
	if shortTerm := shortTermHistoryContext(history); shortTerm != "" {
		contextMessages = append(contextMessages, store.Message{Role: "system", Content: shortTermSystemPrefix + shortTerm})
	}
	if recalled := strings.TrimSpace(recalledContext); recalled != "" {
		contextMessages = append(contextMessages, store.Message{Role: "system", Content: memorySystemPrefix + recalled})
	}
	if graph := strings.TrimSpace(graphContext); graph != "" {
		contextMessages = append(contextMessages, store.Message{Role: "system", Content: graphSystemPrefix + graph})
	}
	return contextMessages
}

func shortTermHistoryContext(history []store.Message) string {
	parts := make([]string, 0, len(history))
	for _, message := range history {
		if message.Role == "system" {
			continue
		}
		content := strings.TrimSpace(message.Content)
		if content == "" {
			continue
		}
		parts = append(parts, message.Role+": "+content)
	}
	return strings.Join(parts, "\n")
}

func mentionConversationText(content string, botID string) (string, bool) {
	mentionForms := []string{"<@" + botID + ">", "<@!" + botID + ">"}
	for _, mention := range mentionForms {
		if strings.HasPrefix(strings.TrimSpace(content), mention) {
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), mention))
			if strings.HasPrefix(strings.ToLower(text), "hey") {
				fields := strings.Fields(text)
				if len(fields) > 0 && strings.EqualFold(fields[0], "hey") {
					if len(fields) == 1 {
						return "", true
					}
					return strings.TrimSpace(strings.TrimPrefix(text, fields[0])), true
				}
			}
			return text, true
		}
	}
	return "", false
}

func sendDiscordResponse(s *discordgo.Session, channelID string, response string) error {
	for _, chunk := range chunkDiscordMessage(response) {
		msg := &discordgo.MessageSend{
			Content: chunk,
			AllowedMentions: &discordgo.MessageAllowedMentions{
				Parse: []discordgo.AllowedMentionType{
					discordgo.AllowedMentionTypeUsers,
					discordgo.AllowedMentionTypeRoles,
					discordgo.AllowedMentionTypeEveryone,
				},
			},
		}
		if _, err := s.ChannelMessageSendComplex(channelID, msg); err != nil {
			return fmt.Errorf("send Discord message: %w", err)
		}
	}
	return nil
}

func chunkDiscordMessage(response string) []string {
	text := strings.TrimSpace(response)
	if text == "" {
		return []string{emptyAssistantResponse}
	}
	if len(text) <= discordSafeMessageLimit {
		return []string{text}
	}

	chunks := make([]string, 0, len(text)/discordSafeMessageLimit+1)
	for len(text) > 0 {
		cut := safeChunkSize(text)
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return chunks
}

func safeChunkSize(text string) int {
	if len(text) <= discordSafeMessageLimit {
		return len(text)
	}
	cut := discordSafeMessageLimit
	for !utf8.ValidString(text[:cut]) {
		cut--
	}
	return cut
}

func channelMemoryKey(m *discordgo.MessageCreate) string {
	if m.GuildID == "" {
		return "dm:" + m.ChannelID
	}
	return "guild:" + m.GuildID + ":channel:" + m.ChannelID
}

func conversationMemoryScope(m *discordgo.MessageCreate) memory.Scope {
	spaceID := "dm"
	visibility := memory.VisibilityPrivateUser
	if m.GuildID != "" {
		spaceID = m.GuildID
		visibility = memory.VisibilityChannel
	}
	return memory.Scope{
		TenantID:   conversationMemoryTenantID,
		SpaceID:    spaceID,
		AgentID:    "pc-principal",
		SessionID:  channelMemoryKey(m),
		UserID:     m.Author.ID,
		Visibility: visibility,
		GuildID:    m.GuildID,
		ChannelID:  m.ChannelID,
	}
}
