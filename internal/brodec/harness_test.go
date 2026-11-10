package brodec

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/redis/go-redis/v9"
)

// The ids the gateway's fixtures use.
const (
	fixtureRoom   = "!kWqPzLmNcXvBtRyUoA:brodec.ai"
	fixtureDMRoom = "!dMzTqWvHsKpLxNbYcE:brodec.ai"
	fixtureSender = "@b3f9a1c05d7e2b46a8c0d1e9:brodec.ai"
	otherSender   = "@0c1d2e3f4a5b6c7d8e9f0a1b:brodec.ai"
	testToken     = "test-local-api-token"
)

// fixture reads one of the gateway's wire fixtures, without the line break
// the file ends with: the gateway signs and sends the JSON alone.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "wire", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return bytes.TrimRight(content, "\n")
}

// edited returns a fixture delivery with some of its fields replaced.
func edited(t *testing.T, name string, changes map[string]any) []byte {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(fixture(t, name), &fields); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	for key, value := range changes {
		fields[key] = value
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("encode delivery: %v", err)
	}
	return encoded
}

// fakeLLM stands in for the chat model. It counts every call: several tests
// are about the model not being called.
type fakeLLM struct {
	mu       sync.Mutex
	requests [][]store.Message
	reply    string
	err      error
}

func (f *fakeLLM) Generate(_ context.Context, messages []store.Message) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, append([]store.Message(nil), messages...))
	if f.err != nil {
		return "", f.err
	}
	if f.reply == "" {
		return "Sweet, bro.", nil
	}
	return f.reply, nil
}

func (f *fakeLLM) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeLLM) lastRequest(t *testing.T) []store.Message {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("the model was not called")
	}
	return f.requests[len(f.requests)-1]
}

// localCall is one request the bot made to the gateway's local API.
type localCall struct {
	Event   json.RawMessage `json:"event"`
	Actions []Action        `json:"actions"`
	raw     []byte
}

// fakeGateway is the gateway's local API.
type fakeGateway struct {
	server *httptest.Server
	key    string

	mu            sync.Mutex
	calls         []localCall
	refuseReplies bool
	unauthorized  int
}

func newFakeGateway(t *testing.T, publicKey string) *fakeGateway {
	t.Helper()
	g := &fakeGateway{key: publicKey}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/public-key", func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		_, _ = io.WriteString(w, g.key)
	})
	mux.HandleFunc("/v1/actions", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			g.unauthorized++
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"the gateway's token is in its data directory (local-api-token)"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		call := localCall{raw: body}
		if err := json.Unmarshal(body, &call); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		g.calls = append(g.calls, call)
		if g.refuseReplies && len(call.Actions) > 0 && call.Actions[0].Type == actionReply {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"the event to reply to could not be read"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
	g.server = httptest.NewServer(mux)
	t.Cleanup(g.server.Close)
	return g
}

func (g *fakeGateway) setKey(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.key = key
}

func (g *fakeGateway) refuseEveryReply() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refuseReplies = true
}

func (g *fakeGateway) refusedTries() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.unauthorized
}

func (g *fakeGateway) localCalls() []localCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]localCall(nil), g.calls...)
}

// actions are all the actions the bot sent through the local API, in order.
func (g *fakeGateway) actions() []Action {
	var all []Action
	for _, call := range g.localCalls() {
		all = append(all, call.Actions...)
	}
	return all
}

// harness is a bot with everything around it faked: the gateway on both
// sides, the model and the store.
type harness struct {
	t       *testing.T
	bot     *Bot
	llm     *fakeLLM
	gateway *fakeGateway
	redis   *miniredis.Miniredis
	rdb     *redis.Client
	private ed25519.PrivateKey
	dataDir string
	config  Config
	now     func() time.Time
	// unpaced, when set, is applied to every bot the harness starts.
	unpaced func(*Bot)
}

type harnessOption func(*harness)

func withConfig(change func(*Config)) harnessOption {
	return func(h *harness) { change(&h.config) }
}

func withClock(now func() time.Time) harnessOption {
	return func(h *harness) { h.now = now }
}

