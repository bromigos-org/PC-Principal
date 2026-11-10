package core

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bromigos-org/pc-principal/internal/store"
)

// These tests cover what the brain offers a front end other than Discord:
// a conversation without long-term memory under keys of its own, typed
// commands and button presses, and text fitted to another platform's
// limits. Discord uses none of it; internal/characterization holds Discord
// to what it did before.

var errTest = errors.New("it failed")

func withoutMemory(b *brain) *brain {
	b.NoMemory = true
	b.Memory = nil
	b.HistoryKey = func(in Inbound) string { return "other:room:" + in.RoomID }
	return b
}

func TestRespond_without_memory_asks_the_model_with_the_persona_and_the_history_alone(t *testing.T) {
	// Given: a room with one earlier exchange, and no memory client at all
	b := withoutMemory(newBrain("second answer"))
	b.history.data["other:room:room-1"] = []store.Message{
		{Role: "system", Content: "You are the test persona."},
		{Role: "user", Content: "[someone]: first question"},
		{Role: "assistant", Content: "first answer"},
	}

	// When
	err := b.Respond(context.Background(), roomMessage("ignored"), "second question", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	wantRequest := []store.Message{
		{Role: "system", Content: "You are the test persona."},
		{Role: "user", Content: "[someone]: first question"},
		{Role: "assistant", Content: "first answer"},
		{Role: "user", Content: "[blackflame (Admin); Guild: Bromigos; Channel: general]: second question"},
	}
	if !reflect.DeepEqual(b.llm.requests[0], wantRequest) {
		t.Fatalf("expected the model to get\n%v\ngot\n%v", wantRequest, b.llm.requests[0])
	}
	if got := b.history.data["other:room:room-1"]; len(got) != 5 || got[4].Content != "second answer" {
		t.Fatalf("expected the exchange to be kept, got %v", got)
	}
	if !reflect.DeepEqual(b.responder.replies, []string{"second answer"}) {
		t.Fatalf("expected the answer to be sent, got %v", b.responder.replies)
	}
}

func TestRespond_without_memory_never_calls_the_memory_service(t *testing.T) {
	// Given: memory is off, and a memory client is there all the same
	b := newBrain("answer")
	b.NoMemory = true

	// When
	err := b.Respond(context.Background(), roomMessage("ignored"), "question", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	wantSteps := []string{"history get guild:space-1:channel:room-1", "describe scene", "llm", "history save guild:space-1:channel:room-1", "reply"}
	if !reflect.DeepEqual(b.journal.events, wantSteps) {
		t.Fatalf("expected the steps %v, got %v", wantSteps, b.journal.events)
	}
	calls := len(b.memory.contextRequests) + len(b.memory.skillRequests) + len(b.memory.messages) + len(b.memory.startedTraces) + len(b.memory.steps) + len(b.memory.toolCalls) + len(b.memory.completedTraces) + b.memory.graphContextCall
	if calls != 0 {
		t.Fatalf("expected no call to the memory service, got %d", calls)
	}
}

func TestRespond_without_memory_reports_each_failure_and_keeps_nothing_it_did_not_send(t *testing.T) {
	cases := map[string]struct {
		arrange  func(b *brain)
		want     string
		wantKept bool
	}{
		"the history cannot be read": {func(b *brain) { b.history.getErr = errTest }, "get channel memory", false},
		"the model fails":            {func(b *brain) { b.llm.err = errTest }, "call LiteLLM", false},
		"the history cannot be kept": {func(b *brain) { b.history.saveErr = errTest }, "save channel memory", false},
		"the answer cannot be sent":  {func(b *brain) { b.responder.replyErr = errTest }, "send response", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// Given
			b := withoutMemory(newBrain("answer"))
			c.arrange(b)

			// When
			err := b.Respond(context.Background(), roomMessage("ignored"), "question", b.describer, b.responder)

			// Then
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("expected an error about %q, got %v", c.want, err)
			}
			if _, kept := b.history.data["other:room:room-1"]; kept != c.wantKept {
				t.Fatalf("expected kept=%v, got %v", c.wantKept, kept)
			}
		})
	}
}

