package commands

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
)

const (
	combinedMemorySystemPrefix = "Relevant reviewed memory context:\n"
	skillSystemPrefix          = "Reviewed non-executable skills:\n"
)

type conversationMemoryPromptRequest struct {
	scope memory.Scope
	query string
}

type conversationMemoryPromptResult struct {
	prompt                 string
	hasShortTerm           bool
	combinedContextSuccess bool
	context                memory.MemoryContextResponse
}

func (result conversationMemoryPromptResult) traceStatus() string {
	if result.combinedContextSuccess {
		return "success"
	}
	return "error"
}

func conversationMemoryPrompt(ctx context.Context, request conversationMemoryPromptRequest) conversationMemoryPromptResult {
	combined, err := conversationMemory.GetMemoryContext(ctx, memory.MemoryContextRequest{
		Scope:            request.scope,
		Query:            request.query,
		IncludeShortTerm: true,
		IncludeLongTerm:  true,
		IncludeReasoning: true,
		IncludeGraph:     true,
		MaxItems:         memoryContextLimit,
		GraphLimit:       graphContextLimit,
	})
	if err == nil {
		return conversationMemoryPromptResult{prompt: combinedMemoryPrompt(combined), hasShortTerm: hasShortTermMemorySection(combined), combinedContextSuccess: true, context: combined}
	}
	log.Printf("gnosis combined context recall failed: %v", err)
	return conversationMemoryPromptResult{}
}

type channelActivityFact struct {
	rank            int
	channelName     string
	messageCount    int
	userDisplayName string
}

func channelActivityReply(response memory.MemoryContextResponse) string {
	facts := channelActivityFacts(response)
	if len(facts) == 0 {
		return ""
	}
	if len(facts) > 5 {
		facts = facts[:5]
	}
	lines := make([]string, 0, len(facts)+1)
	lines = append(lines, facts[0].userDisplayName+"'s top active channels:")
	for _, fact := range facts {
		messageLabel := "messages"
		if fact.messageCount == 1 {
			messageLabel = "message"
		}
		lines = append(lines, fmt.Sprintf("%d. #%s - %d %s", fact.rank, fact.channelName, fact.messageCount, messageLabel))
	}
	return strings.Join(lines, "\n")
}

func channelActivityFacts(response memory.MemoryContextResponse) []channelActivityFact {
	facts := make([]channelActivityFact, 0)
	for _, section := range response.Sections {
		for _, raw := range section.Facts {
			fact, ok := parseChannelActivityFact(raw)
			if ok {
				facts = append(facts, fact)
			}
		}
	}
	sort.Slice(facts, func(i, j int) bool {
		return facts[i].rank < facts[j].rank
	})
	return facts
}

func parseChannelActivityFact(raw memory.JsonObject) (channelActivityFact, bool) {
	if factType, ok := raw["type"].(string); !ok || factType != "channel_activity" {
		return channelActivityFact{}, false
	}
	rank, ok := jsonInt(raw["rank"])
	if !ok {
		return channelActivityFact{}, false
	}
	messageCount, ok := jsonInt(raw["message_count"])
	if !ok {
		return channelActivityFact{}, false
	}
	channelName, ok := nonEmptyJSONText(raw["channel_name"])
	if !ok {
		return channelActivityFact{}, false
	}
	userDisplayName, ok := nonEmptyJSONText(raw["user_display_name"])
	if !ok {
		return channelActivityFact{}, false
	}
	return channelActivityFact{rank: rank, channelName: channelName, messageCount: messageCount, userDisplayName: userDisplayName}, true
}

func jsonInt(value memory.JsonValue) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if typed != float64(int(typed)) {
			return 0, false
		}
		return int(typed), true
	default:
		return 0, false
	}
}

func nonEmptyJSONText(value memory.JsonValue) (string, bool) {
	text, ok := value.(string)
	if !ok {
		return "", false
	}
	text = strings.TrimSpace(text)
	return text, text != ""
}

func historyWithMemoryContext(history []store.Message, memoryPrompt conversationMemoryPromptResult, skills []memory.SkillRecord) []store.Message {
	contextMessages := memoryContextMessages(history, memoryPrompt, skills)
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

func memoryContextMessages(history []store.Message, memoryPrompt conversationMemoryPromptResult, skills []memory.SkillRecord) []store.Message {
	contextMessages := make([]store.Message, 0, 3)
	if shortTerm := shortTermHistoryContext(history); shortTerm != "" && !memoryPrompt.hasShortTerm {
		contextMessages = append(contextMessages, store.Message{Role: "system", Content: shortTermSystemPrefix + shortTerm})
	}
	if memoryContext := strings.TrimSpace(memoryPrompt.prompt); memoryContext != "" {
		contextMessages = append(contextMessages, store.Message{Role: "system", Content: memoryContext})
	}
	if skillContext := skillPromptContext(skills); skillContext != "" {
		contextMessages = append(contextMessages, store.Message{Role: "system", Content: skillSystemPrefix + skillContext})
	}
	return contextMessages
}

func hasShortTermMemorySection(response memory.MemoryContextResponse) bool {
	for _, section := range response.Sections {
		if strings.EqualFold(memorySectionLabel(section), "short_term") && memorySectionPrompt(section) != "" {
			return true
		}
	}
	return false
}

func combinedMemoryPrompt(response memory.MemoryContextResponse) string {
	sections := make([]string, 0, len(response.Sections))
	for _, section := range response.Sections {
		if sectionPrompt := memorySectionPrompt(section); sectionPrompt != "" {
			sections = append(sections, sectionPrompt)
		}
	}
	if len(sections) == 0 {
		return ""
	}
	return combinedMemorySystemPrefix + strings.Join(sections, "\n\n")
}

func memorySectionPrompt(section memory.MemoryContextSection) string {
	lines := make([]string, 0, len(section.Facts)+2)
	if label := memorySectionLabel(section); label != "" {
		lines = append(lines, "Source: "+label)
	}
	if content := strings.TrimSpace(section.Content); content != "" {
		lines = append(lines, content)
	}
	for _, fact := range section.Facts {
		if factText := factPrompt(fact); factText != "" {
			lines = append(lines, "Fact: "+factText)
		}
	}
	return strings.Join(lines, "\n")
}

func memorySectionLabel(section memory.MemoryContextSection) string {
	if memoryType := strings.TrimSpace(section.MemoryType); memoryType != "" {
		return memoryType
	}
	return strings.TrimSpace(section.Source)
}

func factPrompt(fact memory.JsonObject) string {
	keys := make([]string, 0, len(fact))
	for key, value := range fact {
		if strings.TrimSpace(key) != "" && value != nil {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+fmt.Sprint(fact[key]))
	}
	return strings.Join(parts, "; ")
}

func skillPromptContext(skills []memory.SkillRecord) string {
	parts := make([]string, 0, len(skills))
	for _, skill := range skills {
		if skill.Status != memory.SkillStatusApproved || skill.Metadata["reviewed"] != true {
			continue
		}
		name := strings.TrimSpace(skill.Name)
		description := strings.TrimSpace(skill.Description)
		if name == "" || description == "" {
			continue
		}
		parts = append(parts, name+": "+description)
	}
	return strings.Join(parts, "\n")
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