// newHarness starts a bot that answers in the fixtures' group room, and in
// direct messages from the fixtures' sender.
func newHarness(t *testing.T, options ...harnessOption) *harness {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("make key: %v", err)
	}
	encodedKey := base64.RawStdEncoding.EncodeToString(public)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, publicKeyFile), encodedKey)
	writeFile(t, filepath.Join(dir, tokenFile), testToken)

	server := miniredis.RunT(t)
	h := &harness{
		t:       t,
		llm:     &fakeLLM{},
		gateway: newFakeGateway(t, encodedKey),
		redis:   server,
		// No retries, so that a test about the store being down is quick.
		rdb:     redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1}),
		private: private,
		dataDir: dir,
	}
	t.Cleanup(func() { _ = h.rdb.Close() })
	h.config = Config{
		GatewayURL:     h.gateway.server.URL,
		GatewayData:    dir,
		AllowedRooms:   []string{fixtureRoom},
		AllowedDMUsers: []string{fixtureSender},
		Handle:         defaultHandle,
		Name:           defaultName,
		AIWhere:        defaultAIWhere,
	}
	for _, option := range options {
		option(h)
	}
	h.start()
	return h
}

// start builds the bot. Calling it again is a restart: a new bot, with
// nothing in memory, on the same store and the same gateway.
func (h *harness) start() {
	h.t.Helper()
	if h.bot != nil {
		h.bot.Shutdown(context.Background())
	}
	bot, err := New(h.config, Deps{Redis: h.rdb, LLM: h.llm, Now: h.now})
	if err != nil {
		h.t.Fatalf("new bot: %v", err)
	}
	h.bot = bot
	if h.unpaced != nil {
		h.unpaced(bot)
	}
	h.t.Cleanup(func() { bot.Shutdown(context.Background()) })
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (h *harness) clock() time.Time {
	if h.now != nil {
		return h.now()
	}
	return time.Now()
}

// sign is what the gateway does: hex Ed25519 over timestamp + body.
func sign(private ed25519.PrivateKey, timestamp string, body []byte) string {
	return hex.EncodeToString(ed25519.Sign(private, append([]byte(timestamp), body...)))
}

// request builds a delivery as the gateway posts it, signed now.
func (h *harness) request(body []byte) *http.Request {
	timestamp := strconv.FormatInt(h.clock().Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, EventsPath, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(headerTimestamp, timestamp)
	request.Header.Set(headerSignature, sign(h.private, timestamp, body))
	return request
}

// deliver posts a signed delivery, waits for everything it set off, and
// returns the actions the endpoint answered with.
func (h *harness) deliver(body []byte) []Action {
	h.t.Helper()
	recorder := httptest.NewRecorder()
	h.bot.ServeHTTP(recorder, h.request(body))
	h.bot.drain()
	if recorder.Code != http.StatusOK {
		h.t.Fatalf("the endpoint answered %d: %s", recorder.Code, recorder.Body.String())
	}
	return decodeAnswer(h.t, recorder.Body.Bytes())
}

func decodeAnswer(t *testing.T, body []byte) []Action {
	t.Helper()
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var decoded answer
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("the endpoint's answer is not {\"actions\": [...]}: %v\n%s", err, body)
	}
	return decoded.Actions
}

// status posts a request and returns the status the endpoint answered with.
func (h *harness) status(request *http.Request) int {
	recorder := httptest.NewRecorder()
	h.bot.ServeHTTP(recorder, request)
	h.bot.drain()
	return recorder.Code
}

// keys are the keys in the store, for tests about what is kept.
func (h *harness) keys() []string { return h.redis.Keys() }

func mustEncode(t *testing.T, value any) string {
	t.Helper()
	encoded, err := encodeJSON(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(encoded)
}

func types(actions []Action) []string {
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		out = append(out, action.Type)
	}
	return out
}

var errModelDown = errors.New("the model is down")

// fakeClock is a clock that moves only when it is told to, or when the
// pacer sleeps on it.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Now()} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newUnpacedHarness is newHarness on a fake clock that the pacer's waits
// move forward, so that a test with many sends does not wait for real. It
// returns how long the pacer has waited so far.
func newUnpacedHarness(t *testing.T, options ...harnessOption) (*harness, *fakeClock, func() time.Duration) {
	t.Helper()
	clock := newFakeClock()
	h := newHarness(t, append([]harnessOption{withClock(clock.Now)}, options...)...)
	var mu sync.Mutex
	var slept time.Duration
	h.unpaced = func(bot *Bot) {
		bot.pacer.sleep = func(_ context.Context, d time.Duration) error {
			mu.Lock()
			slept += d
			mu.Unlock()
			clock.Advance(d)
			return nil
		}
	}
	h.unpaced(h.bot)
	return h, clock, func() time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return slept
	}
}

func newRecorder() *httptest.ResponseRecorder { return httptest.NewRecorder() }

func bytesReader(content []byte) io.Reader { return bytes.NewReader(content) }
