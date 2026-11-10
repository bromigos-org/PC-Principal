package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
)

const testScene = "blackflame (Admin); Guild: Bromigos; Channel: general"

func TestRespond_sends_persona_context_and_the_message_to_the_model_in_order(t *testing.T) {
	// Given
	b := newBrain("I'm PC, Texas A&M!")
	b.memory.context = memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{
		{Source: "long_term_facts", Content: "blackflame likes graph memory", Facts: []memory.JsonObject{{"summary": "use combined context", "kind": "preference"}}},
	}}
	b.memory.skills = []memory.SkillRecord{
		{Name: "Summarize channel", Description: "Summarize visible context.", Status: memory.SkillStatusApproved, Metadata: memory.JsonObject{"reviewed": true}},
		{Name: "Draft helper", Description: "Not reviewed.", Status: memory.SkillStatusProposed, Metadata: memory.JsonObject{"reviewed": false}},
	}

	// When
	err := b.Respond(context.Background(), roomMessage("<@bot> prove you're pc"), "prove you're pc", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected an answer, got %v", err)
	}
	want := []store.Message{
		{Role: "system", Content: "You are the test persona."},
		{Role: "system", Content: "Recent Dragonfly conversation history:\nuser: [" + testScene + "]: prove you're pc"},
		{Role: "system", Content: "Relevant reviewed memory context:\nSource: long_term_facts\nblackflame likes graph memory\nFact: kind=preference; summary=use combined context"},
		{Role: "system", Content: "Reviewed non-executable skills:\nSummarize channel: Summarize visible context."},
		{Role: "user", Content: "[" + testScene + "]: prove you're pc"},
	}
	if len(b.llm.requests) != 1 || !reflect.DeepEqual(b.llm.requests[0], want) {
		t.Fatalf("unexpected model request:\n got %#v\nwant %#v", b.llm.requests, want)
	}
	if !reflect.DeepEqual(b.responder.replies, []string{"I'm PC, Texas A&M!"}) || len(b.responder.said) != 0 {
		t.Fatalf("expected one reply and nothing said, got replies=%#v said=%#v", b.responder.replies, b.responder.said)
	}
	if !reflect.DeepEqual(b.describer.said, []string{"prove you're pc"}) {
		t.Fatalf("expected the describer to be told what was said, got %#v", b.describer.said)
	}
}

func TestRespond_keeps_the_room_history_without_the_recalled_context(t *testing.T) {
	// Given
	b := newBrain("I'm PC, Texas A&M!", "You said prove it, bro.")
	b.memory.context = memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{Source: "graph", Content: "a graph fact"}}}
	ctx := context.Background()

	// When
	firstErr := b.Respond(ctx, roomMessage("one"), "one", b.describer, b.responder)
	secondErr := b.Respond(ctx, roomMessage("two"), "two", b.describer, b.responder)

	// Then
	if firstErr != nil || secondErr != nil {
		t.Fatalf("expected two answers, got %v and %v", firstErr, secondErr)
	}
	wantHistory := []store.Message{
		{Role: "system", Content: "You are the test persona."},
		{Role: "user", Content: "[" + testScene + "]: one"},
		{Role: "assistant", Content: "I'm PC, Texas A&M!"},
		{Role: "user", Content: "[" + testScene + "]: two"},
		{Role: "assistant", Content: "You said prove it, bro."},
	}
	if got := b.history.data["guild:space-1:channel:room-1"]; !reflect.DeepEqual(got, wantHistory) {
		t.Fatalf("unexpected stored history:\n got %#v\nwant %#v", got, wantHistory)
	}
	second := b.llm.requests[1]
	wantShortTerm := "Recent Dragonfly conversation history:\nuser: [" + testScene + "]: one\nassistant: I'm PC, Texas A&M!\nuser: [" + testScene + "]: two"
	if second[0].Content != "You are the test persona." || second[1].Content != wantShortTerm || second[2].Content != "Relevant reviewed memory context:\nSource: graph\na graph fact" {
		t.Fatalf("expected persona, then short-term history, then recalled memory, got %#v", second[:3])
	}
	if !reflect.DeepEqual(second[3:], wantHistory[1:4]) {
		t.Fatalf("expected the stored turns after the context, got %#v", second[3:])
	}
}

