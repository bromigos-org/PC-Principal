package characterization

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/ambient"
	"github.com/bromigos-org/pc-principal/internal/commands"
	"github.com/bromigos-org/pc-principal/internal/memory"
	"github.com/bromigos-org/pc-principal/internal/store"
	"github.com/bwmarrin/discordgo"
)

var update = flag.Bool("update", false, "rewrite the golden files from what the code does now")

const (
	botID            = "bot-1"
	personaGolden    = "persona.golden"
	personaReference = `"<<persona.golden>>"`
)

// world is one bot with every outside service faked and recorded.
type world struct {
	t       *testing.T
	session *discordgo.Session

	mu         sync.Mutex
	transcript []string
	logs       bytes.Buffer

	discord *fakeDiscord
	llm     *fakeLLM
	gnosis  *fakeGnosis
	redis   *fakeRedis
	clock   *fakeClock

	messages int
}

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{t: t}

	w.redis = newFakeRedis(t, w.record)
	store.Init(w.redis.addr())

	w.llm = &fakeLLM{world: w}
	llmServer := httptest.NewServer(w.llm)
	t.Cleanup(llmServer.Close)
	t.Setenv("LITELLM_BASE_URL", llmServer.URL)
	t.Setenv("LITELLM_API_KEY", "test-key")

	w.gnosis = &fakeGnosis{world: w, responses: map[string]string{}, fail: map[string]bool{}}
	gnosisServer := httptest.NewServer(w.gnosis)
	t.Cleanup(gnosisServer.Close)
	commands.ConfigureMemoryTenant("bromigos")
	commands.ConfigureMemory(memory.NewClient(memory.Config{Enabled: true, BaseURL: gnosisServer.URL, Token: "test-token", TenantID: "bromigos"}, nil))
	t.Cleanup(func() { commands.ConfigureMemory(nil) })

	commands.ConfigureAmbient(nil)
	t.Cleanup(func() { commands.ConfigureAmbient(nil) })

	session, err := discordgo.New("Bot test")
	if err != nil {
		t.Fatalf("new discord session: %v", err)
	}
	session.State.User = &discordgo.User{ID: botID, Username: "PC Principal", Bot: true}
	w.discord = &fakeDiscord{world: w, routes: map[string]string{}, fail: map[string]bool{}}
	session.Client = &http.Client{Transport: w.discord}
	w.session = session

	previousLogOutput, previousLogFlags := log.Writer(), log.Flags()
	log.SetOutput(&w.logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousLogOutput)
		log.SetFlags(previousLogFlags)
	})
	return w
}

// enableAmbient switches ambient replies on, with a clock the test moves.
func (w *world) enableAmbient() {
	w.clock = &fakeClock{now: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)}
	commands.ConfigureAmbient(ambient.NewManager(ambient.Config{Enabled: true}, &recordingAmbientStore{world: w, states: map[string]ambient.State{}}, w.clock))
}

func (w *world) record(format string, args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.transcript = append(w.transcript, fmt.Sprintf(format, args...))
}

// deliver hands one message to the conversation handlers in the order the
// live message handler calls them. The anonymous-vent handlers that sit
// between them in production are left out: they are Discord-only code that
// this package does not cover.
func (w *world) deliver(label string, message *discordgo.MessageCreate) {
	w.t.Helper()
	w.record("=== %s", label)
	stdout := captureStdout(w.t, func() {
		commands.BotMention(w.session, message)
		commands.HeyThreadHandler(w.session, message)
		commands.AmbientReply(w.session, message)
	})
	for _, line := range nonEmptyLines(stdout) {
		w.record("stdout: %s", line)
	}
	for _, line := range nonEmptyLines(w.logs.String()) {
		w.record("log: %s", line)
	}
	w.logs.Reset()
}

// check compares the transcript with the scenario's golden file.
func (w *world) check(name string) {
	w.t.Helper()
	w.mu.Lock()
	got := strings.Join(w.transcript, "\n") + "\n"
	w.mu.Unlock()
	got = strings.ReplaceAll(got, personaJSON(w.t), personaReference)

	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			w.t.Fatalf("write golden file: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		w.t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if got != string(want) {
		w.t.Fatalf("transcript differs from %s\n%s", path, firstDifference(string(want), got))
	}
}

// message builds a guild message from blackflame in #general; the options
// change what the scenario needs changed.
func (w *world) message(content string, options ...func(*discordgo.Message)) *discordgo.MessageCreate {
	w.messages++
	message := &discordgo.Message{
		ID:        fmt.Sprintf("message-%d", w.messages),
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		Content:   content,
		Author:    &discordgo.User{ID: "user-1", Username: "blackflame"},
		Member:    &discordgo.Member{Roles: []string{"role-admin"}},
	}
	if strings.Contains(content, "<@"+botID+">") || strings.Contains(content, "<@!"+botID+">") {
		message.Mentions = []*discordgo.User{{ID: botID, Username: "PC Principal", Bot: true}}
	}
	for _, option := range options {
		option(message)
	}
	return &discordgo.MessageCreate{Message: message}
}

