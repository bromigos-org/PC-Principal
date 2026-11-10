package brodec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bromigos-org/pc-principal/internal/core"
)

// The kinds of delivery the gateway makes.
const (
	deliveryPing    = "ping"
	deliveryCommand = "command"
	deliveryMention = "mention"
	deliveryMessage = "message"
	deliveryButton  = "button"
)

// Delivery is one event as the gateway posts it. Which fields are set
// depends on Type.
type Delivery struct {
	Type          string `json:"type"`
	RoomID        string `json:"room_id"`
	EventID       string `json:"event_id"`
	Sender        string `json:"sender"`
	Name          string `json:"name"`
	Args          string `json:"args"`
	Body          string `json:"body"`
	IsGroup       bool   `json:"is_group"`
	Quoted        string `json:"quoted"`
	InteractionID string `json:"interaction_id"`
	ButtonID      string `json:"button_id"`
	SenderDevice  string `json:"sender_device"`

	// raw is the delivery exactly as it arrived. The gateway's local API
	// wants it back, unchanged, with every action sent later.
	raw json.RawMessage
}

// parseDelivery reads one delivery and keeps its bytes.
func parseDelivery(body []byte) (Delivery, error) {
	var d Delivery
	if err := json.Unmarshal(body, &d); err != nil {
		return Delivery{}, fmt.Errorf("decode delivery: %w", err)
	}
	if d.Type == "" {
		return Delivery{}, fmt.Errorf("decode delivery: no type")
	}
	d.raw = append(json.RawMessage(nil), bytes.TrimSpace(body)...)
	return d, nil
}

// isTap reports whether the delivery is a tap on a button, the one kind
// that can be answered privately.
func (d Delivery) isTap() bool { return d.Type == deliveryButton }

// dedupeID identifies the delivery among the ones recently handled.
func (d Delivery) dedupeID() string {
	if d.isTap() {
		return "tap:" + d.InteractionID
	}
	return d.Type + ":" + d.RoomID + ":" + d.EventID
}

// The kinds of action the gateway carries out.
const (
	actionReply    = "reply"
	actionSay      = "say"
	actionCard     = "card"
	actionEditCard = "edit_card"
	actionAnswer   = "answer"
	actionDefer    = "defer"
	actionFail     = "fail"
)

// Action is one thing the bot asks the gateway to do. The gateway also
// knows a "projection" action; this front end never sends one.
type Action struct {
	Type string `json:"type"`
	// EventID names the card an edit_card replaces. Empty means the card
	// that was tapped.
	EventID string `json:"event_id,omitempty"`
	Text    string `json:"text,omitempty"`
	Card    *Card  `json:"card,omitempty"`
}

// sendsToRoom reports whether the action puts an event into the room, which
// is what the room limits count. An answer to a tap goes to one device.
func (a Action) sendsToRoom() bool {
	switch a.Type {
	case actionReply, actionSay, actionCard, actionEditCard:
		return true
	default:
		return false
	}
}

// complete reports whether the action has what the gateway needs to read
// it. The gateway reads an answer as a whole: one action it cannot read
// costs every action sent with it.
func (a Action) complete() bool {
	switch a.Type {
	case actionReply, actionSay, actionFail:
		return a.Text != ""
	case actionCard, actionEditCard:
		return a.Card != nil
	case actionAnswer, actionDefer:
		return true
	default:
		return false
	}
}

// answer is the body the endpoint answers a delivery with.
type answer struct {
	Actions []Action `json:"actions"`
}

// localActions is the body of the gateway's local API: actions for an event
// it delivered earlier.
type localActions struct {
	Event   json.RawMessage `json:"event"`
	Actions []Action        `json:"actions"`
}