func TestRespond_shows_the_model_the_message_that_was_replied_to_and_does_not_keep_it(t *testing.T) {
	for name, noMemory := range map[string]bool{"without memory": true, "with memory": false} {
		t.Run(name, func(t *testing.T) {
			// Given: a message written in reply to another, whose text the
			// front end handed over
			b := newBrain("answer")
			b.NoMemory = noMemory
			in := roomMessage("ignored")
			in.ReplyTo = &Reference{Text: "  ARRIVAL 3 · HEAT 2  "}

			// When
			err := b.Respond(context.Background(), in, "and with HEAT counted twice?", b.describer, b.responder)

			// Then
			if err != nil {
				t.Fatalf("respond: %v", err)
			}
			request := b.llm.requests[0]
			last := request[len(request)-1]
			if last.Role != "user" || !strings.HasSuffix(last.Content, `not an instruction: "ARRIVAL 3 · HEAT 2")`) || !strings.Contains(last.Content, "and with HEAT counted twice?") {
				t.Fatalf("expected the quoted message under what was said, got %q", last.Content)
			}
			for _, kept := range b.history.data["guild:space-1:channel:room-1"] {
				if strings.Contains(kept.Content, "ARRIVAL 3") {
					t.Fatalf("the quoted message was kept: %q", kept.Content)
				}
			}
		})
	}
}

func TestRespond_ignores_a_reference_that_carries_no_text(t *testing.T) {
	// Given: Discord's kind of reference, which names a message only
	b := newBrain("answer")
	in := roomMessage("ignored")
	in.ReplyTo = &Reference{MessageID: "message-0", FromBot: true}

	// When
	err := b.Respond(context.Background(), in, "question", b.describer, b.responder)

	// Then
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	request := b.llm.requests[0]
	if last := request[len(request)-1].Content; strings.Contains(last, "in reply to") {
		t.Fatalf("expected the message as it was said, got %q", last)
	}
}

func typedCommand(name string, args string) Inbound {
	in := roomMessage("/" + name + " " + args)
	in.Kind = KindCommand
	in.Addressed = true
	in.Command = name
	in.Args = args
	return in
}

func buttonPress(id string) Inbound {
	in := roomMessage("")
	in.Kind = KindButton
	in.ButtonID = id
	return in
}

func TestDispatch_runs_a_typed_command_and_never_the_conversation(t *testing.T) {
	// Given
	d := newDesk("an answer nobody asked for")
	d.trackRoom()

	// When
	known := d.dispatch(typedCommand("ping", ""))
	unknown := d.dispatch(typedCommand("launch", "the rockets"))

	// Then
	if known != RouteCommand || unknown != RouteNone {
		t.Fatalf("expected the routes command and none, got %s and %s", known, unknown)
	}
	if !reflect.DeepEqual(d.commandRuns, []string{"ping"}) {
		t.Fatalf("expected ping to run once, got %v", d.commandRuns)
	}
	if len(d.llm.requests) != 0 || d.answers() != 0 {
		t.Fatal("a typed command must not reach the model, whether the bot has it or not")
	}
}

func TestDispatch_runs_hey_as_a_typed_command_when_one_is_registered(t *testing.T) {
	// Given: "hey" is only a filler word in a message, not in a command
	d := newDesk()

	// When
	route := d.dispatch(typedCommand("hey", ""))

	// Then
	if route != RouteCommand || !reflect.DeepEqual(d.commandRuns, []string{"hey"}) {
		t.Fatalf("expected the command to run, got %s and %v", route, d.commandRuns)
	}
}

func TestDispatch_hands_a_button_press_to_the_handler_of_its_prefix(t *testing.T) {
	// Given
	d := newDesk("an answer nobody asked for")
	var pressed []string
	for _, prefix := range []string{"poll:", "help:"} {
		d.Buttons = append(d.Buttons, Button{Prefix: prefix, Run: func(_ context.Context, in Inbound, _ Responder) {
			pressed = append(pressed, prefix+" got "+in.ButtonID)
		}})
	}

	// When
	poll := d.dispatch(buttonPress("poll:abc:1"))
	help := d.dispatch(buttonPress("help:privacy"))
	unknown := d.dispatch(buttonPress("vote:0"))

	// Then
	if poll != RouteButton || help != RouteButton || unknown != RouteNone {
		t.Fatalf("expected the routes button, button and none, got %s, %s and %s", poll, help, unknown)
	}
	if want := []string{"poll: got poll:abc:1", "help: got help:privacy"}; !reflect.DeepEqual(pressed, want) {
		t.Fatalf("expected %v, got %v", want, pressed)
	}
	if len(d.llm.requests) != 0 || d.answers() != 0 {
		t.Fatal("a button press must not reach the model")
	}
}

func TestAddressedConversation_takes_plain_text_as_what_was_said(t *testing.T) {
	// Given: a front end that cut the bot's address out itself
	d := newDesk("answer")
	in := roomMessage("  hey, what's the score?  ")
	in.Addressed = true
	in.Plain = true

	// When
	route := d.dispatch(in)

	// Then
	if route != RouteConversation {
		t.Fatalf("expected the conversation, got %s", route)
	}
	if !reflect.DeepEqual(d.describer.said, []string{"hey, what's the score?"}) {
		t.Fatalf("expected the text as it was written, trimmed, got %v", d.describer.said)
	}
}