func inChannel(channelID string) func(*discordgo.Message) {
	return func(m *discordgo.Message) { m.ChannelID = channelID }
}

func directMessage(channelID string) func(*discordgo.Message) {
	return func(m *discordgo.Message) {
		m.ChannelID = channelID
		m.GuildID = ""
		m.Member = nil
	}
}

func from(userID string, username string, roles ...string) func(*discordgo.Message) {
	return func(m *discordgo.Message) {
		m.Author = &discordgo.User{ID: userID, Username: username}
		m.Member = &discordgo.Member{Roles: roles}
	}
}

func alsoMentions(userID string, username string) func(*discordgo.Message) {
	return func(m *discordgo.Message) {
		m.Mentions = append(m.Mentions, &discordgo.User{ID: userID, Username: username})
	}
}

// replyPingingBot marks the message as a reply to one of the bot's messages
// with the reply ping left on: Discord lists the bot under Mentions although
// its mention is not in the text.
func replyPingingBot() func(*discordgo.Message) {
	return func(m *discordgo.Message) {
		bot := &discordgo.User{ID: botID, Username: "PC Principal", Bot: true}
		m.Mentions = append(m.Mentions, bot)
		m.ReferencedMessage = &discordgo.Message{ID: "bot-message-1", ChannelID: m.ChannelID, Author: bot}
	}
}

func replyingToBot() func(*discordgo.Message) {
	return func(m *discordgo.Message) {
		bot := &discordgo.User{ID: botID, Username: "PC Principal", Bot: true}
		m.ReferencedMessage = &discordgo.Message{ID: "bot-message-1", ChannelID: m.ChannelID, Author: bot}
	}
}

// fakeDiscord answers the REST calls the handlers make and records each one.
type fakeDiscord struct {
	world *world
	// routes maps "METHOD /path" to the JSON body to answer with.
	routes map[string]string
	// fail lists "METHOD /path" keys that answer 500.
	fail    map[string]bool
	sent    int
	threads int
}

const discordAPI = "/api/v9"

func (d *fakeDiscord) RoundTrip(req *http.Request) (*http.Response, error) {
	path := strings.TrimPrefix(req.URL.EscapedPath(), discordAPI)
	key := req.Method + " " + path
	line := "discord " + key
	if req.URL.RawQuery != "" {
		line += "?" + req.URL.RawQuery
	}
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	if len(body) > 0 {
		line += "\n" + indentJSON(string(body))
	}

	status, response := http.StatusNotFound, `{"message":"Unknown","code":0}`
	switch {
	case d.fail[key]:
		status, response = http.StatusInternalServerError, `{"message":"test failure"}`
	case req.Method == http.MethodPut && strings.Contains(path, "/reactions/"):
		status, response = http.StatusNoContent, ""
	case req.Method == http.MethodPost && strings.HasSuffix(path, "/messages"):
		d.sent++
		var sent discordgo.MessageSend
		_ = json.Unmarshal(body, &sent)
		channelID := strings.TrimSuffix(strings.TrimPrefix(path, "/channels/"), "/messages")
		encoded, _ := json.Marshal(map[string]string{"id": fmt.Sprintf("sent-%d", d.sent), "channel_id": channelID, "content": sent.Content})
		status, response = http.StatusOK, string(encoded)
	case req.Method == http.MethodPost && strings.HasSuffix(path, "/threads"):
		d.threads++
		var start discordgo.ThreadStart
		_ = json.Unmarshal(body, &start)
		encoded, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("thread-%d", d.threads), "type": discordgo.ChannelTypeGuildPublicThread, "guild_id": "guild-1", "name": start.Name})
		status, response = http.StatusOK, string(encoded)
	default:
		if route, ok := d.routes[key]; ok {
			status, response = http.StatusOK, route
		}
	}
	d.world.record("%s\n  -> %d", line, status)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(response)),
		Request:    req,
	}, nil
}

// fakeLLM is the chat-completion endpoint. Replies are taken from a queue.
type fakeLLM struct {
	world   *world
	replies []llmReply
}

type llmReply struct {
	status  int
	content string
}

func (l *fakeLLM) queue(contents ...string) {
	for _, content := range contents {
		l.replies = append(l.replies, llmReply{status: http.StatusOK, content: content})
	}
}

func (l *fakeLLM) queueFailure() {
	l.replies = append(l.replies, llmReply{status: http.StatusInternalServerError})
}

