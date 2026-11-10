package brodec

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/bromigos-org/pc-principal/internal/store"
)

const (
	groupContextKey = "conversation:brodec:room:" + fixtureRoom
	dmContextKey    = "conversation:brodec:dm:" + fixtureDMRoom
)

func (h *harness) storedContext(key string) []store.Message {
	h.t.Helper()
	encoded, err := h.redis.Get(key)
	if err != nil {
		h.t.Fatalf("read %s: %v", key, err)
	}
	var messages []store.Message
	if err := json.Unmarshal([]byte(encoded), &messages); err != nil {
		h.t.Fatalf("decode %s: %v", key, err)
	}
	return messages
}

// ---- every delivery in the gateway's fixtures, and what the bot does ----

func TestMention_is_answered_in_character_through_the_local_api(t *testing.T) {
	// Given
	h := newHarness(t)
	h.llm.reply = "The score is sweet, bro."
	delivery := fixture(t, "delivery/mention.json")

	// When
	inline := h.deliver(delivery)

	// Then: the endpoint answers the gateway at once, with nothing
	if len(inline) != 0 {
		t.Fatalf("expected an empty answer while the model works, got %v", inline)
	}
	// and the reply follows through the local API, with the delivery as it
	// arrived
	calls := h.gateway.localCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one call to the local API, got %d", len(calls))
	}
	if string(calls[0].Event) != string(delivery) {
		t.Fatalf("expected the delivery to go back unchanged:\n%s\n%s", calls[0].Event, delivery)
	}
	if want := []Action{{Type: "reply", Text: "The score is sweet, bro."}}; !reflect.DeepEqual(calls[0].Actions, want) {
		t.Fatalf("expected %v, got %v", want, calls[0].Actions)
	}
	// and the model got the BRODEC persona and what was said, labelled with
	// the sender's ID
	request := h.llm.lastRequest(t)
	want := []store.Message{
		{Role: "system", Content: core.PersonaBRODEC},
		{Role: "user", Content: "[" + fixtureSender + "]: what's the score so far?"},
	}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("expected the model to get\n%v\ngot\n%v", want, request)
	}
}

func TestMention_that_quotes_a_message_shows_it_to_the_model_and_does_not_keep_it(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	h.deliver(fixture(t, "delivery/mention_quoted.json"))

	// Then: the model sees the quoted message with what was said
	request := h.llm.lastRequest(t)
	said := request[len(request)-1].Content
	if !strings.HasPrefix(said, "["+fixtureSender+"]: and with HEAT counted twice?") || !strings.Contains(said, "ARRIVAL 3 · HEAT 2") {
		t.Fatalf("expected the model to get what was said and the quoted message, got %q", said)
	}
	// and the context that is kept holds only what was said and the answer
	kept := h.storedContext(groupContextKey)
	want := []store.Message{
		{Role: "system", Content: core.PersonaBRODEC},
		{Role: "user", Content: "[" + fixtureSender + "]: and with HEAT counted twice?"},
		{Role: "assistant", Content: "Sweet, bro."},
	}
	if !reflect.DeepEqual(kept, want) {
		t.Fatalf("expected the context\n%v\ngot\n%v", want, kept)
	}
}