func TestRespond_leaves_out_local_history_when_memory_returns_short_term(t *testing.T) {
	// Given
	b := newBrain("Sweet.")
	b.memory.context = memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{MemoryType: "short_term", Content: "recent room context"}}}

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected an answer, got %v", err)
	}
	want := []store.Message{
		{Role: "system", Content: "You are the test persona."},
		{Role: "system", Content: "Relevant reviewed memory context:\nSource: short_term\nrecent room context"},
		{Role: "user", Content: "[" + testScene + "]: hello"},
	}
	if !reflect.DeepEqual(b.llm.requests[0], want) {
		t.Fatalf("unexpected model request:\n got %#v\nwant %#v", b.llm.requests[0], want)
	}
}

func TestRespond_does_its_steps_in_the_order_the_room_depends_on(t *testing.T) {
	// Given
	b := newBrain("Sweet.")

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected an answer, got %v", err)
	}
	want := []string{
		"history get guild:space-1:channel:room-1",
		"describe scene",
		"memory context",
		"memory skills",
		"llm",
		"history save guild:space-1:channel:room-1",
		"reply",
		"memory write user",
		"memory write assistant",
	}
	if !reflect.DeepEqual(b.journal.events, want) {
		t.Fatalf("unexpected order:\n got %#v\nwant %#v", b.journal.events, want)
	}
}

func TestRespond_writes_what_was_said_and_the_reply_to_memory_under_the_message_scope(t *testing.T) {
	// Given
	b := newBrain("I'm PC, Texas A&M!")

	// When
	err := b.Respond(context.Background(), roomMessage("<@bot> prove you're pc"), "prove you're pc", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected an answer, got %v", err)
	}
	scope := memory.Scope{TenantID: "tenant-1", SpaceID: "space-1", AgentID: "pc-principal", SessionID: "guild:space-1", UserID: "user-1", Visibility: memory.VisibilityGuild, GuildID: "space-1"}
	want := []memory.Message{
		{Scope: scope, Role: memory.RoleUser, Content: "prove you're pc"},
		{Scope: scope, Role: memory.RoleAssistant, Content: "I'm PC, Texas A&M!"},
	}
	if !reflect.DeepEqual(b.memory.messages, want) {
		t.Fatalf("unexpected memory writes:\n got %#v\nwant %#v", b.memory.messages, want)
	}
	wantRecall := memory.MemoryContextRequest{Scope: scope, Query: "prove you're pc", IncludeShortTerm: true, IncludeLongTerm: true, IncludeReasoning: true, IncludeGraph: true, MaxItems: 8, GraphLimit: 8}
	if len(b.memory.contextRequests) != 1 || !reflect.DeepEqual(b.memory.contextRequests[0], wantRecall) {
		t.Fatalf("unexpected recall request:\n got %#v\nwant %#v", b.memory.contextRequests, wantRecall)
	}
	if !reflect.DeepEqual(b.memory.skillRequests, []memory.SkillListRequest{{TenantID: "tenant-1", AgentID: "pc-principal"}}) {
		t.Fatalf("unexpected skill list request: %#v", b.memory.skillRequests)
	}
}