func (l *fakeLLM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	learnPersona(l.world.t, body)
	l.world.record("llm %s %s\n%s", r.Method, r.URL.Path, indentJSON(string(body)))
	if len(l.replies) == 0 {
		l.world.t.Errorf("chat completion requested with no reply queued")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	reply := l.replies[0]
	l.replies = l.replies[1:]
	if reply.status != http.StatusOK {
		w.WriteHeader(reply.status)
		_, _ = io.WriteString(w, `{"error":"model unavailable"}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"role": "assistant", "content": reply.content}}}})
}

// fakeGnosis is the memory service.
type fakeGnosis struct {
	world *world
	// responses maps a path to the JSON body to answer with.
	responses map[string]string
	// fail lists paths, or path prefixes ending in "*", that answer 500.
	fail   map[string]bool
	traces int
	steps  int
	calls  int
}

func (g *fakeGnosis) failing(path string) bool {
	if g.fail[path] {
		return true
	}
	for pattern := range g.fail {
		if prefix, ok := strings.CutSuffix(pattern, "*"); ok && strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func (g *fakeGnosis) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	path := r.URL.Path
	if g.failing(path) {
		g.world.record("gnosis %s %s\n%s\n  -> 500", r.Method, path, indentJSON(string(body)))
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	g.world.record("gnosis %s %s\n%s", r.Method, path, indentJSON(string(body)))
	response := `{}`
	switch {
	case g.responses[path] != "":
		response = g.responses[path]
	case path == "/v1/messages":
		response = `{"accepted":true}`
	case path == "/v1/reasoning/traces":
		g.traces++
		response = fmt.Sprintf(`{"trace_id":"trace-%d"}`, g.traces)
	case strings.HasSuffix(path, "/steps"):
		g.steps++
		response = fmt.Sprintf(`{"step_id":"step-%d"}`, g.steps)
	case strings.HasSuffix(path, "/tool-calls"):
		g.calls++
		response = fmt.Sprintf(`{"tool_call_id":"call-%d"}`, g.calls)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, response)
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// recordingAmbientStore keeps ambient sessions in memory and records each
// write, so the transcript shows when a session starts, moves and ends.
type recordingAmbientStore struct {
	world  *world
	states map[string]ambient.State
}

func (s *recordingAmbientStore) Load(ctx context.Context, key string) (ambient.State, bool, error) {
	state, ok := s.states[key]
	return state, ok, nil
}

func (s *recordingAmbientStore) Save(ctx context.Context, key string, state ambient.State, ttl time.Duration) error {
	encoded, _ := json.Marshal(state)
	s.world.record("ambient SAVE %s ttl=%s\n%s", key, ttl, indentJSON(string(encoded)))
	s.states[key] = state
	return nil
}

func (s *recordingAmbientStore) Delete(ctx context.Context, key string) error {
	s.world.record("ambient DELETE %s", key)
	delete(s.states, key)
	return nil
}

// persona is the system prompt, kept once in its own golden file so that the
// transcripts stay readable. It is learned from the first request that
// carries it when the golden files are being written, and read from the
// golden file otherwise.
var (
	personaOnce sync.Once
	personaText string
)

func learnPersona(t *testing.T, chatRequest []byte) {
	if !*update {
		return
	}
	var request struct {
		Messages []store.Message `json:"messages"`
	}
	if err := json.Unmarshal(chatRequest, &request); err != nil || len(request.Messages) == 0 || request.Messages[0].Role != "system" {
		return
	}
	personaOnce.Do(func() {
		personaText = request.Messages[0].Content
		if err := os.WriteFile(filepath.Join("testdata", personaGolden), []byte(personaText), 0o644); err != nil {
			t.Fatalf("write persona golden file: %v", err)
		}
	})
}

func personaJSON(t *testing.T) string {
	t.Helper()
	if !*update {
		personaOnce.Do(func() {
			text, err := os.ReadFile(filepath.Join("testdata", personaGolden))
			if err != nil {
				t.Fatalf("read persona golden file (run with -update to create it): %v", err)
			}
			personaText = string(text)
		})
	}
	if personaText == "" {
		t.Fatal("the persona prompt is not known yet: the first scenario must make a chat-completion request")
	}
	encoded, err := json.Marshal(personaText)
	if err != nil {
		t.Fatalf("encode persona: %v", err)
	}
	return string(encoded)
}

func indentJSON(raw string) string {
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return out.String()
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("create stdout capture file: %v", err)
	}
	previous := os.Stdout
	os.Stdout = file
	func() {
		defer func() { os.Stdout = previous }()
		fn()
	}()
	if err := file.Close(); err != nil {
		t.Fatalf("close stdout capture file: %v", err)
	}
	captured, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatalf("read stdout capture file: %v", err)
	}
	return string(captured)
}

func nonEmptyLines(text string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func firstDifference(want string, got string) string {
	wantLines, gotLines := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wantLines) || i < len(gotLines); i++ {
		var wantLine, gotLine string
		if i < len(wantLines) {
			wantLine = wantLines[i]
		}
		if i < len(gotLines) {
			gotLine = gotLines[i]
		}
		if wantLine != gotLine {
			return fmt.Sprintf("first difference at line %d:\n  golden: %s\n  now:    %s", i+1, wantLine, gotLine)
		}
	}
	return "no line differs"
}
