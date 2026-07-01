package memory

import (
	"context"
	"fmt"
)

type TouchedEntityRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
}

type ReasoningTraceStartRequest struct {
	Scope                Scope      `json:"scope"`
	SessionID            string     `json:"session_id"`
	Task                 string     `json:"task"`
	Metadata             JsonObject `json:"metadata"`
	TriggeredByMessageID string     `json:"triggered_by_message_id,omitempty"`
	UserIdentifier       string     `json:"user_identifier,omitempty"`
}

type ReasoningTraceStartResponse struct {
	TraceID   string `json:"trace_id"`
	SessionID string `json:"session_id"`
	Task      string `json:"task"`
}

type ReasoningStepRequest struct {
	Scope       Scope      `json:"scope"`
	TraceID     string     `json:"trace_id"`
	Action      string     `json:"action,omitempty"`
	Observation string     `json:"observation,omitempty"`
	StepNumber  int        `json:"step_number,omitempty"`
	Metadata    JsonObject `json:"metadata"`
}

type ReasoningStepResponse struct {
	StepID     string `json:"step_id"`
	TraceID    string `json:"trace_id"`
	StepNumber int    `json:"step_number"`
}

type ReasoningToolCallRequest struct {
	Scope           Scope              `json:"scope"`
	TraceID         string             `json:"trace_id"`
	StepID          string             `json:"step_id"`
	ToolName        string             `json:"tool_name"`
	Arguments       JsonObject         `json:"arguments"`
	Result          JsonValue          `json:"result,omitempty"`
	Status          string             `json:"status"`
	DurationMs      int                `json:"duration_ms,omitempty"`
	Error           string             `json:"error,omitempty"`
	MessageID       string             `json:"message_id,omitempty"`
	TouchedEntities []TouchedEntityRef `json:"touched_entities"`
}

type ReasoningToolCallResponse struct {
	ToolCallID string `json:"tool_call_id"`
	TraceID    string `json:"trace_id"`
	StepID     string `json:"step_id"`
}

type ReasoningTraceCompleteRequest struct {
	Scope    Scope      `json:"scope"`
	TraceID  string     `json:"trace_id"`
	Outcome  string     `json:"outcome,omitempty"`
	Success  *bool      `json:"success,omitempty"`
	Metadata JsonObject `json:"metadata"`
}

type ReasoningTraceCompleteResponse struct {
	TraceID     string `json:"trace_id"`
	Success     *bool  `json:"success,omitempty"`
	Outcome     string `json:"outcome,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
}

type ReasoningContextRequest struct {
	Scope            Scope  `json:"scope"`
	Query            string `json:"query"`
	IncludeTraces    bool   `json:"include_traces"`
	IncludeSteps     bool   `json:"include_steps"`
	IncludeToolCalls bool   `json:"include_tool_calls"`
	MaxItems         int    `json:"max_items"`
}

type ReasoningContextResponse struct {
	Context string       `json:"context"`
	Traces  []JsonObject `json:"traces"`
}

func (c *HTTPClient) StartReasoningTrace(ctx context.Context, request ReasoningTraceStartRequest) (ReasoningTraceStartResponse, error) {
	var response ReasoningTraceStartResponse
	if !c.enabled {
		return response, nil
	}
	if err := c.postJSON(ctx, "/v1/reasoning/traces", request, &response); err != nil {
		return response, fmt.Errorf("start reasoning trace: %w", err)
	}
	return response, nil
}

func (c *HTTPClient) AddReasoningStep(ctx context.Context, request ReasoningStepRequest) (ReasoningStepResponse, error) {
	var response ReasoningStepResponse
	if !c.enabled {
		return response, nil
	}
	path := fmt.Sprintf("/v1/reasoning/traces/%s/steps", request.TraceID)
	if err := c.postJSON(ctx, path, request, &response); err != nil {
		return response, fmt.Errorf("add reasoning step: %w", err)
	}
	return response, nil
}

func (c *HTTPClient) RecordReasoningToolCall(ctx context.Context, request ReasoningToolCallRequest) (ReasoningToolCallResponse, error) {
	var response ReasoningToolCallResponse
	if !c.enabled {
		return response, nil
	}
	path := fmt.Sprintf("/v1/reasoning/steps/%s/tool-calls", request.StepID)
	if err := c.postJSON(ctx, path, request, &response); err != nil {
		return response, fmt.Errorf("record reasoning tool call: %w", err)
	}
	return response, nil
}

func (c *HTTPClient) CompleteReasoningTrace(ctx context.Context, request ReasoningTraceCompleteRequest) (ReasoningTraceCompleteResponse, error) {
	var response ReasoningTraceCompleteResponse
	if !c.enabled {
		return response, nil
	}
	path := fmt.Sprintf("/v1/reasoning/traces/%s/complete", request.TraceID)
	if err := c.postJSON(ctx, path, request, &response); err != nil {
		return response, fmt.Errorf("complete reasoning trace: %w", err)
	}
	return response, nil
}

func (c *HTTPClient) GetReasoningContext(ctx context.Context, request ReasoningContextRequest) (ReasoningContextResponse, error) {
	var response ReasoningContextResponse
	if !c.enabled {
		return response, nil
	}
	if err := c.postJSON(ctx, "/v1/reasoning/context", request, &response); err != nil {
		return response, fmt.Errorf("get reasoning context: %w", err)
	}
	return response, nil
}
