package commands

import (
	"context"
	"log"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bwmarrin/discordgo"
)

type conversationReasoningTrace struct {
	scope      memory.Scope
	traceID    string
	lastStepID string
	nextStep   int
	enabled    bool
}

func traceStatus(err error) string {
	if err == nil {
		return "success"
	}
	return "error"
}

func startConversationReasoningTrace(ctx context.Context, m *discordgo.MessageCreate, scope memory.Scope) conversationReasoningTrace {
	response, err := conversationMemory.StartReasoningTrace(ctx, memory.ReasoningTraceStartRequest{
		Scope:                scope,
		SessionID:            scope.SessionID,
		Task:                 "reply to Discord mention conversation",
		TriggeredByMessageID: m.ID,
		UserIdentifier:       m.Author.ID,
		Metadata: memory.JsonObject{
			"channel_id": m.ChannelID,
			"guild_id":   m.GuildID,
			"agent_id":   scope.AgentID,
		},
	})
	if err != nil {
		log.Printf("gnosis reasoning trace start failed: %v", err)
		return conversationReasoningTrace{}
	}
	if strings.TrimSpace(response.TraceID) == "" {
		return conversationReasoningTrace{}
	}
	return conversationReasoningTrace{scope: scope, traceID: response.TraceID, nextStep: 1, enabled: true}
}

func (t *conversationReasoningTrace) recordStep(ctx context.Context, action string, observation string, metadata memory.JsonObject) {
	if !t.enabled {
		return
	}
	response, err := conversationMemory.AddReasoningStep(ctx, memory.ReasoningStepRequest{
		Scope:       t.scope,
		TraceID:     t.traceID,
		Action:      action,
		Observation: observation,
		StepNumber:  t.nextStep,
		Metadata:    metadata,
	})
	if err != nil {
		log.Printf("gnosis reasoning step failed: %v", err)
		return
	}
	t.nextStep++
	t.lastStepID = response.StepID
}

func (t *conversationReasoningTrace) recordToolCall(ctx context.Context, toolName string, status string, arguments memory.JsonObject, result memory.JsonValue) {
	if !t.enabled || t.lastStepID == "" {
		return
	}
	request := memory.ReasoningToolCallRequest{
		Scope:     t.scope,
		TraceID:   t.traceID,
		StepID:    t.lastStepID,
		ToolName:  toolName,
		Arguments: arguments,
		Result:    result,
		Status:    status,
	}
	if status == "error" {
		request.Error = "operation failed"
	}
	if _, err := conversationMemory.RecordReasoningToolCall(ctx, request); err != nil {
		log.Printf("gnosis reasoning tool call failed: %v", err)
	}
}

func (t conversationReasoningTrace) complete(ctx context.Context, outcome string, success bool, metadata memory.JsonObject) {
	if !t.enabled {
		return
	}
	if _, err := conversationMemory.CompleteReasoningTrace(ctx, memory.ReasoningTraceCompleteRequest{
		Scope:    t.scope,
		TraceID:  t.traceID,
		Outcome:  outcome,
		Success:  &success,
		Metadata: metadata,
	}); err != nil {
		log.Printf("gnosis reasoning trace complete failed: %v", err)
	}
}
