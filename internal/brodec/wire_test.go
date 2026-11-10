package brodec

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/bromigos-org/pc-principal/internal/core"
)

// The fixtures under testdata/wire are the gateway's own: its tests write
// every delivery and read every action against the same files. These tests
// hold the Go side to them.

func TestDelivery_reads_every_kind_the_gateway_sends(t *testing.T) {
	cases := map[string]Delivery{
		"delivery/ping.json":    {Type: "ping"},
		"delivery/command.json": {Type: "command", RoomID: fixtureRoom, EventID: "$cmd-1", Sender: fixtureSender, Name: "poll", Args: "Movie tonight? | ARRIVAL | HEAT", IsGroup: true},
		"delivery/mention.json": {Type: "mention", RoomID: fixtureRoom, EventID: "$mention-1", Sender: fixtureSender, Body: "what's the score so far?", IsGroup: true},
		"delivery/mention_quoted.json": {Type: "mention", RoomID: fixtureRoom, EventID: "$mention-2", Sender: fixtureSender, Body: "and with HEAT counted twice?", IsGroup: true,
			Quoted: "ARRIVAL 3 · HEAT 2"},
		"delivery/message.json": {Type: "message", RoomID: fixtureDMRoom, EventID: "$dm-1", Sender: fixtureSender, Body: "remind me at 21:40\n\"HEAT\" · café"},
		"delivery/button.json":  {Type: "button", InteractionID: "0d4c1b8e2a7f4e53", RoomID: fixtureRoom, EventID: "$card-1", ButtonID: "vote:0", Sender: fixtureSender, SenderDevice: "QWERTYUIOP"},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			// Given
			body := fixture(t, name)

			// When
			got, err := parseDelivery(body)

			// Then
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if string(got.raw) != string(body) {
				t.Fatalf("the delivery's bytes were not kept as they arrived:\n%s\n%s", got.raw, body)
			}
			got.raw = nil
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("expected %+v, got %+v", want, got)
			}
		})
	}
}

func TestAction_writes_each_action_exactly_as_the_gateway_does(t *testing.T) {
	// "projection" is the one action of the gateway's that is not here: this
	// front end has no feature that posts one.
	for _, name := range []string{"reply", "say", "card", "edit_card", "edit_card_by_id", "answer", "answer_card", "answer_empty", "defer", "fail"} {
		t.Run(name, func(t *testing.T) {
			// Given
			want := fixture(t, "action/"+name+".json")
			var action Action
			if err := json.Unmarshal(want, &action); err != nil {
				t.Fatalf("decode: %v", err)
			}

			// When
			got := mustEncode(t, action)

			// Then
			if got != string(want) {
				t.Fatalf("expected\n%s\ngot\n%s", want, got)
			}
		})
	}
}

