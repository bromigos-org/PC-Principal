package core

import (
	"context"
	"errors"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
)

// journal is the order in which the fakes were called, across all of them.
type journal struct{ events []string }

func (j *journal) add(event string) {
	if j != nil {
		j.events = append(j.events, event)
	}
}

type fakeLLM struct {
	journal  *journal
	requests [][]store.Message
	replies  []string
	err      error
}

func (c *fakeLLM) Generate(_ context.Context, messages []store.Message) (string, error) {
	c.journal.add("llm")
	c.requests = append(c.requests, append([]store.Message(nil), messages...))
	if c.err != nil {
		return "", c.err
	}
	if len(c.replies) == 0 {
		return "", errors.New("fakeLLM: no reply queued")
	}
	reply := c.replies[0]
	c.replies = c.replies[1:]
	return reply, nil
}

// fakeHistory keeps conversations in memory.
type fakeHistory struct {
	journal *journal
	data    map[string][]store.Message
	getErr  error
	saveErr error
}

func newFakeHistory(j *journal) *fakeHistory {
	return &fakeHistory{journal: j, data: map[string][]store.Message{}}
}

func (h *fakeHistory) Exists(_ context.Context, key string) (bool, error) {
	_, ok := h.data[key]
	return ok, nil
}

func (h *fakeHistory) Get(_ context.Context, key string) ([]store.Message, error) {
	h.journal.add("history get " + key)
	if h.getErr != nil {
		return nil, h.getErr
	}
	return append([]store.Message(nil), h.data[key]...), nil
}

func (h *fakeHistory) Save(_ context.Context, key string, messages []store.Message) error {
	h.journal.add("history save " + key)
	if h.saveErr != nil {
		return h.saveErr
	}
	h.data[key] = append([]store.Message(nil), messages...)
	return nil
}

type fakeDescriber struct {
	journal *journal
	sender  string
	scene   string
	said    []string
}

func (d *fakeDescriber) Sender(context.Context, Inbound) string {
	d.journal.add("describe sender")
	return d.sender
}

func (d *fakeDescriber) Scene(_ context.Context, _ Inbound, said string) string {
	d.journal.add("describe scene")
	d.said = append(d.said, said)
	return d.scene
}

// fakeMemory records what the brain sends to long-term memory.
type fakeMemory struct {
	journal          *journal
	context          memory.MemoryContextResponse
	contextErr       error
	skills           []memory.SkillRecord
	skillsErr        error
	writeErr         error
	startTraceErr    error
	contextRequests  []memory.MemoryContextRequest
	skillRequests    []memory.SkillListRequest
	messages         []memory.Message
	startedTraces    []memory.ReasoningTraceStartRequest
	steps            []memory.ReasoningStepRequest
	toolCalls        []memory.ReasoningToolCallRequest
	completedTraces  []memory.ReasoningTraceCompleteRequest
	graphContextCall int
}

func (c *fakeMemory) AddMessage(_ context.Context, message memory.Message) error {
	c.journal.add("memory write " + string(message.Role))
	c.messages = append(c.messages, message)
	return c.writeErr
}

func (c *fakeMemory) GetMemoryContext(_ context.Context, request memory.MemoryContextRequest) (memory.MemoryContextResponse, error) {
	c.journal.add("memory context")
	c.contextRequests = append(c.contextRequests, request)
	return c.context, c.contextErr
}

func (c *fakeMemory) GetGraphContext(context.Context, memory.GraphContextRequest) (memory.GraphContextResponse, error) {
	c.graphContextCall++
	return memory.GraphContextResponse{}, nil
}

func (c *fakeMemory) StartReasoningTrace(_ context.Context, request memory.ReasoningTraceStartRequest) (memory.ReasoningTraceStartResponse, error) {
	c.startedTraces = append(c.startedTraces, request)
	if c.startTraceErr != nil {
		return memory.ReasoningTraceStartResponse{}, c.startTraceErr
	}
	return memory.ReasoningTraceStartResponse{TraceID: "trace-1"}, nil
}

func (c *fakeMemory) AddReasoningStep(_ context.Context, request memory.ReasoningStepRequest) (memory.ReasoningStepResponse, error) {
	c.steps = append(c.steps, request)
	return memory.ReasoningStepResponse{StepID: "step-" + request.Action}, nil
}

func (c *fakeMemory) RecordReasoningToolCall(_ context.Context, request memory.ReasoningToolCallRequest) (memory.ReasoningToolCallResponse, error) {
	c.toolCalls = append(c.toolCalls, request)
	return memory.ReasoningToolCallResponse{}, nil
}

func (c *fakeMemory) CompleteReasoningTrace(_ context.Context, request memory.ReasoningTraceCompleteRequest) (memory.ReasoningTraceCompleteResponse, error) {
	c.completedTraces = append(c.completedTraces, request)
	return memory.ReasoningTraceCompleteResponse{}, nil
}

func (c *fakeMemory) GetReasoningContext(context.Context, memory.ReasoningContextRequest) (memory.ReasoningContextResponse, error) {
	return memory.ReasoningContextResponse{}, nil
}

func (c *fakeMemory) IngestEvent(context.Context, memory.ClientEvent) error { return nil }

func (c *fakeMemory) IngestEvents(context.Context, []memory.ClientEvent) (memory.ClientEventBatchResponse, error) {
	return memory.ClientEventBatchResponse{}, nil
}

func (c *fakeMemory) ListSkills(_ context.Context, request memory.SkillListRequest) (memory.SkillListResponse, error) {
	c.journal.add("memory skills")
	c.skillRequests = append(c.skillRequests, request)
	return memory.SkillListResponse{Skills: c.skills}, c.skillsErr
}

func (c *fakeMemory) ProposeSkill(context.Context, memory.SkillProposal) (memory.SkillProposal, error) {
	return memory.SkillProposal{}, nil
}

func (c *fakeMemory) RecordSkillUsage(context.Context, memory.SkillUsage) error { return nil }

func (c *fakeMemory) toolCallStatuses() []string {
	statuses := make([]string, 0, len(c.toolCalls))
	for _, call := range c.toolCalls {
		statuses = append(statuses, call.ToolName+"="+call.Status)
	}
	return statuses
}

// brain is a Conversation with every dependency faked, and the fakes.
type brain struct {
	*Conversation
	journal   *journal
	llm       *fakeLLM
	memory    *fakeMemory
	history   *fakeHistory
	describer *fakeDescriber
	responder *textResponder
}

func newBrain(replies ...string) *brain {
	j := &journal{}
	b := &brain{
		journal:   j,
		llm:       &fakeLLM{journal: j, replies: replies},
		memory:    &fakeMemory{journal: j},
		history:   newFakeHistory(j),
		describer: &fakeDescriber{journal: j, sender: "blackflame (Admin)", scene: "blackflame (Admin); Guild: Bromigos; Channel: general"},
		responder: &textResponder{journal: j},
	}
	b.Conversation = &Conversation{Persona: "You are the test persona.", LLM: b.llm, Memory: b.memory, TenantID: "tenant-1", History: b.history}
	return b
}

func roomMessage(text string) Inbound {
	return Inbound{
		Platform:   PlatformDiscord,
		MessageID:  "message-1",
		SpaceID:    "space-1",
		RoomID:     "room-1",
		SenderID:   "user-1",
		SenderName: "blackflame",
		Text:       text,
	}
}

func directMessage(text string) Inbound {
	in := roomMessage(text)
	in.SpaceID = ""
	in.RoomID = "dm-room-1"
	in.Direct = true
	return in
}