func TestRespond_records_a_reasoning_trace_without_the_words_that_were_said(t *testing.T) {
	// Given
	b := newBrain("I'm PC, Texas A&M!")

	// When
	err := b.Respond(context.Background(), roomMessage("<@bot> prove you're pc"), "prove you're pc", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected an answer, got %v", err)
	}
	wantStart := memory.ReasoningTraceStartRequest{
		Scope:                b.MemoryScope(roomMessage("")),
		SessionID:            "guild:space-1",
		Task:                 "reply to Discord mention conversation",
		Metadata:             memory.JsonObject{"channel_id": "room-1", "guild_id": "space-1", "agent_id": "pc-principal"},
		TriggeredByMessageID: "message-1",
		UserIdentifier:       "user-1",
	}
	if len(b.memory.startedTraces) != 1 || !reflect.DeepEqual(b.memory.startedTraces[0], wantStart) {
		t.Fatalf("unexpected trace start:\n got %#v\nwant %#v", b.memory.startedTraces, wantStart)
	}
	wantCalls := []string{
		"gnosis.memory_context=success",
		"gnosis.skills_list=success",
		"litellm.generate=success",
		"discord.message_send=success",
		"gnosis.message_write.user=success",
		"gnosis.message_write.assistant=success",
	}
	if got := b.memory.toolCallStatuses(); !reflect.DeepEqual(got, wantCalls) {
		t.Fatalf("unexpected tool calls:\n got %#v\nwant %#v", got, wantCalls)
	}
	if len(b.memory.steps) != 2 || b.memory.steps[0].Action != "retrieve_context" || b.memory.steps[0].StepNumber != 1 || b.memory.steps[1].Action != "prepare_context" || b.memory.steps[1].StepNumber != 2 {
		t.Fatalf("unexpected trace steps: %#v", b.memory.steps)
	}
	if len(b.memory.completedTraces) != 1 || b.memory.completedTraces[0].Outcome != "answered" || !*b.memory.completedTraces[0].Success {
		t.Fatalf("unexpected trace completion: %#v", b.memory.completedTraces)
	}
}

func TestRespond_answers_channel_activity_from_graph_facts_without_the_model(t *testing.T) {
	// Given
	b := newBrain()
	b.memory.context = memory.MemoryContextResponse{Sections: []memory.MemoryContextSection{{Source: "graph", Facts: []memory.JsonObject{
		{"type": "channel_activity", "rank": 2, "user_display_name": "BlackDave", "channel_name": "suggestions", "message_count": 1},
		{"type": "channel_activity", "rank": float64(1), "user_display_name": "BlackDave", "channel_name": "general", "message_count": float64(247)},
		{"type": "something_else", "rank": 3},
	}}}}

	// When
	err := b.Respond(context.Background(), roomMessage("top channels?"), "top channels?", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected an answer, got %v", err)
	}
	want := "BlackDave's top active channels:\n1. #general - 247 messages\n2. #suggestions - 1 message"
	if !reflect.DeepEqual(b.responder.replies, []string{want}) {
		t.Fatalf("unexpected reply: %#v", b.responder.replies)
	}
	if len(b.llm.requests) != 0 || len(b.memory.skillRequests) != 0 {
		t.Fatalf("expected no model call and no skill lookup, got llm=%d skills=%d", len(b.llm.requests), len(b.memory.skillRequests))
	}
	if len(b.memory.messages) != 2 || b.memory.messages[1].Content != want {
		t.Fatalf("expected the exchange written to memory, got %#v", b.memory.messages)
	}
	if got := b.memory.completedTraces[0].Outcome; got != "answered_channel_activity" {
		t.Fatalf("expected the trace to say how it was answered, got %q", got)
	}
}

func TestRespond_stops_before_asking_the_platform_when_history_cannot_be_read(t *testing.T) {
	// Given
	b := newBrain("unused")
	b.history.getErr = errors.New("store down")

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", b.describer, b.responder)

	// Then
	if err == nil || !strings.Contains(err.Error(), "get channel memory: store down") {
		t.Fatalf("expected the history error, got %v", err)
	}
	if !reflect.DeepEqual(b.journal.events, []string{"history get guild:space-1:channel:room-1"}) {
		t.Fatalf("expected nothing after the failed read, got %#v", b.journal.events)
	}
}

func TestRespond_reports_a_model_failure_and_answers_nothing(t *testing.T) {
	// Given
	b := newBrain()
	b.llm.err = errors.New("upstream rejected")

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", b.describer, b.responder)

	// Then
	if err == nil || err.Error() != "call LiteLLM: upstream rejected" {
		t.Fatalf("expected the model error, got %v", err)
	}
	if len(b.responder.replies) != 0 || len(b.history.data) != 0 || len(b.memory.messages) != 0 {
		t.Fatalf("expected no reply, no saved history and no memory write, got replies=%#v history=%#v writes=%#v", b.responder.replies, b.history.data, b.memory.messages)
	}
	if got := b.memory.completedTraces[0]; got.Outcome != "llm_failed" || *got.Success {
		t.Fatalf("expected a failed trace, got %#v", got)
	}
}

