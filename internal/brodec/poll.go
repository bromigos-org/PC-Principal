package brodec

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/redis/go-redis/v9"
)

const (
	// pollLifetime is how long a poll takes votes. The poll and its votes
	// are erased when it ends.
	pollLifetime = 7 * 24 * time.Hour

	pollKeyPrefix    = "brodec:poll:"
	pollButtonPrefix = "poll:"

	pollIDBytes     = 8
	pollChoicesMin  = 2
	pollChoicesMax  = cardFieldsMax
	pollQuestionMax = 300

	pollUsageLine    = "Give me a question and at least two choices, bro: /poll Movie tonight? | ARRIVAL | HEAT"
	pollTooManyLine  = "That's too many choices, bro. Ten at most."
	pollStoreLine    = "I can't keep a poll right now, bro. Try again in a bit."
	pollOverLine     = "That poll is over, bro."
	pollVoteFailLine = "I couldn't count that, bro. Tap again in a bit."
)

// poll is a poll as it is kept in the store, under pollKeyPrefix and its
// id. The votes are kept beside it in a hash, under the same key with
// ":votes": one field per voter, named by the voter's marker, holding the
// number of the choice.
type poll struct {
	ID       string   `json:"id"`
	RoomID   string   `json:"room_id"`
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
	// Salt keys the voters' markers. It is made for this poll alone, so a
	// marker says nothing outside it.
	Salt string `json:"salt"`
	// Command is the delivery of the /poll command, as the gateway made it.
	// The gateway's local API takes an action only together with a delivery
	// it made, and this is the one the card's later updates are sent with.
	Command   json.RawMessage `json:"command"`
	ExpiresAt time.Time       `json:"expires_at"`
}

func pollKey(id string) string { return pollKeyPrefix + id }

func votesKey(id string) string { return pollKeyPrefix + id + ":votes" }

// marker stands for a voter in the votes of one poll. It is the same every
// time the same person votes in the poll, which is what lets them change
// their vote, and it is not their ID.
func (p poll) marker(voter string) string {
	mac := hmac.New(sha256.New, []byte(p.Salt))
	mac.Write([]byte(voter))
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// buttonID is the id of the button for one choice. It carries the poll's
// id, because the gateway does not say which event a card became.
func (p poll) buttonID(choice int) string {
	return pollButtonPrefix + p.ID + ":" + strconv.Itoa(choice)
}

// card draws the poll: a bar and a button per choice.
func (p poll) card(counts []int) core.Card {
	total := 0
	for _, count := range counts {
		total += count
	}
	card := core.Card{Title: "POLL", Text: p.Question}
	var row []core.CardButton
	for i, choice := range p.Choices {
		percent := 0
		if total > 0 {
			percent = (counts[i]*100 + total/2) / total
		}
		card.Fields = append(card.Fields, core.CardField{Label: choice, Value: strconv.Itoa(counts[i]), Bar: &percent})
		row = append(row, core.CardButton{ID: p.buttonID(i), Label: choice})
		if len(row) == cardRowButtonsMax {
			card.Buttons = append(card.Buttons, row)
			row = nil
		}
	}
	if len(row) > 0 {
		card.Buttons = append(card.Buttons, row)
	}
	votes := "votes"
	if total == 1 {
		votes = "vote"
	}
	card.Footer = fmt.Sprintf("%d %s · nobody sees who voted for what", total, votes)
	return card
}

// parsePoll reads "Question | A | B ...". It returns the line to answer
// with when that is not what it was given.
func parsePoll(args string) (question string, choices []string, problem string) {
	parts := strings.Split(args, "|")
	question = clip(strings.TrimSpace(parts[0]), pollQuestionMax)
	for _, part := range parts[1:] {
		if choice := oneLine(clip(strings.TrimSpace(part), cardButtonLabelMax)); choice != "" {
			choices = append(choices, choice)
		}
	}
	switch {
	case question == "" || len(choices) < pollChoicesMin:
		return "", nil, pollUsageLine
	case len(choices) > pollChoicesMax:
		return "", nil, pollTooManyLine
	}
	return question, choices, ""
}

// polls runs the /poll command and the votes on its cards.
type polls struct {
	bot *Bot

	mu      sync.Mutex
	pending map[string]*pendingRedraw
}

// pendingRedraw is a poll whose card is being brought up to date.
type pendingRedraw struct {
	cardEvent string
	dirty     bool
}

// create answers /poll with the poll's card.
func (ps *polls) create(ctx context.Context, in core.Inbound, r core.Responder) {
	question, choices, problem := parsePoll(in.Args)
	if problem != "" {
		_ = r.Reply(ctx, problem)
		return
	}
	from, ok := r.(*responder)
	if !ok {
		_ = r.Reply(ctx, pollStoreLine)
		return
	}
	id, err := randomHex(pollIDBytes)
	if err != nil {
		_ = r.Reply(ctx, pollStoreLine)
		return
	}
	salt, err := randomHex(16)
	if err != nil {
		_ = r.Reply(ctx, pollStoreLine)
		return
	}
	p := poll{
		ID:        id,
		RoomID:    in.RoomID,
		Question:  question,
		Choices:   choices,
		Salt:      salt,
		Command:   from.delivery.raw,
		ExpiresAt: ps.bot.now().Add(pollLifetime).UTC(),
	}
	encoded, err := json.Marshal(p)
	if err == nil {
		err = ps.bot.redis.Set(ctx, pollKey(p.ID), encoded, pollLifetime).Err()
	}
	if err != nil {
		log.Printf("brodec: keep poll: %v", err)
		_ = r.Reply(ctx, pollStoreLine)
		return
	}
	_, _ = r.Card(ctx, p.card(make([]int, len(p.Choices))))
}

// vote counts a tap on one of a poll's buttons. The person who tapped is
// told privately; the card is then redrawn for everyone with the new count.
func (ps *polls) vote(ctx context.Context, in core.Inbound, r core.Responder) {
	from, ok := r.(*responder)
	if !ok {
		return
	}
	id, choice, ok := parsePollButton(in.ButtonID)
	if !ok {
		_ = from.Fail(ctx, unknownButtonLine)
		return
	}
	p, err := ps.load(ctx, id)
	if err != nil {
		log.Printf("brodec: load poll: %v", err)
		_ = from.Fail(ctx, pollVoteFailLine)
		return
	}
	if p == nil || p.RoomID != in.RoomID || choice >= len(p.Choices) {
		_ = from.Fail(ctx, pollOverLine)
		return
	}

	marker := p.marker(in.SenderID)
	var before *redis.StringCmd
	_, err = ps.bot.redis.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		before = pipe.HGet(ctx, votesKey(p.ID), marker)
		pipe.HSet(ctx, votesKey(p.ID), marker, choice)
		pipe.ExpireAt(ctx, votesKey(p.ID), p.ExpiresAt)
		return nil
	})
	if err != nil && !errors.Is(err, redis.Nil) {
		log.Printf("brodec: count vote: %v", err)
		_ = from.Fail(ctx, pollVoteFailLine)
		return
	}

	previous, err := before.Int()
	switch {
	case err != nil:
		_ = from.Answer(ctx, "Your vote: "+p.Choices[choice])
	case previous == choice:
		_ = from.Answer(ctx, "Your vote is already "+p.Choices[choice]+".")
	default:
		_ = from.Answer(ctx, "Changed your vote to "+p.Choices[choice]+".")
	}
	ps.redraw(*p, from.delivery.EventID)
}

