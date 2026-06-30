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
	trace := startConversationReasoningTrace(ctx, m, scope)
	trace.recordStep(ctx, "retrieve_context", "Retrieved scoped memory and reviewed skill context.", memory.JsonObject{"stage": "context"})
	memoryPrompt := conversationMemoryPrompt(ctx, conversationMemoryPromptRequest{scope: scope, query: userMessage})
	trace.recordToolCall(ctx, "gnosis.memory_context", memoryPrompt.traceStatus(), memory.JsonObject{
		"graph_limit":           graphContextLimit,
		"include_graph":         true,
		"include_long_term":     true,
		"include_reasoning":     true,
		"include_short_term":    true,
		"max_items":             memoryContextLimit,
		"used_short_term_scope": memoryPrompt.hasShortTerm,
	}, memory.JsonObject{"prompt_empty": strings.TrimSpace(memoryPrompt.prompt) == ""})
	skills, err := conversationMemory.ListSkills(ctx, memory.SkillListRequest{
		TenantID: scope.TenantID,
		AgentID:  scope.AgentID,
	})
	if err != nil {
		log.Printf("gnosis skill list failed: %v", err)
	}
	trace.recordToolCall(ctx, "gnosis.skills_list", traceStatus(err), memory.JsonObject{"agent_id": scope.AgentID}, memory.JsonObject{"skills_count": len(skills.Skills)})
	requestHistory := historyWithMemoryContext(history, memoryPrompt, skills.Skills)
	trace.recordStep(ctx, "prepare_context", "Prepared scoped memory, graph, and reviewed skill context.", memory.JsonObject{
		"message_count": len(requestHistory),
		"skills_count":  len(skills.Skills),
	})
	reply, err := generateAssistant(ctx, requestHistory)
	if err != nil {
		trace.recordToolCall(ctx, "litellm.generate", "error", memory.JsonObject{"message_count": len(requestHistory)}, nil)
		trace.complete(ctx, "llm_failed", false, memory.JsonObject{"stage": "llm"})
		return fmt.Errorf("call LiteLLM: %w", err)
	}
	trace.recordToolCall(ctx, "litellm.generate", "success", memory.JsonObject{"message_count": len(requestHistory)}, memory.JsonObject{"reply_empty": strings.TrimSpace(reply) == ""})
	history = append(history, store.Message{Role: "assistant", Content: reply})
	if err := store.Save(ctx, key, history); err != nil {
		trace.complete(ctx, "store_failed", false, memory.JsonObject{"stage": "channel_memory"})
		return fmt.Errorf("save channel memory: %w", err)
	}
	if err := sendDiscordResponse(s, m.ChannelID, reply); err != nil {
		trace.recordToolCall(ctx, "discord.message_send", "error", memory.JsonObject{"channel_id": m.ChannelID}, nil)
		trace.complete(ctx, "discord_send_failed", false, memory.JsonObject{"stage": "discord"})
		return fmt.Errorf("send response: %w", err)
	}
	trace.recordToolCall(ctx, "discord.message_send", "success", memory.JsonObject{"channel_id": m.ChannelID}, memory.JsonObject{"sent": true})
	if err := conversationMemory.AddMessage(ctx, memory.Message{Scope: scope, Role: memory.RoleUser, Content: userMessage}); err != nil {
		trace.recordToolCall(ctx, "gnosis.message_write.user", "error", memory.JsonObject{"role": string(memory.RoleUser)}, nil)
		log.Printf("gnosis user write failed: %v", err)
	} else {
		trace.recordToolCall(ctx, "gnosis.message_write.user", "success", memory.JsonObject{"role": string(memory.RoleUser)}, memory.JsonObject{"stored": true})
	}
	if err := conversationMemory.AddMessage(ctx, memory.Message{Scope: scope, Role: memory.RoleAssistant, Content: reply}); err != nil {
		trace.recordToolCall(ctx, "gnosis.message_write.assistant", "error", memory.JsonObject{"role": string(memory.RoleAssistant)}, nil)
		log.Printf("gnosis assistant write failed: %v", err)
	} else {
		trace.recordToolCall(ctx, "gnosis.message_write.assistant", "success", memory.JsonObject{"role": string(memory.RoleAssistant)}, memory.JsonObject{"stored": true})
	}
	trace.complete(ctx, "answered", true, memory.JsonObject{"stage": "complete"})
	return nil
}

func mentionConversationText(content string, botID string) (string, bool) {
	text := strings.TrimSpace(content)
	mentionForms := []string{"<@" + botID + ">", "<@!" + botID + ">"}
	for _, mention := range mentionForms {
		mentionIndex := strings.Index(text, mention)
		if mentionIndex >= 0 {
			before := strings.TrimSpace(text[:mentionIndex])
			after := trimLeadingHey(strings.TrimSpace(text[mentionIndex+len(mention):]))
			return strings.TrimSpace(strings.Join([]string{before, after}, " ")), true
		}
	}
	return "", false
}

func trimLeadingHey(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "hey") {
		return text
	}
	return strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
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
	sessionID := "dm:" + m.ChannelID
	channelID := m.ChannelID
	if m.GuildID != "" {
		spaceID = m.GuildID
		visibility = memory.VisibilityGuild
		sessionID = "guild:" + m.GuildID
		channelID = ""
	}
	return memory.Scope{
		TenantID:   conversationMemoryTenantID,
		SpaceID:    spaceID,
		AgentID:    "pc-principal",
		SessionID:  sessionID,
		UserID:     m.Author.ID,
		Visibility: visibility,
		GuildID:    m.GuildID,
		ChannelID:  channelID,
	}
}