func TestRespond_reports_a_failed_save_before_replying(t *testing.T) {
	// Given
	b := newBrain("Sweet.")
	b.history.saveErr = errors.New("store full")

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", b.describer, b.responder)

	// Then
	if err == nil || err.Error() != "save channel memory: store full" {
		t.Fatalf("expected the save error, got %v", err)
	}
	if len(b.responder.replies) != 0 || len(b.memory.messages) != 0 {
		t.Fatalf("expected no reply and no memory write, got replies=%#v writes=%#v", b.responder.replies, b.memory.messages)
	}
	if got := b.memory.completedTraces[0].Outcome; got != "store_failed" {
		t.Fatalf("expected a store_failed trace, got %q", got)
	}
}

func TestRespond_reports_a_failed_reply_and_writes_nothing_to_memory(t *testing.T) {
	// Given
	b := newBrain("Sweet.")
	b.responder.replyErr = errors.New("platform refused")

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", b.describer, b.responder)

	// Then
	if err == nil || err.Error() != "send response: platform refused" {
		t.Fatalf("expected the send error, got %v", err)
	}
	if len(b.memory.messages) != 0 {
		t.Fatalf("expected no memory write for an answer nobody saw, got %#v", b.memory.messages)
	}
	if got := b.memory.completedTraces[0].Outcome; got != "discord_send_failed" {
		t.Fatalf("expected a send-failed trace, got %q", got)
	}
}

func TestRespond_answers_when_memory_is_down(t *testing.T) {
	// Given
	b := newBrain("Sweet.")
	b.memory.contextErr = errors.New("recall down")
	b.memory.skillsErr = errors.New("skills down")
	b.memory.writeErr = errors.New("write down")
	b.memory.startTraceErr = errors.New("trace down")

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected memory failures to stay out of the way, got %v", err)
	}
	if !reflect.DeepEqual(b.responder.replies, []string{"Sweet."}) {
		t.Fatalf("expected the reply, got %#v", b.responder.replies)
	}
	if len(b.memory.steps) != 0 || len(b.memory.toolCalls) != 0 || len(b.memory.completedTraces) != 0 {
		t.Fatalf("expected no trace records for a trace that never started, got %#v %#v %#v", b.memory.steps, b.memory.toolCalls, b.memory.completedTraces)
	}
	for _, message := range b.llm.requests[0] {
		if strings.Contains(message.Content, "down") {
			t.Fatalf("expected error details to stay out of the prompt, got %#v", b.llm.requests[0])
		}
	}
}

func TestRespond_uses_the_sender_name_when_the_platform_describes_nothing(t *testing.T) {
	// Given
	b := newBrain("Sweet.")

	// When
	err := b.Respond(context.Background(), roomMessage("hello"), "hello", nil, b.responder)

	// Then
	if err != nil {
		t.Fatalf("expected an answer, got %v", err)
	}
	request := b.llm.requests[0]
	if got := request[len(request)-1].Content; got != "[blackflame]: hello" {
		t.Fatalf("expected the sender's name as the prefix, got %q", got)
	}
}

