package core

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by a Responder for a verb its platform cannot
// perform.
var ErrUnsupported = errors.New("core: the platform does not support this verb")

// Responder is what the brain can do in answer to one Inbound. A front end
// makes one per message, bound to that message and its room.
//
// A platform that cannot perform a verb returns ErrUnsupported; the brain
// never assumes a verb beyond Reply and Say exists.
type Responder interface {
	// Acknowledge shows the sender, before any answer is ready, that the
	// message was taken up (on Discord, the :PC: reaction).
	Acknowledge(ctx context.Context) error
	// Reply answers the message with text the model or the brain produced.
	// The front end fits the text to the platform (length, splitting).
	Reply(ctx context.Context, text string) error
	// Say posts a fixed line in the room, as is.
	Say(ctx context.Context, text string) error
	// Card posts a structured message and returns a reference to it that
	// Edit accepts.
	Card(ctx context.Context, card Card) (ref string, err error)
	// Edit replaces the card behind ref.
	Edit(ctx context.Context, ref string, card Card) error
	// Answer tells the sender something that only they see.
	Answer(ctx context.Context, text string) error
}

// Card is a structured message: a title, text, labelled values and rows of
// buttons. A front end draws it within its platform's limits.
type Card struct {
	Title   string
	Text    string
	Fields  []CardField
	Buttons [][]CardButton
	Footer  string
}

// CardField is one labelled value on a Card.
type CardField struct {
	Label string
	Value string
}

// CardButton is one button on a Card. ID comes back to the bot when the
// button is pressed.
type CardButton struct {
	ID    string
	Label string
}

// TextOnly implements the verbs beyond Acknowledge, Reply and Say by
// returning ErrUnsupported. A front end for a platform without cards or
// private answers embeds it.
type TextOnly struct{}

func (TextOnly) Card(context.Context, Card) (string, error) { return "", ErrUnsupported }

func (TextOnly) Edit(context.Context, string, Card) error { return ErrUnsupported }

func (TextOnly) Answer(context.Context, string) error { return ErrUnsupported }