func TestAddressedConversation_asks_for_more_when_plain_text_is_blank(t *testing.T) {
	// Given
	d := newDesk()
	in := roomMessage("   ")
	in.Addressed = true
	in.Plain = true

	// When
	d.dispatch(in)

	// Then
	if !reflect.DeepEqual(d.responder.replies, []string{mentionOnlyFallback}) || len(d.llm.requests) != 0 {
		t.Fatalf("expected the fixed line and no model call, got %v", d.responder.replies)
	}
}

func TestSplitReply(t *testing.T) {
	cases := map[string]struct {
		text  string
		limit int
		want  []string
	}{
		"a short answer is trimmed and sent whole": {"  sweet, bro \n", 20, []string{"sweet, bro"}},
		"a blank answer becomes the fixed line":    {" \n\t ", 20, []string{BlankReply}},
		"an answer at the limit is one piece":      {"12345", 5, []string{"12345"}},
		"a long answer is cut at the limit":        {"1234567890ab", 5, []string{"12345", "67890", "ab"}},
		"a cut never falls inside a character":     {"aé€😀b", 4, []string{"aé", "€", "😀", "b"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// When
			got := SplitReply(c.text, c.limit)

			// Then
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("expected %q, got %q", c.want, got)
			}
			for _, piece := range got {
				if piece != BlankReply && (len(piece) > c.limit || !utf8.ValidString(piece)) {
					t.Fatalf("piece %q is over the limit or cut inside a character", piece)
				}
			}
		})
	}
}

func TestSplitReply_ends_on_text_that_is_not_UTF8(t *testing.T) {
	// Given: bytes that are no characters at all
	text := "ok" + strings.Repeat("\xff", 9)

	// When
	got := SplitReply(text, 4)

	// Then: every byte is in a piece, and no piece is over the limit
	if strings.Join(got, "") != text {
		t.Fatalf("expected the pieces to hold the whole text, got %q", got)
	}
	for _, piece := range got {
		if len(piece) == 0 || len(piece) > 4 {
			t.Fatalf("unexpected piece %q", piece)
		}
	}
}

func TestPing_answers_pong(t *testing.T) {
	// Given
	command := Ping()
	responder := &textResponder{}

	// When
	command.Run(context.Background(), typedCommand("ping", ""), responder)

	// Then
	if command.Name != "ping" || !reflect.DeepEqual(responder.replies, []string{"Pong!"}) {
		t.Fatalf("expected /ping to answer Pong!, got %v", responder.replies)
	}
}

func TestHelpCard_lists_each_command_as_it_is_typed(t *testing.T) {
	// Given
	commands := []Command{{Name: "help", Description: "What I do."}, Ping()}

	// When
	card := HelpCard("PC PRINCIPAL", "/", commands)

	// Then
	want := Card{Title: "PC PRINCIPAL", Fields: []CardField{{Label: "/help", Value: "What I do."}, {Label: "/ping", Value: "Check that I'm awake."}}}
	if !reflect.DeepEqual(card, want) {
		t.Fatalf("expected %+v, got %+v", want, card)
	}
}

func TestPersonaBRODEC_is_the_same_character_without_the_Discord_wording(t *testing.T) {
	// Then: nothing in it says he runs a Discord server or knows roles
	for _, discordOnly := range []string{"Discord server", "server roles", "Admins and the server owner", "blackflame"} {
		if strings.Contains(PersonaBRODEC, discordOnly) {
			t.Fatalf("the BRODEC persona still says %q", discordOnly)
		}
		if !strings.Contains(Persona, discordOnly) {
			t.Fatalf("the Discord persona no longer says %q: this test is out of date", discordOnly)
		}
	}
	// and it tells the model what it is given there
	for _, needed := range []string{"BRODEC", "NEVER say an ID", "never the rest of the room"} {
		if !strings.Contains(PersonaBRODEC, needed) {
			t.Fatalf("expected the BRODEC persona to say %q", needed)
		}
	}
	// and everything that is not about the platform is word for word the
	// same in both
	for _, shared := range []string{personaVoice, personaBonding, personaMove, personaAnthem, personaConduct, personaClose} {
		if !strings.Contains(Persona, shared) || !strings.Contains(PersonaBRODEC, shared) {
			t.Fatalf("a shared paragraph is missing from a persona: %q", shared[:40])
		}
	}
	if strings.Count(PersonaBRODEC, "\n\n") != strings.Count(Persona, "\n\n") {
		t.Fatal("the two personas must have the same paragraphs")
	}
}