func TestMemoryScope_is_one_scope_per_space_and_a_private_one_per_direct_conversation(t *testing.T) {
	// Given
	c := &Conversation{TenantID: "tenant-1"}
	otherRoom := roomMessage("")
	otherRoom.RoomID = "room-2"

	// When
	room := c.MemoryScope(roomMessage(""))
	sibling := c.MemoryScope(otherRoom)
	direct := c.MemoryScope(directMessage(""))

	// Then
	wantRoom := memory.Scope{TenantID: "tenant-1", SpaceID: "space-1", AgentID: "pc-principal", SessionID: "guild:space-1", UserID: "user-1", Visibility: memory.VisibilityGuild, GuildID: "space-1"}
	wantDirect := memory.Scope{TenantID: "tenant-1", SpaceID: "dm", AgentID: "pc-principal", SessionID: "dm:dm-room-1", UserID: "user-1", Visibility: memory.VisibilityPrivateUser, ChannelID: "dm-room-1"}
	if room != wantRoom || sibling != wantRoom {
		t.Fatalf("expected rooms of one space to share a scope:\n room    %#v\n sibling %#v\n want    %#v", room, sibling, wantRoom)
	}
	if direct != wantDirect {
		t.Fatalf("unexpected direct scope:\n got %#v\nwant %#v", direct, wantDirect)
	}
}

func TestHistoryKey_separates_rooms_and_direct_conversations(t *testing.T) {
	// Given
	otherRoom := roomMessage("")
	otherRoom.RoomID = "room-2"

	// Then
	for name, test := range map[string]struct {
		in   Inbound
		want string
	}{
		"room":    {in: roomMessage(""), want: "guild:space-1:channel:room-1"},
		"sibling": {in: otherRoom, want: "guild:space-1:channel:room-2"},
		"direct":  {in: directMessage(""), want: "dm:dm-room-1"},
	} {
		if got := historyKey(test.in); got != test.want {
			t.Fatalf("%s: expected history key %q, got %q", name, test.want, got)
		}
	}
}

func TestAddressedText(t *testing.T) {
	forms := []string{"<@bot-1>", "<@!bot-1>"}
	tests := []struct {
		name   string
		text   string
		want   string
		wantOK bool
	}{
		{name: "address first", text: "<@bot-1> prove you're pc", want: "prove you're pc", wantOK: true},
		{name: "second address form", text: "<@!bot-1> prove you're pc", want: "prove you're pc", wantOK: true},
		{name: "leading hey dropped", text: "<@bot-1> hey prove you're pc", want: "prove you're pc", wantOK: true},
		{name: "hey in any case", text: "<@bot-1> HEY prove it", want: "prove it", wantOK: true},
		{name: "hey kept when it is not first", text: "<@bot-1> oh hey there", want: "oh hey there", wantOK: true},
		{name: "address only", text: "  <@bot-1>  ", want: "", wantOK: true},
		{name: "address and hey only", text: "<@bot-1> hey", want: "", wantOK: true},
		{name: "address later", text: "<@user-2> said memory was broken <@bot-1> what do you know?", want: "<@user-2> said memory was broken what do you know?", wantOK: true},
		{name: "hey after a later address", text: "<@user-2> <@bot-1> hey what channel?", want: "<@user-2> what channel?", wantOK: true},
		{name: "only the first address is cut", text: "<@bot-1> who is <@bot-1>?", want: "who is <@bot-1>?", wantOK: true},
		{name: "no address in the text", text: "tell me more", want: "", wantOK: false},
		{name: "another member's mention is not the address", text: "<@user-2> hello", want: "", wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := AddressedText(test.text, forms)
			if got != test.want || ok != test.wantOK {
				t.Fatalf("AddressedText(%q) = %q, %v; want %q, %v", test.text, got, ok, test.want, test.wantOK)
			}
		})
	}
}

func TestStartSession_seeds_the_history_replies_then_saves(t *testing.T) {
	// Given
	b := newBrain("Kubernetes is sweet, bro.")

	// When
	b.StartSession(context.Background(), "thread-1", "blackflame (Admin)", "kubernetes networking", b.responder)

	// Then
	want := []store.Message{
		{Role: "system", Content: "You are the test persona."},
		{Role: "user", Content: "[blackflame (Admin)]: kubernetes networking"},
		{Role: "assistant", Content: "Kubernetes is sweet, bro."},
	}
	if got := b.history.data["thread-1"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected session history:\n got %#v\nwant %#v", got, want)
	}
	if !reflect.DeepEqual(b.llm.requests[0], want[:2]) {
		t.Fatalf("unexpected model request: %#v", b.llm.requests[0])
	}
	if !reflect.DeepEqual(b.journal.events, []string{"llm", "reply", "history save thread-1"}) {
		t.Fatalf("unexpected order: %#v", b.journal.events)
	}
	if len(b.memory.messages) != 0 || len(b.memory.contextRequests) != 0 {
		t.Fatalf("expected a session to stay out of long-term memory, got %#v %#v", b.memory.messages, b.memory.contextRequests)
	}
}

