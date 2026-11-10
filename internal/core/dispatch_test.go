package core

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/ambient"
	"github.com/bromigos-org/pc-principal/internal/store"
)

var botAddressForms = []string{"<@bot-1>", "<@!bot-1>"}

// desk is a Dispatcher with every dependency faked, and the fakes.
type desk struct {
	*Dispatcher
	*brain
	commandRuns []string
	sessions    *ambientSessions
	clock       *deskClock
}

func newDesk(replies ...string) *desk {
	d := &desk{brain: newBrain(replies...)}
	d.Dispatcher = &Dispatcher{Conversation: d.brain.Conversation}
	for _, name := range []string{"ping", "hey"} {
		d.Commands = append(d.Commands, Command{Name: name, Run: func(_ context.Context, in Inbound, r Responder) {
			d.journal.add("command " + name)
			d.commandRuns = append(d.commandRuns, name)
		}})
	}
	return d
}

// withAmbient switches ambient sessions on.
func (d *desk) withAmbient() *desk {
	d.sessions = &ambientSessions{states: map[string]ambient.State{}}
	d.clock = &deskClock{now: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)}
	d.Ambient = ambient.NewManager(ambient.Config{Enabled: true}, d.sessions, d.clock)
	return d
}

// startSession opens an ambient session for the room's usual sender, old
// enough that the bot may answer again.
func (d *desk) startSession(t *testing.T) {
	t.Helper()
	if err := d.Ambient.Activate(context.Background(), "room-1", "space-1", "user-1"); err != nil {
		t.Fatalf("start ambient session: %v", err)
	}
	d.clock.now = d.clock.now.Add(2 * time.Minute)
}

// trackRoom makes the room a session's room.
func (d *desk) trackRoom() {
	d.history.data["room-1"] = []store.Message{{Role: "system", Content: "You are the test persona."}}
}

func (d *desk) dispatch(in Inbound) Route {
	return d.Dispatch(context.Background(), in, d.describer, d.responder)
}

// answers is how many times the bot spoke.
func (d *desk) answers() int {
	return len(d.responder.replies) + len(d.responder.said)
}

type deskClock struct{ now time.Time }

func (c *deskClock) Now() time.Time { return c.now }

type ambientSessions struct {
	states map[string]ambient.State
	err    error
}

func (s *ambientSessions) Load(_ context.Context, key string) (ambient.State, bool, error) {
	if s.err != nil {
		return ambient.State{}, false, s.err
	}
	state, ok := s.states[key]
	return state, ok, nil
}

func (s *ambientSessions) Save(_ context.Context, key string, state ambient.State, _ time.Duration) error {
	s.states[key] = state
	return nil
}

func (s *ambientSessions) Delete(_ context.Context, key string) error {
	delete(s.states, key)
	return nil
}

func addressed(text string, command string) Inbound {
	in := roomMessage(text)
	in.Addressed = true
	in.AddressForms = botAddressForms
	in.Command = command
	return in
}

