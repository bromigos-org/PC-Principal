package brodec

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

// startPoll runs the fixture's /poll command and returns the poll's card.
func startPoll(t *testing.T, h *harness) *Card {
	t.Helper()
	inline := h.deliver(fixture(t, "delivery/command.json"))
	if len(inline) != 1 || inline[0].Type != "card" {
		t.Fatalf("expected the poll's card, got %v", inline)
	}
	return inline[0].Card
}

// tap is a tap on a button of a poll's card by one person. Every tap is an
// interaction of its own.
func tap(t *testing.T, buttonID string, sender string) []byte {
	t.Helper()
	tapCount++
	return edited(t, "delivery/button.json", map[string]any{
		"button_id":      buttonID,
		"sender":         sender,
		"interaction_id": fmt.Sprintf("tap-%d", tapCount),
		"event_id":       "$poll-card",
	})
}

var tapCount int

func voter(n int) string { return fmt.Sprintf("@%024x:brodec.ai", n) }

// lastRedraw is the card as the bot last redrew it for everyone.
func lastRedraw(t *testing.T, h *harness) (localCall, *Card) {
	t.Helper()
	calls := h.gateway.localCalls()
	if len(calls) == 0 {
		t.Fatal("the card was never redrawn")
	}
	last := calls[len(calls)-1]
	if len(last.Actions) != 1 || last.Actions[0].Type != "edit_card" {
		t.Fatalf("expected an edit of the card, got %v", last.Actions)
	}
	return last, last.Actions[0].Card
}

func counts(card *Card) []string {
	out := make([]string, 0, len(card.Fields))
	for _, field := range card.Fields {
		out = append(out, fmt.Sprintf("%s=%s (%d%%)", field.Label, field.Value, *field.Bar))
	}
	return out
}

