package core

import (
	"context"
	"errors"
	"testing"
)

// textResponder is a Responder for a platform that only has text. It records
// what the brain asked of it.
type textResponder struct {
	TextOnly
	journal      *journal
	acknowledged int
	replies      []string
	said         []string
	replyErr     error
}

func (r *textResponder) Acknowledge(context.Context) error {
	r.journal.add("acknowledge")
	r.acknowledged++
	return nil
}

func (r *textResponder) Reply(_ context.Context, text string) error {
	r.journal.add("reply")
	r.replies = append(r.replies, text)
	return r.replyErr
}

func (r *textResponder) Say(_ context.Context, text string) error {
	r.journal.add("say")
	r.said = append(r.said, text)
	return nil
}

var _ Responder = (*textResponder)(nil)

func TestTextOnly_reports_the_rich_verbs_as_unsupported(t *testing.T) {
	// Given
	var responder Responder = &textResponder{}
	ctx := context.Background()

	// When
	_, cardErr := responder.Card(ctx, Card{Title: "Poll"})
	editErr := responder.Edit(ctx, "card-1", Card{Title: "Poll"})
	answerErr := responder.Answer(ctx, "only you see this")
	deferErr := responder.Defer(ctx)

	// Then
	for verb, err := range map[string]error{"Card": cardErr, "Edit": editErr, "Answer": answerErr, "Defer": deferErr} {
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("expected %s to report ErrUnsupported, got %v", verb, err)
		}
	}
}
