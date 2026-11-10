package brodec

import (
	"context"
	"errors"
	"log"
	"sync"

	"github.com/bromigos-org/pc-principal/internal/core"
)

// responder is how the brain answers one delivery.
//
// The gateway gives the endpoint three seconds. What the bot has to say
// within them goes back in the endpoint's answer ("inline"). Once the
// responder is sealed, which happens when the endpoint answers, every
// further action goes through the gateway's local API instead, with the
// delivery it belongs to.
//
// An action that sends into the room first takes its turn at the pacer. One
// that would have to wait is not sent inline: it asks the endpoint to
// answer now with what it has, and goes out through the local API when its
// turn comes. The order of what the bot says is kept that way.
//
// One thing to know before a handler sends into the room after answering a
// tap: the gateway answers a tap itself, with a bare "ok", whenever a batch
// of actions sent with the tap's delivery holds no answer to it. A room
// action sent later with that delivery would therefore answer the tap a
// second time. The poll sends its redraws with the /poll command's delivery
// for that reason.
type responder struct {
	client   *client
	pacer    *pacer
	delivery Delivery

	// overflow is closed when an action cannot go inline and the endpoint
	// should answer without waiting for the handler to finish.
	overflow     chan struct{}
	overflowOnce sync.Once
	// flushed is closed when the endpoint has taken the inline actions.
	flushed     chan struct{}
	flushedOnce sync.Once

	mu       sync.Mutex
	inline   []Action
	sealed   bool
	answered bool
	deferred bool
}

func newResponder(c *client, p *pacer, d Delivery) *responder {
	return &responder{client: c, pacer: p, delivery: d, overflow: make(chan struct{}), flushed: make(chan struct{})}
}

var (
	errAnswered   = errors.New("brodec: the tap was already answered")
	errIncomplete = errors.New("brodec: the action lacks what the gateway needs to read it")
)

// seal ends the inline phase and returns the actions the endpoint answers
// with. A tap that has not been answered by then is deferred, which gives
// the handler fifteen minutes to answer it through the local API.
func (r *responder) seal() []Action {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.sealed && r.delivery.isTap() && !r.answered && !r.deferred {
		r.inline = append(r.inline, Action{Type: actionDefer})
		r.deferred = true
	}
	r.sealed = true
	r.flushedOnce.Do(func() { close(r.flushed) })
	return r.inline
}

// emit carries one action out: inline while that is still possible, through
// the local API otherwise.
func (r *responder) emit(ctx context.Context, action Action) error {
	if !action.complete() {
		return errIncomplete
	}
	r.mu.Lock()
	if !r.sealed && (!action.sendsToRoom() || r.pacer.tryTake(r.delivery.RoomID) == 0) {
		r.inline = append(r.inline, action)
		r.mu.Unlock()
		return nil
	}
	// From here on nothing more goes inline, so that nothing overtakes
	// this action. What is inline already goes out first.
	r.sealed = true
	r.mu.Unlock()
	r.overflowOnce.Do(func() { close(r.overflow) })
	select {
	case <-r.flushed:
	case <-ctx.Done():
		return ctx.Err()
	}

	if action.sendsToRoom() {
		if err := r.pacer.wait(ctx, r.delivery.RoomID); err != nil {
			return err
		}
	}
	err := r.client.send(ctx, r.delivery, []Action{action})
	if action.Type == actionReply && refused(err) {
		// The gateway could not send it as a reply to the message. Saying
		// it in the room is better than saying nothing.
		log.Printf("brodec: the gateway refused a reply (%v); saying it in the room instead", err)
		say := action
		say.Type = actionSay
		err = r.client.send(ctx, r.delivery, []Action{say})
	}
	return err
}

// tapAnswer carries out an answer to a tap. A tap takes one answer, or a
// deferral and then one answer.
func (r *responder) tapAnswer(ctx context.Context, action Action) error {
	if !r.delivery.isTap() {
		return core.ErrUnsupported
	}
	if !action.complete() {
		return errIncomplete
	}
	r.mu.Lock()
	if r.answered {
		r.mu.Unlock()
		return errAnswered
	}
	r.answered = true
	r.mu.Unlock()
	return r.emit(ctx, action)
}

// Acknowledge does nothing: the gateway has no way to react to a message.
func (r *responder) Acknowledge(context.Context) error { return nil }

// Reply answers the delivery's message. A long answer is split into a few
// messages; the first is the reply and the rest follow it in the room.
func (r *responder) Reply(ctx context.Context, text string) error {
	chunks := core.SplitReply(text, messageTextMax)
	if len(chunks) > replyChunksMax {
		chunks = chunks[:replyChunksMax]
	}
	for i, chunk := range chunks {
		action := Action{Type: actionReply, Text: chunk}
		if i > 0 {
			action.Type = actionSay
		}
		if err := r.emit(ctx, action); err != nil {
			return err
		}
	}
	return nil
}

// Say posts one line in the room.
func (r *responder) Say(ctx context.Context, text string) error {
	text = clipBytes(text, messageTextMax)
	if text == "" {
		return nil
	}
	return r.emit(ctx, Action{Type: actionSay, Text: text})
}

// Card posts a card as a reply to the delivery's message. The gateway does
// not say which event the card became, so the reference is empty; a tap on
// the card brings its event along.
func (r *responder) Card(ctx context.Context, card core.Card) (string, error) {
	return "", r.emit(ctx, Action{Type: actionCard, Card: wireCard(card)})
}

// Edit replaces a card. With an empty reference it replaces the card that
// was tapped, which only a tap has.
func (r *responder) Edit(ctx context.Context, ref string, card core.Card) error {
	if ref == "" && !r.delivery.isTap() {
		return core.ErrUnsupported
	}
	return r.emit(ctx, Action{Type: actionEditCard, EventID: ref, Card: wireCard(card)})
}

// Answer tells the person who tapped something only they see. Only a tap
// can be answered that way.
func (r *responder) Answer(ctx context.Context, text string) error {
	return r.tapAnswer(ctx, Action{Type: actionAnswer, Text: clipBytes(text, messageTextMax)})
}

// Fail tells the person who tapped that it did not work.
func (r *responder) Fail(ctx context.Context, text string) error {
	return r.tapAnswer(ctx, Action{Type: actionFail, Text: clipBytes(text, messageTextMax)})
}

// Defer tells the person who tapped that the answer is on its way. Nothing
// else on BRODEC waits for the bot, so for anything but a tap it does
// nothing. A tap that is still unanswered when the endpoint answers is
// deferred without being asked.
func (r *responder) Defer(context.Context) error {
	if !r.delivery.isTap() {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answered || r.deferred || r.sealed {
		return nil
	}
	r.deferred = true
	r.inline = append(r.inline, Action{Type: actionDefer})
	return nil
}

var _ core.Responder = (*responder)(nil)
