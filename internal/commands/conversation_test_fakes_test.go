package commands

import (
	"context"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
)

type fakeAssistantClient struct {
	messages []store.Message
	reply    string
	err      error
}

func (c *fakeAssistantClient) Generate(ctx context.Context, messages []store.Message) (string, error) {
	c.messages = append([]store.Message(nil), messages...)
	return c.reply, c.err
}

type fakeMemoryClient struct {
	contextText        string
	contextErr         error
	graphText          string
	graphErr           error
	memoryContext      memory.MemoryContextResponse
	memoryContextErr   error
	skills             []memory.SkillRecord
	skillsErr          error
	writeErr           error
	queries            []memory.ContextQuery
	graphCalls         []memory.GraphContextRequest
	memoryContextCalls []memory.MemoryContextRequest
	skillCalls         []memory.SkillListRequest
	messages           []memory.Message
	startTraceErr      error
	stepErr            error
	toolCallErr        error
	completeTraceErr   error
	startedTraces      []memory.ReasoningTraceStartRequest
	reasoningSteps     []memory.ReasoningStepRequest
	toolCalls          []memory.ReasoningToolCallRequest
	completedTraces    []memory.ReasoningTraceCompleteRequest
}

func (c *fakeMemoryClient) GetContext(ctx context.Context, query memory.ContextQuery) (string, error) {
	c.queries = append(c.queries, query)
	return c.contextText, c.contextErr
}

func (c *fakeMemoryClient) AddMessage(ctx context.Context, message memory.Message) error {
	c.messages = append(c.messages, message)
	return c.writeErr
}

func (c *fakeMemoryClient) GetGraphContext(ctx context.Context, request memory.GraphContextRequest) (memory.GraphContextResponse, error) {
	c.graphCalls = append(c.graphCalls, request)
	return memory.GraphContextResponse{Context: c.graphText}, c.graphErr
}

func (c *fakeMemoryClient) GetMemoryContext(ctx context.Context, request memory.MemoryContextRequest) (memory.MemoryContextResponse, error) {
	c.memoryContextCalls = append(c.memoryContextCalls, request)
	return c.memoryContext, c.memoryContextErr
}

func (c *fakeMemoryClient) StartReasoningTrace(ctx context.Context, request memory.ReasoningTraceStartRequest) (memory.ReasoningTraceStartResponse, error) {
	c.startedTraces = append(c.startedTraces, request)
	if c.startTraceErr != nil {
		return memory.ReasoningTraceStartResponse{}, c.startTraceErr
	}
	return memory.ReasoningTraceStartResponse{TraceID: "trace-1", SessionID: request.SessionID, Task: request.Task}, nil
}

func (c *fakeMemoryClient) AddReasoningStep(ctx context.Context, request memory.ReasoningStepRequest) (memory.ReasoningStepResponse, error) {
	c.reasoningSteps = append(c.reasoningSteps, request)
	if c.stepErr != nil {
		return memory.ReasoningStepResponse{}, c.stepErr
	}
	stepID := "step-" + request.Action
	return memory.ReasoningStepResponse{StepID: stepID, TraceID: request.TraceID, StepNumber: request.StepNumber}, nil
}

func (c *fakeMemoryClient) RecordReasoningToolCall(ctx context.Context, request memory.ReasoningToolCallRequest) (memory.ReasoningToolCallResponse, error) {
	c.toolCalls = append(c.toolCalls, request)
	if c.toolCallErr != nil {
		return memory.ReasoningToolCallResponse{}, c.toolCallErr
	}
	return memory.ReasoningToolCallResponse{ToolCallID: "tool-call-1", TraceID: request.TraceID, StepID: request.StepID}, nil
}

func (c *fakeMemoryClient) CompleteReasoningTrace(ctx context.Context, request memory.ReasoningTraceCompleteRequest) (memory.ReasoningTraceCompleteResponse, error) {
	c.completedTraces = append(c.completedTraces, request)
	if c.completeTraceErr != nil {
		return memory.ReasoningTraceCompleteResponse{}, c.completeTraceErr
	}
	return memory.ReasoningTraceCompleteResponse{TraceID: request.TraceID, Success: request.Success, Outcome: request.Outcome}, nil
}

func (c *fakeMemoryClient) GetReasoningContext(ctx context.Context, request memory.ReasoningContextRequest) (memory.ReasoningContextResponse, error) {
	return memory.ReasoningContextResponse{}, nil
}

func (c *fakeMemoryClient) IngestEvent(ctx context.Context, event memory.ClientEvent) error {
	return nil
}

func (c *fakeMemoryClient) IngestEvents(ctx context.Context, events []memory.ClientEvent) (memory.ClientEventBatchResponse, error) {
	return memory.ClientEventBatchResponse{}, nil
}

func (c *fakeMemoryClient) ListSkills(ctx context.Context, request memory.SkillListRequest) (memory.SkillListResponse, error) {
	c.skillCalls = append(c.skillCalls, request)
	return memory.SkillListResponse{Skills: c.skills}, c.skillsErr
}

func (c *fakeMemoryClient) ProposeSkill(ctx context.Context, proposal memory.SkillProposal) (memory.SkillProposal, error) {
	return memory.SkillProposal{}, nil
}

func (c *fakeMemoryClient) RecordSkillUsage(ctx context.Context, usage memory.SkillUsage) error {
	return nil
}