func TestPoll_counts_a_vote_privately_and_redraws_the_card_for_everyone(t *testing.T) {
	// Given
	h := newHarness(t)
	card := startPoll(t, h)
	arrival := card.Rows[0][0].ID

	// When
	inline := h.deliver(tap(t, arrival, voter(1)))

	// Then: the person who tapped is told, and only they
	if want := []Action{{Type: "answer", Text: "Your vote: ARRIVAL"}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
	// and the card is redrawn through the local API: the tapped card, by
	// its event, sent with the delivery of the /poll command
	call, redrawn := lastRedraw(t, h)
	if string(call.Event) != string(fixture(t, "delivery/command.json")) {
		t.Fatalf("expected the redraw to go with the /poll command's delivery, got %s", call.Event)
	}
	if call.Actions[0].EventID != "$poll-card" {
		t.Fatalf("expected the redraw to name the tapped card, got %q", call.Actions[0].EventID)
	}
	if got, want := counts(redrawn), []string{"ARRIVAL=1 (100%)", "HEAT=0 (0%)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if redrawn.Footer != "1 vote · nobody sees who voted for what" {
		t.Fatalf("unexpected footer %q", redrawn.Footer)
	}
	if h.llm.calls() != 0 {
		t.Fatal("a poll must not call the model")
	}
}

func TestPoll_gives_each_person_one_vote_which_they_can_change(t *testing.T) {
	// Given
	h, _, _ := newUnpacedHarness(t)
	card := startPoll(t, h)
	arrival, heat := card.Rows[0][0].ID, card.Rows[0][1].ID

	// When: three people vote, one of them three times
	first := h.deliver(tap(t, arrival, voter(1)))
	same := h.deliver(tap(t, arrival, voter(1)))
	changed := h.deliver(tap(t, heat, voter(1)))
	h.deliver(tap(t, arrival, voter(2)))
	h.deliver(tap(t, arrival, voter(3)))

	// Then
	for i, c := range []struct {
		inline []Action
		want   string
	}{{first, "Your vote: ARRIVAL"}, {same, "Your vote is already ARRIVAL."}, {changed, "Changed your vote to HEAT."}} {
		if len(c.inline) != 1 || c.inline[0].Type != "answer" || c.inline[0].Text != c.want {
			t.Fatalf("tap %d: expected the private answer %q, got %v", i+1, c.want, c.inline)
		}
	}
	_, redrawn := lastRedraw(t, h)
	if got, want := counts(redrawn), []string{"ARRIVAL=2 (67%)", "HEAT=1 (33%)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	if redrawn.Footer != "3 votes · nobody sees who voted for what" {
		t.Fatalf("unexpected footer %q", redrawn.Footer)
	}
}

func TestPoll_shows_nobody_who_voted_for_what(t *testing.T) {
	// Given
	h, _, _ := newUnpacedHarness(t)
	card := startPoll(t, h)
	voters := []string{voter(1), voter(2), voter(3)}

	// When
	var inline []Action
	for i, who := range voters {
		inline = append(inline, h.deliver(tap(t, card.Rows[0][i%2].ID, who))...)
	}

	// Then: no voter is named in anything sent into the room
	for _, action := range h.gateway.actions() {
		sent := mustEncode(t, action)
		for _, who := range voters {
			if strings.Contains(sent, who) {
				t.Fatalf("a card names a voter: %s", sent)
			}
		}
		for _, row := range action.Card.Rows {
			for _, button := range row {
				if button.Selected {
					t.Fatal("the card everyone sees must not mark anyone's choice")
				}
			}
		}
	}
	// nor in a private answer to someone else
	for _, action := range inline {
		for _, who := range voters {
			if strings.Contains(action.Text, who) {
				t.Fatalf("a private answer names a voter: %q", action.Text)
			}
		}
	}
	// and the votes are kept under markers, not under anyone's ID
	for _, key := range h.keys() {
		if !strings.HasSuffix(key, ":votes") {
			continue
		}
		markers, err := h.redis.HKeys(key)
		if err != nil {
			t.Fatal(err)
		}
		if len(markers) != len(voters) {
			t.Fatalf("expected a marker per voter, got %v", markers)
		}
		for _, marker := range markers {
			for _, who := range voters {
				if strings.Contains(marker, strings.TrimSuffix(strings.TrimPrefix(who, "@"), ":brodec.ai")) || strings.Contains(marker, "@") {
					t.Fatalf("a vote is kept under a voter's ID: %q", marker)
				}
			}
		}
	}
}

func TestPoll_survives_a_restart(t *testing.T) {
	// Given: a poll with two votes
	h, _, _ := newUnpacedHarness(t)
	card := startPoll(t, h)
	arrival, heat := card.Rows[0][0].ID, card.Rows[0][1].ID
	h.deliver(tap(t, arrival, voter(1)))
	h.deliver(tap(t, heat, voter(2)))

	// When: the bot is restarted, then a new vote and a changed one come in
	h.start()
	afterRestart := h.deliver(tap(t, arrival, voter(3)))
	changed := h.deliver(tap(t, arrival, voter(2)))

	// Then: the votes from before the restart are still counted, and still
	// belong to the people who cast them
	if want := []Action{{Type: "answer", Text: "Your vote: ARRIVAL"}}; !reflect.DeepEqual(afterRestart, want) {
		t.Fatalf("expected %v, got %v", want, afterRestart)
	}
	if want := []Action{{Type: "answer", Text: "Changed your vote to ARRIVAL."}}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("expected %v, got %v", want, changed)
	}
	_, redrawn := lastRedraw(t, h)
	if got, want := counts(redrawn), []string{"ARRIVAL=3 (100%)", "HEAT=0 (0%)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestPoll_and_its_votes_are_erased_after_seven_days(t *testing.T) {
	// Given
	h := newHarness(t)
	card := startPoll(t, h)
	h.deliver(tap(t, card.Rows[0][0].ID, voter(1)))
	if len(h.keys()) != 2 {
		t.Fatalf("expected the poll and its votes in the store, got %v", h.keys())
	}
	for _, key := range h.keys() {
		if ttl := h.redis.TTL(key); ttl <= 0 || ttl > pollLifetime {
			t.Fatalf("expected %s to expire within the poll's lifetime, got %s", key, ttl)
		}
	}

	// When
	h.redis.FastForward(pollLifetime + time.Minute)
	inline := h.deliver(tap(t, card.Rows[0][0].ID, voter(2)))

	// Then
	if len(h.keys()) != 0 {
		t.Fatalf("expected nothing left of the poll, got %v", h.keys())
	}
	if want := []Action{{Type: "fail", Text: pollOverLine}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
}

func TestPoll_takes_votes_only_in_its_own_room(t *testing.T) {
	// Given: a poll in the group, and a tap that names it from elsewhere
	h := newHarness(t, withConfig(func(c *Config) { c.AllowedRooms = append(c.AllowedRooms, strangersRoom) }))
	card := startPoll(t, h)
	elsewhere := tap(t, card.Rows[0][0].ID, voter(1))
	var fields map[string]any
	if err := json.Unmarshal(elsewhere, &fields); err != nil {
		t.Fatal(err)
	}
	fields["room_id"] = strangersRoom
	elsewhere, _ = json.Marshal(fields)

	// When
	inline := h.deliver(elsewhere)

	// Then
	if want := []Action{{Type: "fail", Text: pollOverLine}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
	if len(h.gateway.localCalls()) != 0 {
		t.Fatal("a vote from another room must not redraw the card")
	}
}

func TestPoll_refuses_a_tap_on_a_choice_it_does_not_have(t *testing.T) {
	// Given
	h := newHarness(t)
	card := startPoll(t, h)
	id, _, _ := parsePollButton(card.Rows[0][0].ID)

	for name, buttonID := range map[string]string{
		"a choice past the last one": "poll:" + id + ":7",
		"a choice that is no number": "poll:" + id + ":first",
		"a negative choice":          "poll:" + id + ":-1",
		"no choice":                  "poll:" + id,
		"a poll that never was":      "poll:0000000000000000:0",
		"an id that is no id":        "poll:" + id + ":votes:0",
		"an id of another length":    "poll:abc:0",
	} {
		t.Run(name, func(t *testing.T) {
			// When
			inline := h.deliver(tap(t, buttonID, voter(1)))

			// Then
			if len(inline) != 1 || inline[0].Type != "fail" {
				t.Fatalf("expected the tap to be refused privately, got %v", inline)
			}
		})
	}
	if len(h.gateway.localCalls()) != 0 {
		t.Fatal("a refused tap must not redraw the card")
	}
}

func TestPoll_asks_for_a_question_and_two_to_ten_choices(t *testing.T) {
	eleven := "Pick | " + strings.Join(strings.Split("a b c d e f g h i j k", " "), " | ")
	cases := map[string]struct{ args, want string }{
		"nothing":         {"", pollUsageLine},
		"a question only": {"Movie tonight?", pollUsageLine},
		"one choice":      {"Movie tonight? | ARRIVAL", pollUsageLine},
		"empty choices":   {"Movie tonight? |  | ", pollUsageLine},
		"no question":     {" | ARRIVAL | HEAT", pollUsageLine},
		"eleven choices":  {eleven, pollTooManyLine},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// Given
			h := newHarness(t)

			// When
			inline := h.deliver(edited(t, "delivery/command.json", map[string]any{"args": c.args}))

			// Then
			if want := []Action{{Type: "reply", Text: c.want}}; !reflect.DeepEqual(inline, want) {
				t.Fatalf("expected %v, got %v", want, inline)
			}
			if len(h.keys()) != 0 {
				t.Fatalf("a poll that was not started must keep nothing, got %v", h.keys())
			}
		})
	}
}

func TestPoll_with_ten_long_choices_fits_the_card(t *testing.T) {
	// Given: the most choices a poll takes, each longer than a button holds
	h := newHarness(t)
	choices := make([]string, 0, pollChoicesMax)
	for i := range pollChoicesMax {
		choices = append(choices, fmt.Sprintf("%d %s", i, strings.Repeat("é", 60)))
	}
	args := strings.Repeat("Why? ", 100) + " | " + strings.Join(choices, " | ")

	// When
	inline := h.deliver(edited(t, "delivery/command.json", map[string]any{"args": args}))

	// Then
	if len(inline) != 1 || inline[0].Type != "card" {
		t.Fatalf("expected the poll's card, got %v", inline)
	}
	card := inline[0].Card
	if !reflect.DeepEqual(*card, card.clamped()) {
		t.Fatal("the poll's card must be within the platform's limits as sent")
	}
	if len(card.Fields) != 10 || len(card.Rows) != 2 || len(card.Rows[0]) != 5 || len(card.Rows[1]) != 5 {
		t.Fatalf("expected ten bars and two rows of five buttons, got %d fields and rows %v", len(card.Fields), card.Rows)
	}
	seen := map[string]bool{}
	for _, row := range card.Rows {
		for _, button := range row {
			if seen[button.ID] {
				t.Fatalf("two buttons share the id %q", button.ID)
			}
			seen[button.ID] = true
		}
	}
}

func TestPoll_says_so_when_the_store_is_down(t *testing.T) {
	// Given
	h := newHarness(t)
	h.redis.Close()

	// When
	inline := h.deliver(fixture(t, "delivery/command.json"))

	// Then
	if want := []Action{{Type: "reply", Text: pollStoreLine}}; !reflect.DeepEqual(inline, want) {
		t.Fatalf("expected %v, got %v", want, inline)
	}
}

// A rush of votes must not become a rush of edits: the room allows twenty
// sends a minute. Every vote is still on the card in the end.
func TestPoll_under_a_rush_of_votes_redraws_within_the_room_limits(t *testing.T) {
	// Given
	h, clock, _ := newUnpacedHarness(t)
	card := startPoll(t, h)
	started := clock.Now()

	// When: sixty people vote at once
	const voters = 60
	done := make(chan struct{})
	for i := range voters {
		body := tap(t, card.Rows[0][i%2].ID, voter(i+1))
		go func() {
			defer func() { done <- struct{}{} }()
			recorder := newRecorder()
			h.bot.ServeHTTP(recorder, h.request(body))
		}()
	}
	for range voters {
		<-done
	}
	h.bot.drain()

	// Then: the last redraw shows every vote
	_, redrawn := lastRedraw(t, h)
	if got, want := counts(redrawn), []string{"ARRIVAL=30 (50%)", "HEAT=30 (50%)"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	// and the redraws, with the card itself, stayed within twenty a minute
	redraws := len(h.gateway.localCalls())
	minutes := int(clock.Now().Sub(started)/time.Minute) + 1
	if redraws+1 > roomPerMinute*minutes {
		t.Fatalf("%d sends in %d minute(s) is over the room's limit of %d a minute", redraws+1, minutes, roomPerMinute)
	}
}
