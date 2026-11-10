package brodec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bromigos-org/pc-principal/internal/core"
	"github.com/bromigos-org/pc-principal/internal/llm"
	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/redis/go-redis/v9"
)

const (
	// The short context: what was said to the bot in a room and what it
	// answered, at most contextMessages of them, kept for contextLifetime
	// after the last one.
	contextLifetime = 2 * time.Hour
	contextMessages = 12

	// inlineBudget is how long the endpoint lets a handler work before it
	// answers the gateway, which waits three seconds.
	inlineBudget = 2 * time.Second
	// workTimeout bounds everything done for one delivery.
	workTimeout = 2 * time.Minute
	// maxDeliveryBytes bounds the body of one delivery.
	maxDeliveryBytes = 1 << 20
	// quotedMax bounds, in characters, how much of the message someone
	// replied to is shown to the model.
	quotedMax = 1000

	// notAllowedLine is all the bot says where it is not on an allowlist.
	notAllowedLine = "I'm not set up to talk here, bro. The people who run me have to add this place first."
	// noticeEvery is how often it says so in one room.
	noticeEvery     = 24 * time.Hour
	noticeKeyPrefix = "brodec:notice:"

	unknownCommandLine = "I don't know that command, bro. Try /help."
	unknownButtonLine  = "That button doesn't do anything any more, bro."
)