func TestStartSession_says_so_when_the_model_fails_and_saves_nothing(t *testing.T) {
	// Given
	b := newBrain()
	b.llm.err = errors.New("model down")

	// When
	b.StartSession(context.Background(), "thread-1", "blackflame", "kubernetes", b.responder)

	// Then
	if !reflect.DeepEqual(b.responder.said, []string{"LiteLLM is NOT responding. I am livid. Check the logs."}) || len(b.responder.replies) != 0 {
		t.Fatalf("expected the failure line only, got said=%#v replies=%#v", b.responder.said, b.responder.replies)
	}
	if len(b.history.data) != 0 {
		t.Fatalf("expected no session to be saved, got %#v", b.history.data)
	}
}

func TestFollowUp_ignores_a_room_that_is_not_a_session(t *testing.T) {
	// Given
	b := newBrain("unused")

	// When
	handled := b.FollowUp(context.Background(), roomMessage("hello"), b.describer, b.responder)

	// Then
	if handled {
		t.Fatal("expected a room without a session to be left alone")
	}
	if len(b.journal.events) != 0 {
		t.Fatalf("expected nothing to happen, got %#v", b.journal.events)
	}
}

func TestFollowUp_continues_the_session_saving_before_it_replies(t *testing.T) {
	// Given
	b := newBrain("Cilium rocks, dude.")
	seed := []store.Message{
		{Role: "system", Content: "You are the test persona."},
		{Role: "user", Content: "[blackflame (Admin)]: kubernetes"},
		{Role: "assistant", Content: "Sweet."},
	}
	b.history.data["room-1"] = seed

	// When
	handled := b.FollowUp(context.Background(), roomMessage("<@bot-1> what about cilium?"), b.describer, b.responder)

	// Then
	if !handled {
		t.Fatal("expected the session room to be handled")
	}
	want := append(append([]store.Message(nil), seed...),
		store.Message{Role: "user", Content: "[blackflame (Admin)]: <@bot-1> what about cilium?"},
		store.Message{Role: "assistant", Content: "Cilium rocks, dude."},
	)
	if got := b.history.data["room-1"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected session history:\n got %#v\nwant %#v", got, want)
	}
	if !reflect.DeepEqual(b.llm.requests[0], want[:4]) {
		t.Fatalf("expected the model to get the session as stored plus the new message, got %#v", b.llm.requests[0])
	}
	if !reflect.DeepEqual(b.journal.events, []string{"history get room-1", "describe sender", "llm", "history save room-1", "reply"}) {
		t.Fatalf("unexpected order: %#v", b.journal.events)
	}
	if !reflect.DeepEqual(b.responder.replies, []string{"Cilium rocks, dude."}) {
		t.Fatalf("unexpected reply: %#v", b.responder.replies)
	}
}

func TestFollowUp_says_so_when_the_model_fails_and_keeps_the_session_as_it_was(t *testing.T) {
	// Given
	b := newBrain()
	b.llm.err = errors.New("model down")
	seed := []store.Message{{Role: "system", Content: "You are the test persona."}}
	b.history.data["room-1"] = seed

	// When
	handled := b.FollowUp(context.Background(), roomMessage("still there?"), b.describer, b.responder)

	// Then
	if !handled {
		t.Fatal("expected the session room to be handled")
	}
	if !reflect.DeepEqual(b.responder.said, []string{"LiteLLM is DOWN right now. I am VERY upset. Someone fix the infrastructure."}) {
		t.Fatalf("expected the failure line, got %#v", b.responder.said)
	}
	if !reflect.DeepEqual(b.history.data["room-1"], seed) {
		t.Fatalf("expected the session to be unchanged, got %#v", b.history.data["room-1"])
	}
}
