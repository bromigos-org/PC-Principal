package core

import (
	"context"
	"log"
	"strings"

	"github.com/bromigos-org/pc-principal/internal/memory"
)

// These names go to gnosis in the reasoning trace of every answer. They say
// Discord because Discord is the only front end so far, and they are kept
// as they were so that the traces already stored and the ones written from
// now on read the same.
const (
	traceTask         = "reply to Discord mention conversation"
	traceSendTool     = "discord.message_send"
	traceSendFailed   = "discord_send_failed"
	traceSendStage    = "discord"
	traceRoomArgument = "channel_id"
)

type conversationReasoningTrace struct {
	memory     memory.Client
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

func (c *Conversation) startReasoningTrace(ctx context.Context, in Inbound, scope memory.Scope) conversationReasoningTrace {
	response, err := c.Memory.StartReasoningTrace(ctx, memory.ReasoningTraceStartRequest{
		Scope:                scope,
		SessionID:            scope.SessionID,
		Task:                 traceTask,
		TriggeredByMessageID: in.MessageID,
		UserIdentifier:       in.SenderID,
		Metadata: memory.JsonObject{
			"channel_id": in.RoomID,
			"guild_id":   in.SpaceID,
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
	return conversationReasoningTrace{memory: c.Memory, scope: scope, traceID: response.TraceID, nextStep: 1, enabled: true}
}

func (t *conversationReasoningTrace) recordStep(ctx context.Context, action string, observation string, metadata memory.JsonObject) {
	if !t.enabled {
		return
	}
	response, err := t.memory.AddReasoningStep(ctx, memory.ReasoningStepRequest{
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
	if _, err := t.memory.RecordReasoningToolCall(ctx, request); err != nil {
		log.Printf("gnosis reasoning tool call failed: %v", err)
	}
}

func (t conversationReasoningTrace) complete(ctx context.Context, outcome string, success bool, metadata memory.JsonObject) {
	if !t.enabled {
		return
	}
	if _, err := t.memory.CompleteReasoningTrace(ctx, memory.ReasoningTraceCompleteRequest{
		Scope:    t.scope,
		TraceID:  t.traceID,
		Outcome:  outcome,
		Success:  &success,
		Metadata: metadata,
	}); err != nil {
		log.Printf("gnosis reasoning trace complete failed: %v", err)
	}
}