// Deps are the services the front end works with.
type Deps struct {
	// Redis is the Redis-compatible store the bot keeps its short context,
	// its polls and its once-a-day notices in. Required.
	Redis redis.Cmdable
	// LLM writes the bot's answers. Required.
	LLM llm.Client
	// HTTPClient reaches the gateway's local API. Nil means a client with a
	// timeout that fits the gateway.
	HTTPClient *http.Client
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Bot is the BRODEC front end: the event endpoint and everything behind it.
type Bot struct {
	config     Config
	allow      allowlist
	redis      redis.Cmdable
	verifier   *verifier
	client     *client
	pacer      *pacer
	mentions   mentionStripper
	dispatcher *core.Dispatcher
	polls      *polls
	now        func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	work   sync.WaitGroup
	rooms  keyedLocks

	mu           sync.Mutex
	recent       map[string]struct{}
	recentOrder  []string
	lastDelivery time.Time
}

// New builds the front end.
func New(config Config, deps Deps) (*Bot, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	if deps.Redis == nil {
		return nil, errors.New("brodec: a Redis-compatible store is required (DRAGONFLY_ADDR): the short context, the polls and the once-a-day notice are kept there")
	}
	if deps.LLM == nil {
		return nil, errors.New("brodec: a chat model client is required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	verifier, err := newVerifier(config.PublicKey, config.publicKeyPath(), now)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bot{
		config:   config,
		allow:    newAllowlist(config),
		redis:    deps.Redis,
		verifier: verifier,
		client:   newClient(config.GatewayURL, config.tokenPath(), deps.HTTPClient),
		pacer:    newPacer(now),
		mentions: newMentionStripper(config.Handle, config.Name, config.UserID),
		now:      now,
		ctx:      ctx,
		cancel:   cancel,
		recent:   map[string]struct{}{},
	}
	b.polls = &polls{bot: b}
	b.dispatcher = &core.Dispatcher{
		Conversation: &core.Conversation{
			Persona:    core.PersonaBRODEC,
			LLM:        deps.LLM,
			NoMemory:   true,
			History:    store.History{Client: deps.Redis, TTL: contextLifetime, MaxHistory: contextMessages},
			HistoryKey: historyKey,
		},
		Commands: b.commands(),
		Buttons:  b.buttons(),
	}
	return b, nil
}

// historyKey is where a room's short context is kept. With the store's own
// prefix the key reads "conversation:brodec:room:<room>" or
// "conversation:brodec:dm:<room>": no Discord key begins that way.
func historyKey(in core.Inbound) string {
	if in.Direct {
		return "brodec:dm:" + in.RoomID
	}
	return "brodec:room:" + in.RoomID
}

// Shutdown stops work in progress and waits for it to end, or for ctx.
func (b *Bot) Shutdown(ctx context.Context) {
	b.cancel()
	done := make(chan struct{})
	go func() {
		b.work.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// drain waits for the work the deliveries so far started. Tests use it.
func (b *Bot) drain() { b.work.Wait() }

// background runs work that may outlive the endpoint's answer. Shutdown
// waits for it.
func (b *Bot) background(work func()) {
	b.work.Add(1)
	go func() {
		defer b.work.Done()
		work()
	}()
}

// workContext bounds one piece of work, and ends it when the bot shuts
// down.
func (b *Bot) workContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(b.ctx, workTimeout)
}

// ServeHTTP is the event endpoint. It answers nothing that is not signed by
// the gateway.
func (b *Bot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxDeliveryBytes))
	if err != nil {
		http.Error(w, "the body could not be read", http.StatusBadRequest)
		return
	}
	switch err := b.verifier.verify(r.Header.Get(headerTimestamp), r.Header.Get(headerSignature), body); {
	case errors.Is(err, errNoKey):
		// The gateway tries its first check again for a minute; by then
		// its key file is there.
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	delivery, err := parseDelivery(body)
	if err != nil {
		http.Error(w, "not a delivery", http.StatusBadRequest)
		return
	}
	b.noteDelivery()
	actions := b.handle(delivery)
	if len(actions) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}
	encoded, err := encodeJSON(answer{Actions: actions})
	if err != nil {
		log.Printf("brodec: encode answer: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
}

// handle takes one verified delivery and returns the actions the endpoint
// answers with. Whatever takes longer goes on in the background.
func (b *Bot) handle(d Delivery) []Action {
	switch d.Type {
	case deliveryPing:
		return nil
	case deliveryCommand, deliveryMention, deliveryMessage, deliveryButton:
	default:
		// A kind of delivery this version does not know.
		return nil
	}
	if d.Type == deliveryMessage && d.IsGroup {
		// A group message that does not address the bot. The gateway hands
		// those over only when it is told to read everything, which this
		// bot never asks for. It is dropped unread.
		return nil
	}
	if !b.firstTime(d.dedupeID()) {
		return nil
	}

	r := newResponder(b.client, b.pacer, d)
	if !b.allow.allows(d) {
		return b.inline(r, func(ctx context.Context) { b.refuse(ctx, d, r) })
	}

	in := b.inbound(d)
	if in.Kind == core.KindMessage {
		// The answer needs the model. The endpoint answers the gateway at
		// once and the reply follows through the local API.
		r.seal()
		b.background(func() {
			unlock := b.rooms.lock(d.RoomID)
			defer unlock()
			ctx, cancel := b.workContext()
			defer cancel()
			b.dispatcher.Dispatch(ctx, in, describer{}, r)
		})
		return nil
	}

	return b.inline(r, func(ctx context.Context) {
		if b.dispatcher.Dispatch(ctx, in, describer{}, r) != core.RouteNone {
			return
		}
		if in.Kind == core.KindButton {
			_ = r.Fail(ctx, unknownButtonLine)
			return
		}
		_ = r.Reply(ctx, unknownCommandLine)
	})
}

// inline runs work that needs no model and returns what it answered with
// in time for the endpoint's answer. The endpoint does not wait longer than
// its budget, nor past the first action that cannot go inline; the work
// goes on either way, and what it says later goes through the local API.
func (b *Bot) inline(r *responder, work func(ctx context.Context)) []Action {
	done := make(chan struct{})
	b.background(func() {
		defer close(done)
		ctx, cancel := b.workContext()
		defer cancel()
		work(ctx)
	})
	timer := time.NewTimer(inlineBudget)
	defer timer.Stop()
	select {
	case <-done:
	case <-r.overflow:
	case <-timer.C:
	}
	return r.seal()
}

// refuse answers a delivery from a place that is not on an allowlist: the
// fixed line, at most once a day per room, and never the model. A tap gets
// the line privately, which costs the room nothing.
func (b *Bot) refuse(ctx context.Context, d Delivery, r *responder) {
	if d.isTap() {
		_ = r.Fail(ctx, notAllowedLine)
		return
	}
	first, err := b.redis.SetNX(ctx, noticeKeyPrefix+d.RoomID, "1", noticeEvery).Result()
	if err != nil {
		// Without the store there is no telling whether the line was said
		// today. Silence is the safe answer.
		log.Printf("brodec: notice state: %v", err)
		return
	}
	if first {
		_ = r.Reply(ctx, notAllowedLine)
	}
}

// inbound turns a delivery into what the brain sees.
func (b *Bot) inbound(d Delivery) core.Inbound {
	in := core.Inbound{
		Platform:   core.PlatformBRODEC,
		MessageID:  d.EventID,
		RoomID:     d.RoomID,
		SenderID:   d.Sender,
		SenderName: d.Sender,
		Addressed:  true,
		Plain:      true,
	}
	switch d.Type {
	case deliveryCommand:
		in.Kind = core.KindCommand
		in.Direct = !d.IsGroup
		in.Command = strings.ToLower(d.Name)
		in.Args = d.Args
		in.Text = strings.TrimSpace("/" + d.Name + " " + d.Args)
	case deliveryButton:
		in.Kind = core.KindButton
		in.ButtonID = d.ButtonID
	default:
		in.Direct = !d.IsGroup
		in.Text = b.mentions.strip(d.Body)
		if quoted := strings.TrimSpace(d.Quoted); quoted != "" {
			in.ReplyTo = &core.Reference{Text: clip(quoted, quotedMax)}
		}
	}
	return in
}

// describer labels a speaker for the prompt. The gateway gives the bot a
// Matrix ID and nothing else, so that is the label; it tells the model who
// is who in a room and is kept nowhere but in the short context.
type describer struct{}

func (describer) Sender(_ context.Context, in core.Inbound) string { return in.SenderID }

func (describer) Scene(_ context.Context, in core.Inbound, _ string) string { return in.SenderID }

// firstTime reports whether a delivery is new, and remembers it. The
// gateway does not deliver an event twice, but a signed delivery stays
// valid for five minutes, and nothing should be answered twice.
func (b *Bot) firstTime(id string) bool {
	const remembered = 4096
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, seen := b.recent[id]; seen {
		return false
	}
	b.recent[id] = struct{}{}
	b.recentOrder = append(b.recentOrder, id)
	if len(b.recentOrder) > remembered {
		delete(b.recent, b.recentOrder[0])
		b.recentOrder = b.recentOrder[1:]
	}
	return true
}

func (b *Bot) noteDelivery() {
	b.mu.Lock()
	b.lastDelivery = b.now()
	b.mu.Unlock()
}

// Health is what the bot knows about the gateway beside it.
type Health struct {
	// Gateway is "ready" when the gateway's local API answers: it is signed
	// in and delivering.
	Gateway string
	// Key says whether the gateway's public key is in hand.
	Key string
	// LastDelivery is when the gateway last delivered anything, its own
	// check included. Zero means never.
	LastDelivery time.Time
	// Ready is true when deliveries can be verified and answers sent.
	Ready bool
}

// Health asks the gateway whether it is up.
func (b *Bot) Health(ctx context.Context) Health {
	health := Health{Gateway: "ready", Key: "loaded"}
	b.mu.Lock()
	health.LastDelivery = b.lastDelivery
	b.mu.Unlock()

	hasKey := b.verifier.hasKey()
	if !hasKey {
		health.Key = "missing"
	}
	served, err := b.client.publicKey(ctx)
	var gatewayErr *gatewayError
	switch {
	case errors.As(err, &gatewayErr):
		health.Gateway = fmt.Sprintf("not_ready: the local API answered %d", gatewayErr.Status)
	case err != nil:
		health.Gateway = "unreachable"
	case hasKey && !b.verifier.matches(served):
		// The gateway signs with a key other than the one the bot checks
		// with: every delivery is being refused.
		health.Key = "mismatch"
	}
	health.Ready = health.Gateway == "ready" && health.Key == "loaded"
	return health
}

// keyedLocks is one lock per key, so that two messages in one room are
// answered one after the other and each sees the other in the context.
type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu      sync.Mutex
	waiting int
}

// lock takes the key's lock and returns the function that gives it back.
func (k *keyedLocks) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*keyedLock{}
	}
	entry, ok := k.locks[key]
	if !ok {
		entry = &keyedLock{}
		k.locks[key] = entry
	}
	entry.waiting++
	k.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		k.mu.Lock()
		entry.waiting--
		if entry.waiting == 0 {
			delete(k.locks, key)
		}
		k.mu.Unlock()
	}
}