func TestAnswer_has_the_shape_the_gateway_reads(t *testing.T) {
	// Given: the gateway's fixture of an endpoint's answer, without the
	// projection action
	var fixtureAnswer struct {
		Actions []json.RawMessage `json:"actions"`
	}
	if err := json.Unmarshal(fixture(t, "endpoint_answer.json"), &fixtureAnswer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var want []string
	var actions []Action
	for _, raw := range fixtureAnswer.Actions {
		var action Action
		if err := json.Unmarshal(raw, &action); err != nil {
			t.Fatalf("decode action: %v", err)
		}
		if action.Type == "projection" {
			continue
		}
		want = append(want, string(raw))
		actions = append(actions, action)
	}

	// When
	got := mustEncode(t, answer{Actions: actions})

	// Then
	if expected := `{"actions":[` + strings.Join(want, ",") + `]}`; got != expected {
		t.Fatalf("expected\n%s\ngot\n%s", expected, got)
	}
}

func TestCard_is_clamped_to_the_platforms_limits(t *testing.T) {
	// Given: a card that is over every limit at once
	long := strings.Repeat("é", 5000)
	hundred := 100
	tooFull := 250
	negative := -5
	card := core.Card{
		Title:  "TITLE\nON TWO LINES " + long,
		Text:   "line one\nline two\x07 " + long,
		Footer: long,
	}
	for i := range 14 {
		bar := &hundred
		switch i {
		case 0:
			bar = &tooFull
		case 1:
			bar = &negative
		case 2:
			bar = nil
		}
		card.Fields = append(card.Fields, core.CardField{Label: "label\n" + long, Value: "value\t" + long, Bar: bar})
	}
	for range 7 {
		row := []core.CardButton{
			{ID: "", Label: "no id"},
			{ID: strings.Repeat("x", cardButtonIDMax+1), Label: "id too long"},
		}
		for i := range 8 {
			row = append(row, core.CardButton{ID: "ok:" + string(rune('a'+i)), Label: "label\n" + long, Style: []string{core.ButtonPrimary, core.ButtonDanger, "sparkly", ""}[i%4]})
		}
		card.Buttons = append(card.Buttons, row)
	}
	card.Buttons = append(card.Buttons, []core.CardButton{{ID: "", Label: "a row with no usable button"}})

	// When
	got := wireCard(card)

	// Then
	if got.Schema != 1 {
		t.Fatalf("expected schema 1, got %d", got.Schema)
	}
	assertChars(t, "title", got.Title, cardTitleMax)
	assertChars(t, "text", got.Text, cardTextMax)
	assertChars(t, "footer", got.Footer, cardFooterMax)
	if strings.ContainsAny(got.Title, "\n") {
		t.Fatalf("the title must be one line, got %q", got.Title)
	}
	if !strings.HasPrefix(got.Text, "line one\nline two  ") {
		t.Fatalf("the text keeps its line breaks and loses other control characters, got %q", got.Text[:24])
	}
	if len(got.Fields) != cardFieldsMax {
		t.Fatalf("expected %d fields, got %d", cardFieldsMax, len(got.Fields))
	}
	for _, field := range got.Fields {
		assertChars(t, "field label", field.Label, cardFieldLabelMax)
		assertChars(t, "field value", field.Value, cardFieldValueMax)
		if strings.ContainsAny(field.Label+field.Value, "\n\t") {
			t.Fatalf("a field is one line without control characters, got %q / %q", field.Label, field.Value)
		}
		if field.Bar != nil && (*field.Bar < 0 || *field.Bar > 100) {
			t.Fatalf("a bar is 0 to 100, got %d", *field.Bar)
		}
	}
	if *got.Fields[0].Bar != 100 || *got.Fields[1].Bar != 0 || got.Fields[2].Bar != nil {
		t.Fatalf("expected bars 100, 0 and none, got %v %v %v", *got.Fields[0].Bar, *got.Fields[1].Bar, got.Fields[2].Bar)
	}
	if len(got.Rows) != cardRowsMax {
		t.Fatalf("expected %d rows, got %d", cardRowsMax, len(got.Rows))
	}
	for _, row := range got.Rows {
		if len(row) != cardRowButtonsMax {
			t.Fatalf("expected %d buttons in a row, got %d", cardRowButtonsMax, len(row))
		}
		for _, button := range row {
			if button.ID == "" || len(button.ID) > cardButtonIDMax {
				t.Fatalf("a button with a bad id was kept: %q", button.ID)
			}
			assertChars(t, "button label", button.Label, cardButtonLabelMax)
			if button.Style != "" && button.Style != "primary" && button.Style != "danger" {
				t.Fatalf("an unknown style was kept: %q", button.Style)
			}
		}
	}
	if again := got.clamped(); !reflect.DeepEqual(again, *got) {
		t.Fatal("clamping a clamped card must change nothing")
	}
}

func TestCard_within_the_limits_is_sent_as_it_is(t *testing.T) {
	// Given: the card of the gateway's own fixture
	var action Action
	if err := json.Unmarshal(fixture(t, "action/card.json"), &action); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// When
	got := action.Card.clamped()

	// Then
	if !reflect.DeepEqual(got, *action.Card) {
		t.Fatalf("expected the card unchanged, got %+v", got)
	}
}

func assertChars(t *testing.T, what string, text string, limit int) {
	t.Helper()
	if count := utf8.RuneCountInString(text); count > limit {
		t.Fatalf("%s is %d characters, over the limit of %d", what, count, limit)
	}
}