// parsePollButton reads "poll:<id>:<choice>".
func parsePollButton(buttonID string) (id string, choice int, ok bool) {
	rest, found := strings.CutPrefix(buttonID, pollButtonPrefix)
	if !found {
		return "", 0, false
	}
	id, number, found := strings.Cut(rest, ":")
	if !found || !isPollID(id) {
		return "", 0, false
	}
	choice, err := strconv.Atoi(number)
	if err != nil || choice < 0 {
		return "", 0, false
	}
	return id, choice, true
}

// isPollID reports whether text has the shape of a poll's id. The id in a
// button comes from whoever tapped, and it becomes part of a store key.
func isPollID(text string) bool {
	if len(text) != 2*pollIDBytes {
		return false
	}
	_, err := hex.DecodeString(text)
	return err == nil
}

// load reads a poll. It returns nil when the poll is over or never was.
func (ps *polls) load(ctx context.Context, id string) (*poll, error) {
	encoded, err := ps.bot.redis.Get(ctx, pollKey(id)).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p poll
	if err := json.Unmarshal([]byte(encoded), &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// tally counts the votes for each choice.
func (ps *polls) tally(ctx context.Context, p poll) ([]int, error) {
	votes, err := ps.bot.redis.HVals(ctx, votesKey(p.ID)).Result()
	if err != nil {
		return nil, err
	}
	counts := make([]int, len(p.Choices))
	for _, vote := range votes {
		if choice, err := strconv.Atoi(vote); err == nil && choice >= 0 && choice < len(counts) {
			counts[choice]++
		}
	}
	return counts, nil
}

// redraw brings a poll's card up to date for everyone. cardEvent is the
// card, as the tap named it.
//
// One poll is redrawn by one worker at a time. Each redraw takes its turn
// at the pacer and then draws the count as it is at that moment, so a rush
// of votes becomes a few redraws, the last of which shows every vote. The
// redraw is sent with the /poll command's delivery, not the tap's: sent
// with a tap, the gateway would answer the tap a second time.
func (ps *polls) redraw(p poll, cardEvent string) {
	ps.mu.Lock()
	if ps.pending == nil {
		ps.pending = map[string]*pendingRedraw{}
	}
	if waiting, ok := ps.pending[p.ID]; ok {
		waiting.cardEvent, waiting.dirty = cardEvent, true
		ps.mu.Unlock()
		return
	}
	waiting := &pendingRedraw{cardEvent: cardEvent, dirty: true}
	ps.pending[p.ID] = waiting
	ps.mu.Unlock()

	ps.bot.background(func() {
		for {
			ps.mu.Lock()
			if !waiting.dirty {
				delete(ps.pending, p.ID)
				ps.mu.Unlock()
				return
			}
			waiting.dirty = false
			event := waiting.cardEvent
			ps.mu.Unlock()

			if err := ps.redrawOnce(p, event); err != nil {
				log.Printf("brodec: redraw poll: %v", err)
				ps.mu.Lock()
				delete(ps.pending, p.ID)
				ps.mu.Unlock()
				return
			}
		}
	})
}

func (ps *polls) redrawOnce(p poll, cardEvent string) error {
	ctx, cancel := ps.bot.workContext()
	defer cancel()
	if err := ps.bot.pacer.wait(ctx, p.RoomID); err != nil {
		return err
	}
	counts, err := ps.tally(ctx, p)
	if err != nil {
		return err
	}
	command, err := parseDelivery(p.Command)
	if err != nil {
		return err
	}
	return ps.bot.client.send(ctx, command, []Action{{Type: actionEditCard, EventID: cardEvent, Card: wireCard(p.card(counts))}})
}

func randomHex(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