// TestDispatch_takes_one_route_per_message tries every combination of what
// a message can be and what state its room can be in, and checks for each
// that the bot did the work of exactly the route it reported, and never
// answered twice.
func TestDispatch_takes_one_route_per_message(t *testing.T) {
	type messageKind struct {
		name      string
		addressed bool
		text      string
		command   string
	}
	messages := []messageKind{
		{name: "addressed with a command", addressed: true, text: "<@bot-1> ping", command: "ping"},
		{name: "addressed with an unknown word", addressed: true, text: "<@bot-1> dance for me", command: "dance"},
		{name: "addressed with hey", addressed: true, text: "<@bot-1> hey there", command: "hey"},
		{name: "addressed and nothing else", addressed: true, text: "<@bot-1>"},
		{name: "addressed without the address in the text", addressed: true, text: "tell me more", command: "me"},
		{name: "addressed asking to stop", addressed: true, text: "<@bot-1> please stop", command: "please"},
		{name: "addressed with a command and stop", addressed: true, text: "<@bot-1> ping stop", command: "ping"},
		{name: "plain message", text: "I am also PC"},
		{name: "plain stop", text: "stop"},
		{name: "blank message", text: "   "},
	}
	type roomKind struct {
		name    string
		tracked bool
		ambient string // "off", "idle" or "active"
	}
	var rooms []roomKind
	for _, tracked := range []bool{false, true} {
		for _, ambientState := range []string{"off", "idle", "active"} {
			rooms = append(rooms, roomKind{name: fmt.Sprintf("tracked=%v ambient=%s", tracked, ambientState), tracked: tracked, ambient: ambientState})
		}
	}

	// want is the routing rule, written out independently of the code.
	want := func(m messageKind, r roomKind) Route {
		stops := r.ambient == "active" && (m.text == "stop" || (m.addressed && strings.Contains(m.text, "stop")))
		switch {
		case m.addressed && stops:
			return RouteNone
		case m.addressed && m.command == "ping":
			return RouteCommand
		case m.addressed:
			return RouteConversation
		case r.tracked:
			return RouteFollowUp
		case r.ambient == "active" && !stops:
			return RouteAmbient
		default:
			return RouteNone
		}
	}

	for _, m := range messages {
		for _, r := range rooms {
			t.Run(m.name+"/"+r.name, func(t *testing.T) {
				// Given
				d := newDesk("first reply", "second reply")
				if r.ambient != "off" {
					d.withAmbient()
				}
				if r.ambient == "active" {
					d.startSession(t)
				}
				if r.tracked {
					d.trackRoom()
				}
				in := roomMessage(m.text)
				if m.addressed {
					in = addressed(m.text, m.command)
				}

				// When
				got := d.dispatch(in)

				// Then
				if expected := want(m, r); got != expected {
					t.Fatalf("expected route %v, got %v", expected, got)
				}
				modelCalls, commandRuns := len(d.llm.requests), len(d.commandRuns)
				if modelCalls+commandRuns > 1 {
					t.Fatalf("expected at most one handler to do work, got %d model calls and commands %v", modelCalls, d.commandRuns)
				}
				if d.answers() > 1 {
					t.Fatalf("expected at most one answer, got replies=%#v said=%#v", d.responder.replies, d.responder.said)
				}
				switch got {
				case RouteNone:
					if modelCalls+commandRuns+d.answers()+d.responder.acknowledged != 0 {
						t.Fatalf("expected silence, got %d model calls, commands %v, %d answers, %d acknowledgements", modelCalls, d.commandRuns, d.answers(), d.responder.acknowledged)
					}
				case RouteCommand:
					if commandRuns != 1 || modelCalls != 0 || d.answers() != 0 {
						t.Fatalf("expected only the command to run, got commands %v, %d model calls, %d answers", d.commandRuns, modelCalls, d.answers())
					}
				case RouteFollowUp:
					if modelCalls != 1 || commandRuns != 0 || len(d.history.data["room-1"]) != 3 {
						t.Fatalf("expected only the session to answer, got %d model calls, commands %v, session %#v", modelCalls, d.commandRuns, d.history.data["room-1"])
					}
				case RouteConversation, RouteAmbient:
					if commandRuns != 0 {
						t.Fatalf("expected no command to run, got %v", d.commandRuns)
					}
					if r.tracked && len(d.history.data["room-1"]) != 1 {
						t.Fatalf("expected the session to be left alone, got %#v", d.history.data["room-1"])
					}
				}
			})
		}
	}
}

func TestDispatch_answers_once_when_the_bot_is_addressed_in_a_session_room(t *testing.T) {
	// Given
	d := newDesk("Calico too, bro.", "must not be used")
	d.trackRoom()

	// When
	route := d.dispatch(addressed("<@bot-1> and calico?", "and"))

	// Then
	if route != RouteConversation {
		t.Fatalf("expected the addressed conversation to take the message, got %v", route)
	}
	if !reflect.DeepEqual(d.responder.replies, []string{"Calico too, bro."}) || len(d.llm.requests) != 1 {
		t.Fatalf("expected one answer from one model call, got replies=%#v calls=%d", d.responder.replies, len(d.llm.requests))
	}
	if len(d.history.data["room-1"]) != 1 {
		t.Fatalf("expected the session history to be untouched, got %#v", d.history.data["room-1"])
	}
}