func TestDirectMessage_is_answered_and_kept_under_its_own_key(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	inline := h.deliver(fixture(t, "delivery/message.json"))

	// Then
	if len(inline) != 0 {
		t.Fatalf("expected an empty answer while the model works, got %v", inline)
	}
	if got, want := h.gateway.actions(), []Action{{Type: "reply", Text: "Sweet, bro."}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	kept := h.storedContext(dmContextKey)
	if got, want := kept[1].Content, "["+fixtureSender+"]: remind me at 21:40\n\"HEAT\" · café"; got != want {
		t.Fatalf("expected the message as it was written, %q, got %q", want, got)
	}
	if keys := h.keys(); !reflect.DeepEqual(keys, []string{dmContextKey}) {
		t.Fatalf("expected only %s in the store, got %v", dmContextKey, keys)
	}
}

func TestCommand_poll_is_answered_inline_with_a_card(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	inline := h.deliver(fixture(t, "delivery/command.json"))

	// Then
	if got := types(inline); !reflect.DeepEqual(got, []string{"card"}) {
		t.Fatalf("expected one card, got %v", got)
	}
	card := inline[0].Card
	if card.Title != "POLL" || card.Text != "Movie tonight?" {
		t.Fatalf("expected the poll's question on the card, got %q / %q", card.Title, card.Text)
	}
	zero := 0
	if want := []CardField{{Label: "ARRIVAL", Value: "0", Bar: &zero}, {Label: "HEAT", Value: "0", Bar: &zero}}; !reflect.DeepEqual(card.Fields, want) {
		t.Fatalf("expected a bar per choice, got %+v", card.Fields)
	}
	if len(card.Rows) != 1 || len(card.Rows[0]) != 2 || card.Rows[0][0].Label != "ARRIVAL" || card.Rows[0][1].Label != "HEAT" {
		t.Fatalf("expected a button per choice, got %+v", card.Rows)
	}
	if h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 {
		t.Fatal("a poll needs neither the model nor the local API")
	}
}

func TestButton_that_belongs_to_no_card_is_answered_privately(t *testing.T) {
	// Given: the fixture's tap, on a button this bot never made
	h := newHarness(t)

	// When
	inline := h.deliver(fixture(t, "delivery/button.json"))

	// Then
	if want := []Action{{Type: "fail", Text: unknownButtonLine}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
	if h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 {
		t.Fatal("a tap must not reach the model or the local API")
	}
}

// ---- the commands ----

func TestCommand_ping_is_answered_inline(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	inline := h.deliver(edited(t, "delivery/command.json", map[string]any{"name": "ping", "args": ""}))

	// Then
	if want := []Action{{Type: "reply", Text: "Pong!"}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
	if h.llm.calls() != 0 {
		t.Fatal("ping must not call the model")
	}
}

func TestCommand_help_posts_a_card_with_the_commands_and_two_buttons(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	inline := h.deliver(edited(t, "delivery/command.json", map[string]any{"name": "help", "args": ""}))

	// Then
	if got := types(inline); !reflect.DeepEqual(got, []string{"card"}) {
		t.Fatalf("expected one card, got %v", got)
	}
	card := inline[0].Card
	var listed []string
	for _, field := range card.Fields {
		listed = append(listed, field.Label)
	}
	if want := []string{"/help", "/ping", "/poll"}; !reflect.DeepEqual(listed, want) {
		t.Fatalf("expected the commands %v, got %v", want, listed)
	}
	wantButtons := [][]Button{{{ID: "help:privacy", Label: "PRIVACY", Style: "primary"}, {ID: "help:sees", Label: "WHAT I SEE"}}}
	if !reflect.DeepEqual(card.Rows, wantButtons) {
		t.Fatalf("expected the buttons %+v, got %+v", wantButtons, card.Rows)
	}
	if !strings.Contains(card.Footer, "2 hours") || !strings.Contains(card.Footer, "PRIVACY") {
		t.Fatalf("expected the footer to point at PRIVACY, got %q", card.Footer)
	}
	if !reflect.DeepEqual(*card, card.clamped()) {
		t.Fatal("the help card must be within the platform's limits as written")
	}
}

func TestHelpButtons_answer_only_the_person_who_tapped(t *testing.T) {
	cases := map[string]string{"help:privacy": "PRIVACY", "help:sees": "WHAT I SEE"}
	for buttonID, heading := range cases {
		t.Run(buttonID, func(t *testing.T) {
			// Given
			h := newHarness(t)

			// When
			inline := h.deliver(edited(t, "delivery/button.json", map[string]any{"button_id": buttonID}))

			// Then: one private answer, and nothing sent into the room
			if got := types(inline); !reflect.DeepEqual(got, []string{"answer"}) {
				t.Fatalf("expected one private answer, got %v", got)
			}
			if !strings.HasPrefix(inline[0].Text, heading) || !strings.HasSuffix(inline[0].Text, "Only you see this.") {
				t.Fatalf("unexpected answer: %q", inline[0].Text)
			}
			if len(inline[0].Text) > messageTextMax {
				t.Fatalf("the answer is %d bytes, over the limit of %d", len(inline[0].Text), messageTextMax)
			}
			if h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 {
				t.Fatal("a help button must not reach the model or the local API")
			}
		})
	}
}

// The privacy text is a promise. Each sentence in it is checked here
// against the constant or the behaviour it describes.
func TestPrivacyText_says_what_the_code_does(t *testing.T) {
	// Given
	text := privacyText("an AI model somewhere I was told about")

	// Then
	for _, claim := range []string{
		"at most 12 messages",
		"erased 2 hours after the last one",
		"erased after 7 days",
		"an AI model somewhere I was told about",
		"no long-term memory",
	} {
		if !strings.Contains(text, claim) {
			t.Fatalf("expected the privacy text to say %q:\n%s", claim, text)
		}
	}
	if contextMessages != 12 || contextLifetime != 2*time.Hour || pollLifetime != 7*24*time.Hour {
		t.Fatal("the limits changed: the claims above must change with them")
	}
}

func TestCommand_the_bot_does_not_have_gets_a_fixed_line(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	inline := h.deliver(edited(t, "delivery/command.json", map[string]any{"name": "launch", "args": "the rockets"}))

	// Then
	if want := []Action{{Type: "reply", Text: unknownCommandLine}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
	if h.llm.calls() != 0 {
		t.Fatal("an unknown command must not be handed to the model")
	}
}

// ---- the conversation ----

func TestConversation_keeps_a_rolling_context_of_twelve_messages_per_room(t *testing.T) {
	// Given: eight exchanges in the group, which is sixteen messages
	h, _, _ := newUnpacedHarness(t)
	for i := range 8 {
		h.deliver(edited(t, "delivery/mention.json", map[string]any{"event_id": "$m-" + string(rune('a'+i)), "body": "message " + string(rune('a'+i))}))
	}

	// When
	kept := h.storedContext(groupContextKey)

	// Then: the persona and the last twelve
	if len(kept) != 1+contextMessages {
		t.Fatalf("expected the persona and %d messages, got %d", contextMessages, len(kept))
	}
	if kept[0].Role != "system" || kept[1].Content != "["+fixtureSender+"]: message c" {
		t.Fatalf("expected the oldest kept message to be the third, got %q", kept[1].Content)
	}
	// and the model never saw more than the persona, the twelve kept and
	// the message being answered
	request := h.llm.lastRequest(t)
	if len(request) != 1+contextMessages+1 {
		t.Fatalf("expected the last request to carry the persona, twelve kept messages and the new one, got %d", len(request))
	}
}

func TestConversation_forgets_a_room_two_hours_after_the_last_message(t *testing.T) {
	// Given
	h := newHarness(t)
	h.deliver(fixture(t, "delivery/mention.json"))
	if ttl := h.redis.TTL(groupContextKey); ttl != 2*time.Hour {
		t.Fatalf("expected the context to live 2 hours, got %s", ttl)
	}

	// When: an hour passes, another message, then two more hours
	h.redis.FastForward(time.Hour)
	h.deliver(edited(t, "delivery/mention.json", map[string]any{"event_id": "$mention-later"}))
	stillThere := h.redis.Exists(groupContextKey)
	h.redis.FastForward(2 * time.Hour)

	// Then
	if !stillThere {
		t.Fatal("a new message must keep the context alive")
	}
	if h.redis.Exists(groupContextKey) {
		t.Fatal("expected the context to be gone two hours after the last message")
	}
	h.deliver(edited(t, "delivery/mention.json", map[string]any{"event_id": "$mention-next-day"}))
	if request := h.llm.lastRequest(t); len(request) != 2 {
		t.Fatalf("expected a fresh conversation after the context expired, got %d messages", len(request))
	}
}

func TestConversation_keeps_rooms_apart(t *testing.T) {
	// Given: a message in the group and one in a direct message
	h := newHarness(t)
	h.deliver(fixture(t, "delivery/mention.json"))

	// When
	h.deliver(fixture(t, "delivery/message.json"))

	// Then: the direct message's request holds nothing from the group
	request := h.llm.lastRequest(t)
	if len(request) != 2 {
		t.Fatalf("expected a fresh conversation in the direct message, got %d messages", len(request))
	}
	keys := h.keys()
	if !reflect.DeepEqual(keys, []string{dmContextKey, groupContextKey}) {
		t.Fatalf("expected one context per room, got %v", keys)
	}
}

func TestConversation_strips_the_bots_mention_from_what_goes_to_the_model(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	h.deliver(edited(t, "delivery/mention.json", map[string]any{"body": "@pc_principal what's the score so far?"}))

	// Then
	request := h.llm.lastRequest(t)
	if got, want := request[1].Content, "["+fixtureSender+"]: what's the score so far?"; got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestMention_with_nothing_after_it_gets_a_fixed_line(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	h.deliver(edited(t, "delivery/mention.json", map[string]any{"body": "@PC Principal"}))

	// Then
	if h.llm.calls() != 0 {
		t.Fatal("a bare mention must not call the model")
	}
	actions := h.gateway.actions()
	if len(actions) != 1 || actions[0].Type != "reply" || actions[0].Text == "" {
		t.Fatalf("expected one fixed reply, got %v", actions)
	}
	if len(h.keys()) != 0 {
		t.Fatalf("a bare mention must keep nothing, got %v", h.keys())
	}
}

func TestConversation_says_so_when_the_model_is_down(t *testing.T) {
	// Given
	h := newHarness(t)
	h.llm.err = errModelDown

	// When
	h.deliver(fixture(t, "delivery/mention.json"))

	// Then
	actions := h.gateway.actions()
	if len(actions) != 1 || actions[0].Type != "say" || actions[0].Text == "" {
		t.Fatalf("expected one line saying so, got %v", actions)
	}
	if len(h.keys()) != 0 {
		t.Fatalf("a failed answer must keep nothing, got %v", h.keys())
	}
}

func TestReply_that_is_too_long_is_split_and_then_cut(t *testing.T) {
	// Given: the model answers with far more than a few messages hold
	h := newHarness(t)
	h.llm.reply = strings.Repeat("é", 20_000)

	// When
	h.deliver(fixture(t, "delivery/mention.json"))

	// Then
	actions := h.gateway.actions()
	if got, want := types(actions), []string{"reply", "say", "say", "say"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for _, action := range actions {
		if len(action.Text) > messageTextMax {
			t.Fatalf("a message of %d bytes is over the limit of %d", len(action.Text), messageTextMax)
		}
		if strings.ContainsRune(action.Text, '\uFFFD') {
			t.Fatal("a message was cut inside a character")
		}
	}
}

// Whether a reply to a message the bot was only handed a copy of always
// goes through is not settled. When the gateway refuses one, the answer is
// said in the room instead of being lost.
func TestReply_the_gateway_refuses_is_said_in_the_room(t *testing.T) {
	// Given
	h := newHarness(t)
	h.gateway.refuseEveryReply()

	// When
	h.deliver(fixture(t, "delivery/mention.json"))

	// Then
	if got, want := h.gateway.actions(), []Action{{Type: "reply", Text: "Sweet, bro."}, {Type: "say", Text: "Sweet, bro."}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestDelivery_made_twice_is_answered_once(t *testing.T) {
	// Given
	h := newHarness(t)
	delivery := fixture(t, "delivery/mention.json")

	// When
	h.deliver(delivery)
	h.deliver(delivery)

	// Then
	if h.llm.calls() != 1 || len(h.gateway.localCalls()) != 1 {
		t.Fatalf("expected one answer, got %d model calls and %d sends", h.llm.calls(), len(h.gateway.localCalls()))
	}
}

func TestDelivery_of_a_kind_the_bot_does_not_know_is_accepted_and_ignored(t *testing.T) {
	// Given
	h := newHarness(t)

	// When
	inline := h.deliver([]byte(`{"type":"reaction","room_id":"` + fixtureRoom + `","event_id":"$r-1","sender":"` + fixtureSender + `","key":"👍"}`))

	// Then
	if len(inline) != 0 || h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 || len(h.keys()) != 0 {
		t.Fatal("an unknown kind of delivery must set nothing off")
	}
}

func TestGroupMessage_that_does_not_address_the_bot_is_dropped(t *testing.T) {
	// Given: the kind of delivery a gateway told to read everything makes
	h := newHarness(t)

	// When
	inline := h.deliver(edited(t, "delivery/message.json", map[string]any{"room_id": fixtureRoom, "is_group": true}))

	// Then
	if len(inline) != 0 || h.llm.calls() != 0 || len(h.gateway.localCalls()) != 0 || len(h.keys()) != 0 {
		t.Fatal("a group message that does not address the bot must be neither answered nor kept")
	}
}

// ---- the slow path of the endpoint ----

func TestResponder_sends_what_comes_after_the_endpoint_answered_through_the_local_api(t *testing.T) {
	// Given: a responder for a tap whose handler is still working when the
	// endpoint has to answer
	h := newHarness(t)
	delivery, err := parseDelivery(fixture(t, "delivery/button.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := newResponder(h.bot.client, h.bot.pacer, delivery)

	// When
	inline := r.seal()
	answerErr := r.Answer(context.Background(), "Counted.")
	secondErr := r.Answer(context.Background(), "Counted again.")

	// Then: the tap is deferred inline, and answered once through the
	// local API, in the shape of the gateway's fixture
	if want := []Action{{Type: "defer"}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
	if answerErr != nil {
		t.Fatalf("answer: %v", answerErr)
	}
	if secondErr == nil {
		t.Fatal("a tap takes one answer")
	}
	calls := h.gateway.localCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one call to the local API, got %d", len(calls))
	}
	if got, want := string(calls[0].raw), string(fixture(t, "local_actions_request.json")); got != want {
		t.Fatalf("expected the request\n%s\ngot\n%s", want, got)
	}
}

func TestResponder_refuses_private_answers_to_anything_but_a_tap(t *testing.T) {
	// Given
	h := newHarness(t)
	delivery, err := parseDelivery(fixture(t, "delivery/command.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := newResponder(h.bot.client, h.bot.pacer, delivery)

	// When
	answerErr := r.Answer(context.Background(), "only you see this")
	editErr := r.Edit(context.Background(), "", core.Card{Title: "POLL"})
	deferErr := r.Defer(context.Background())

	// Then
	if answerErr != core.ErrUnsupported || editErr != core.ErrUnsupported {
		t.Fatalf("expected ErrUnsupported, got %v and %v", answerErr, editErr)
	}
	if deferErr != nil || len(r.seal()) != 0 {
		t.Fatal("deferring anything but a tap does nothing")
	}
}

// The gateway reads an answer as a whole. An action it cannot read, such as
// a reply without text, would cost every action sent with it.
func TestResponder_never_sends_an_action_the_gateway_cannot_read(t *testing.T) {
	// Given
	h := newHarness(t)
	delivery, err := parseDelivery(fixture(t, "delivery/button.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := newResponder(h.bot.client, h.bot.pacer, delivery)

	// When
	sayErr := r.Say(context.Background(), "")
	failErr := r.Fail(context.Background(), "")
	emitErr := r.emit(context.Background(), Action{Type: actionCard})
	unknownErr := r.emit(context.Background(), Action{Type: "projection"})
	replyErr := r.Reply(context.Background(), "   ")
	inline := r.seal()

	// Then: only the blank reply is sent, as the fixed line it becomes; the
	// tap, which got no answer, is deferred
	if sayErr != nil {
		t.Fatalf("saying nothing is not an error, got %v", sayErr)
	}
	if failErr == nil || emitErr == nil || unknownErr == nil {
		t.Fatalf("expected the incomplete actions to be refused, got %v, %v, %v", failErr, emitErr, unknownErr)
	}
	if replyErr != nil {
		t.Fatalf("reply: %v", replyErr)
	}
	for _, action := range inline {
		if !action.complete() {
			t.Fatalf("an incomplete action was sent: %+v", action)
		}
	}
	if got := types(inline); !reflect.DeepEqual(got, []string{"reply", "defer"}) || inline[0].Text != core.BlankReply {
		t.Fatalf("expected the fixed line for a blank reply, then the deferral, got %v", inline)
	}
}

func TestResponder_keeps_to_the_room_limits(t *testing.T) {
	// Given: a clock that only moves when the pacer sleeps
	h, _, slept := newUnpacedHarness(t)
	delivery, err := parseDelivery(fixture(t, "delivery/command.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := newResponder(h.bot.client, h.bot.pacer, delivery)

	// When: a handler says seven lines at once
	inline := h.bot.inline(r, func(ctx context.Context) {
		for i := range 7 {
			if err := r.Say(ctx, "line "+string(rune('1'+i))); err != nil {
				t.Errorf("say: %v", err)
			}
		}
	})
	h.bot.drain()

	// Then: the burst of five goes inline, the rest waits its turn and goes
	// through the local API, in order
	if len(inline) != 5 {
		t.Fatalf("expected the burst of 5 inline, got %d", len(inline))
	}
	later := h.gateway.actions()
	if len(later) != 2 || later[0].Text != "line 6" || later[1].Text != "line 7" {
		t.Fatalf("expected lines 6 and 7 through the local API, got %v", later)
	}
	if slept() < time.Second {
		t.Fatalf("expected the later lines to wait about a second each, waited %s", slept())
	}
}