// encodeJSON is json.Marshal without the escaping of <, > and & that only
// matters inside HTML, so that what is sent reads like the gateway's own
// JSON.
func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// The platform's limits. Apps clamp a card to them when they draw it; the
// bot keeps to them before it sends, so nothing it sends is ever cut by
// someone else. Text limits count characters, a button id counts bytes.
const (
	cardSchema         = 1
	cardTitleMax       = 80
	cardTextMax        = 2000
	cardFieldsMax      = 10
	cardFieldLabelMax  = 40
	cardFieldValueMax  = 80
	cardRowsMax        = 5
	cardRowButtonsMax  = 5
	cardButtonIDMax    = 100
	cardButtonLabelMax = 40
	cardFooterMax      = 200

	// messageTextMax bounds one text message (and a private answer). The
	// platform sets no limit of its own for text below Matrix's event size;
	// this is the card's text limit, so that a message is never longer than
	// what a card could say.
	messageTextMax = 2000
	// replyChunksMax bounds how many messages one answer of the model may
	// be split into. What is left over is dropped.
	replyChunksMax = 4
)

// Card is a card in the gateway's JSON.
type Card struct {
	Schema int         `json:"schema"`
	Title  string      `json:"title,omitempty"`
	Text   string      `json:"text,omitempty"`
	Fields []CardField `json:"fields,omitempty"`
	Rows   [][]Button  `json:"rows,omitempty"`
	Footer string      `json:"footer,omitempty"`
}

// CardField is a label and a value; Bar (0 to 100) draws a bar under it.
type CardField struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Bar   *int   `json:"bar,omitempty"`
}

// Button is a button on a card. A tap on it reaches only the bot.
type Button struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Style    string `json:"style,omitempty"`
	Selected bool   `json:"selected,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// wireCard turns the brain's card into the gateway's, within the limits.
func wireCard(card core.Card) *Card {
	out := Card{Title: card.Title, Text: card.Text, Footer: card.Footer}
	for _, field := range card.Fields {
		out.Fields = append(out.Fields, CardField{Label: field.Label, Value: field.Value, Bar: field.Bar})
	}
	for _, row := range card.Buttons {
		buttons := make([]Button, 0, len(row))
		for _, button := range row {
			buttons = append(buttons, Button{ID: button.ID, Label: button.Label, Style: button.Style})
		}
		out.Rows = append(out.Rows, buttons)
	}
	clamped := out.clamped()
	return &clamped
}

// clamped returns the card within the platform's limits, by the rules the
// apps apply when they draw one: text that is too long is cut, fields, rows
// and buttons beyond the limit are dropped, a button whose id is empty or
// too long is dropped, an unknown style is the plain one, and a row left
// without buttons is dropped.
func (c Card) clamped() Card {
	out := Card{
		Schema: cardSchema,
		Title:  oneLine(clip(c.Title, cardTitleMax)),
		Text:   clip(c.Text, cardTextMax),
		Footer: clip(c.Footer, cardFooterMax),
	}
	for i, field := range c.Fields {
		if i == cardFieldsMax {
			break
		}
		clampedField := CardField{
			Label: oneLine(clip(field.Label, cardFieldLabelMax)),
			Value: oneLine(clip(field.Value, cardFieldValueMax)),
		}
		if field.Bar != nil {
			bar := min(max(*field.Bar, 0), 100)
			clampedField.Bar = &bar
		}
		out.Fields = append(out.Fields, clampedField)
	}
	for _, row := range c.Rows {
		if len(out.Rows) == cardRowsMax {
			break
		}
		buttons := make([]Button, 0, min(len(row), cardRowButtonsMax))
		for _, button := range row {
			if len(buttons) == cardRowButtonsMax {
				break
			}
			if button.ID == "" || len(button.ID) > cardButtonIDMax {
				continue
			}
			style := button.Style
			switch style {
			case "primary", "secondary", "danger":
			default:
				style = ""
			}
			buttons = append(buttons, Button{
				ID:       button.ID,
				Label:    oneLine(clip(button.Label, cardButtonLabelMax)),
				Style:    style,
				Selected: button.Selected,
				Disabled: button.Disabled,
			})
		}
		if len(buttons) > 0 {
			out.Rows = append(out.Rows, buttons)
		}
	}
	return out
}

// clip cuts text to at most limit characters and turns control characters
// other than a line break into spaces.
func clip(text string, limit int) string {
	var builder strings.Builder
	count := 0
	for _, r := range text {
		if count == limit {
			break
		}
		if unicode.IsControl(r) && r != '\n' {
			r = ' '
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String()
}

func oneLine(text string) string { return strings.ReplaceAll(text, "\n", " ") }

// clipBytes cuts text to at most limit bytes, never inside a character.
func clipBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