func TestDispatch_answers_once_when_a_session_room_also_has_an_ambient_session(t *testing.T) {
	// Given
	d := newDesk("Cilium rocks, dude.", "must not be used").withAmbient()
	d.startSession(t)
	d.trackRoom()
	before := d.sessions.states[ambient.Key("room-1")]

	// When
	route := d.dispatch(roomMessage("what about cilium?"))

	// Then
	if route != RouteFollowUp {
		t.Fatalf("expected the session to take the message, got %v", route)
	}
	if len(d.llm.requests) != 1 || d.answers() != 1 {
		t.Fatalf("expected one answer from one model call, got %d answers from %d calls", d.answers(), len(d.llm.requests))
	}
	if after := d.sessions.states[ambient.Key("room-1")]; !reflect.DeepEqual(after, before) {
		t.Fatalf("expected the ambient session to be left alone, got %#v", after)
	}
}

func TestHandleAddressed_acknowledges_then_runs_the_command(t *testing.T) {
	// Given
	d := newDesk()

	// When
	route := d.HandleAddressed(context.Background(), addressed("<@bot-1> ping", "ping"), d.describer, d.responder)

	// Then
	if route != RouteCommand {
		t.Fatalf("expected the command route, got %v", route)
	}
	if !reflect.DeepEqual(d.journal.events, []string{"acknowledge", "command ping"}) {
		t.Fatalf("expected an acknowledgement, then the command, got %#v", d.journal.events)
	}
}

func TestHandleAddressed_runs_the_first_command_registered_under_the_word(t *testing.T) {
	// Given
	d := newDesk()
	d.Commands = append(d.Commands, Command{Name: "ping", Run: func(context.Context, Inbound, Responder) {
		d.commandRuns = append(d.commandRuns, "second ping")
	}})

	// When
	d.HandleAddressed(context.Background(), addressed("<@bot-1> ping", "ping"), d.describer, d.responder)

	// Then
	if !reflect.DeepEqual(d.commandRuns, []string{"ping"}) {
		t.Fatalf("expected only the first ping to run, got %#v", d.commandRuns)
	}
}

func TestHandleAddressed_treats_hey_as_conversation_even_when_a_command_has_that_name(t *testing.T) {
	// Given
	d := newDesk("Sup, bro.")

	// When
	route := d.HandleAddressed(context.Background(), addressed("<@bot-1> hey ping", "hey"), d.describer, d.responder)

	// Then
	if route != RouteConversation || len(d.commandRuns) != 0 {
		t.Fatalf("expected conversation and no command, got %v and %#v", route, d.commandRuns)
	}
	if !reflect.DeepEqual(d.describer.said, []string{"ping"}) {
		t.Fatalf("expected the conversation to be about what follows hey, got %#v", d.describer.said)
	}
}

func TestHandleAddressed_acknowledges_then_answers_in_conversation(t *testing.T) {
	// Given
	d := newDesk("I'm PC, Texas A&M!")

	// When
	route := d.HandleAddressed(context.Background(), addressed("<@bot-1> prove you're pc", "prove"), d.describer, d.responder)

	// Then
	if route != RouteConversation {
		t.Fatalf("expected the conversation route, got %v", route)
	}
	if d.journal.events[0] != "acknowledge" || d.responder.acknowledged != 1 {
		t.Fatalf("expected one acknowledgement before anything else, got %#v", d.journal.events)
	}
	if !reflect.DeepEqual(d.responder.replies, []string{"I'm PC, Texas A&M!"}) {
		t.Fatalf("expected the reply, got %#v", d.responder.replies)
	}
}

func TestHandleAddressed_says_the_model_is_down_when_the_conversation_fails(t *testing.T) {
	// Given
	d := newDesk()
	d.llm.err = errors.New("model down")

	// When
	route := d.HandleAddressed(context.Background(), addressed("<@bot-1> hello", "hello"), d.describer, d.responder)

	// Then
	if route != RouteConversation {
		t.Fatalf("expected the conversation route, got %v", route)
	}
	if !reflect.DeepEqual(d.responder.said, []string{"Bro, LiteLLM is not cooperating right now. Totally unacceptable."}) || len(d.responder.replies) != 0 {
		t.Fatalf("expected the failure line only, got said=%#v replies=%#v", d.responder.said, d.responder.replies)
	}
}

func TestHandleAddressed_stop_ends_the_ambient_session_without_an_answer(t *testing.T) {
	// Given
	d := newDesk("must not be used").withAmbient()
	d.startSession(t)

	// When
	route := d.HandleAddressed(context.Background(), addressed("<@bot-1> stop", "stop"), d.describer, d.responder)

	// Then
	if route != RouteNone {
		t.Fatalf("expected no route, got %v", route)
	}
	if len(d.journal.events) != 0 {
		t.Fatalf("expected no acknowledgement and no answer, got %#v", d.journal.events)
	}
	if _, ok := d.sessions.states[ambient.Key("room-1")]; ok {
		t.Fatal("expected the ambient session to be gone")
	}
}

func TestHandleAddressed_goes_on_when_the_ambient_state_cannot_be_read(t *testing.T) {
	// Given
	d := newDesk().withAmbient()
	d.sessions.err = errors.New("store down")

	// When
	route := d.HandleAddressed(context.Background(), addressed("<@bot-1> ping", "ping"), d.describer, d.responder)

	// Then
	if route != RouteCommand || len(d.commandRuns) != 1 {
		t.Fatalf("expected the command to run anyway, got %v and %#v", route, d.commandRuns)
	}
}

func TestAddressedConversation_stays_silent_when_the_address_is_not_in_the_text(t *testing.T) {
	// Given
	d := newDesk("must not be used").withAmbient()

	// When
	err := d.AddressedConversation(context.Background(), addressed("tell me more", "me"), d.describer, d.responder)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(d.journal.events) != 0 || len(d.sessions.states) != 0 {
		t.Fatalf("expected nothing to happen, got %#v and sessions %#v", d.journal.events, d.sessions.states)
	}
}

func TestAddressedConversation_asks_for_more_when_the_address_is_all_there_is(t *testing.T) {
	// Given
	d := newDesk("must not be used").withAmbient()

	// When
	err := d.AddressedConversation(context.Background(), addressed("<@bot-1> hey", "hey"), d.describer, d.responder)

	// Then
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !reflect.DeepEqual(d.responder.replies, []string{"Bro, I'm here. Say what you need after the mention."}) || len(d.llm.requests) != 0 {
		t.Fatalf("expected the fallback line without a model call, got %#v", d.responder.replies)
	}
	if len(d.sessions.states) != 0 {
		t.Fatalf("expected no ambient session, got %#v", d.sessions.states)
	}
}

func TestAddressedConversation_starts_the_ambient_session_only_after_an_answer(t *testing.T) {
	// Given
	answered := newDesk("Sweet.").withAmbient()
	failed := newDesk().withAmbient()
	failed.llm.err = errors.New("model down")
	ctx := context.Background()

	// When
	answeredErr := answered.AddressedConversation(ctx, addressed("<@bot-1> are you pc?", "are"), answered.describer, answered.responder)
	failedErr := failed.AddressedConversation(ctx, addressed("<@bot-1> are you pc?", "are"), failed.describer, failed.responder)

	// Then
	if answeredErr != nil || failedErr == nil {
		t.Fatalf("expected one answer and one failure, got %v and %v", answeredErr, failedErr)
	}
	session, ok := answered.sessions.states[ambient.Key("room-1")]
	if !ok || session.ActiveUserID != "user-1" || session.ChannelID != "room-1" || session.GuildID != "space-1" {
		t.Fatalf("expected a session for the sender in the room, got %#v", answered.sessions.states)
	}
	if len(failed.sessions.states) != 0 {
		t.Fatalf("expected no session after a failed answer, got %#v", failed.sessions.states)
	}
}

func TestHandleAmbient_answers_the_session_owner_and_counts_the_reply(t *testing.T) {
	// Given
	d := newDesk("Sweet, bro.").withAmbient()
	d.startSession(t)

	// When
	route := d.HandleAmbient(context.Background(), roomMessage("  I am also PC  "), d.describer, d.responder)

	// Then
	if route != RouteAmbient {
		t.Fatalf("expected the ambient route, got %v", route)
	}
	if !reflect.DeepEqual(d.describer.said, []string{"I am also PC"}) || !reflect.DeepEqual(d.responder.replies, []string{"Sweet, bro."}) {
		t.Fatalf("expected the trimmed message answered, got said=%#v replies=%#v", d.describer.said, d.responder.replies)
	}
	if d.responder.acknowledged != 0 {
		t.Fatalf("expected no acknowledgement for an ambient reply, got %d", d.responder.acknowledged)
	}
	session := d.sessions.states[ambient.Key("room-1")]
	if session.UserTurns != 1 || session.ConsecutiveBotReplies != 1 {
		t.Fatalf("expected the reply to be counted, got %#v", session)
	}
}

func TestHandleAmbient_answers_someone_else_only_when_they_reply_to_the_bot(t *testing.T) {
	// Given
	d := newDesk("Casey gets it.").withAmbient()
	d.startSession(t)
	stranger := roomMessage("me too")
	stranger.SenderID = "user-3"
	replying := stranger
	replying.ReplyTo = &Reference{MessageID: "bot-message-1", SenderID: "bot-1", FromBot: true}
	ctx := context.Background()

	// When
	strangerRoute := d.HandleAmbient(ctx, stranger, d.describer, d.responder)
	replyingRoute := d.HandleAmbient(ctx, replying, d.describer, d.responder)

	// Then
	if strangerRoute != RouteNone || replyingRoute != RouteAmbient {
		t.Fatalf("expected silence then an ambient reply, got %v then %v", strangerRoute, replyingRoute)
	}
}

func TestHandleAmbient_is_off_without_a_manager_and_for_addressed_messages(t *testing.T) {
	// Given
	off := newDesk("must not be used")
	on := newDesk("must not be used").withAmbient()
	on.startSession(t)
	ctx := context.Background()

	// When
	offRoute := off.HandleAmbient(ctx, roomMessage("hello"), off.describer, off.responder)
	addressedRoute := on.HandleAmbient(ctx, addressed("<@bot-1> hello", "hello"), on.describer, on.responder)

	// Then
	if offRoute != RouteNone || addressedRoute != RouteNone {
		t.Fatalf("expected no ambient route, got %v and %v", offRoute, addressedRoute)
	}
	if len(off.journal.events)+len(on.journal.events) != 0 {
		t.Fatalf("expected nothing to happen, got %#v and %#v", off.journal.events, on.journal.events)
	}
}

func TestHandleAmbient_says_the_model_is_down_and_does_not_count_the_reply(t *testing.T) {
	// Given
	d := newDesk().withAmbient()
	d.startSession(t)
	d.llm.err = errors.New("model down")

	// When
	route := d.HandleAmbient(context.Background(), roomMessage("you good?"), d.describer, d.responder)

	// Then
	if route != RouteAmbient {
		t.Fatalf("expected the ambient route, got %v", route)
	}
	if !reflect.DeepEqual(d.responder.said, []string{"Bro, LiteLLM is not cooperating right now. Totally unacceptable."}) {
		t.Fatalf("expected the failure line, got %#v", d.responder.said)
	}
	if session := d.sessions.states[ambient.Key("room-1")]; session.UserTurns != 0 {
		t.Fatalf("expected the failed reply not to be counted, got %#v", session)
	}
}

func TestHandleAmbient_stays_silent_when_the_session_state_cannot_be_read(t *testing.T) {
	// Given
	d := newDesk("must not be used").withAmbient()
	d.sessions.err = errors.New("store down")

	// When
	route := d.HandleAmbient(context.Background(), roomMessage("hello"), d.describer, d.responder)

	// Then
	if route != RouteNone || len(d.journal.events) != 0 {
		t.Fatalf("expected silence, got %v and %#v", route, d.journal.events)
	}
}

func TestRoute_names(t *testing.T) {
	for route, want := range map[Route]string{RouteNone: "none", RouteCommand: "command", RouteConversation: "conversation", RouteFollowUp: "follow-up", RouteAmbient: "ambient", RouteButton: "button", Route(42): "Route(42)"} {
		if got := route.String(); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	}
}
