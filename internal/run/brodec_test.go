package run

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/bromigos-org/pc-principal/internal/brodec"
)

func TestSelectedPlatform(t *testing.T) {
	cases := map[string]struct {
		value   string
		want    string
		wantErr bool
	}{
		"unset means Discord":          {"", platformDiscord, false},
		"discord":                      {"discord", platformDiscord, false},
		"brodec":                       {"brodec", platformBRODEC, false},
		"case and blanks do not count": {"  BRODEC ", platformBRODEC, false},
		"a typo starts nothing":        {"brodek", "", true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// When
			got, err := selectedPlatform(c.value)

			// Then
			if got != c.want || (err != nil) != c.wantErr {
				t.Fatalf("expected %q (error: %v), got %q (%v)", c.want, c.wantErr, got, err)
			}
		})
	}
}

// brodecSurroundings is everything the BRODEC front end talks to, faked,
// and the environment that points it there.
type brodecSurroundings struct {
	private ed25519.PrivateKey
	redis   *miniredis.Miniredis

	memoryCalls atomic.Int64
	modelCalls  atomic.Int64

	mu           sync.Mutex
	gatewayCalls [][]byte
}

const brodecTestRoom = "!kWqPzLmNcXvBtRyUoA:brodec.ai"

func newBRODECSurroundings(t *testing.T) *brodecSurroundings {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s := &brodecSurroundings{private: private, redis: miniredis.RunT(t)}

	dataDir := t.TempDir()
	for name, content := range map[string]string{
		"webhook-public-key": base64.RawStdEncoding.EncodeToString(public),
		"local-api-token":    "token-for-the-test",
	} {
		if err := os.WriteFile(filepath.Join(dataDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// The memory service: configured and switched on in the environment, as
	// it is for the Discord bot. Nothing may ever reach it.
	memoryService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.memoryCalls.Add(1)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(memoryService.Close)

	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.modelCalls.Add(1)
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Sweet, bro."}}]}`)
	}))
	t.Cleanup(model.Close)

	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/public-key" {
			_, _ = io.WriteString(w, base64.RawStdEncoding.EncodeToString(public))
			return
		}
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.gatewayCalls = append(s.gatewayCalls, body)
		s.mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(gateway.Close)

	for name, value := range map[string]string{
		"DISCORD_BOT_TOKEN":         "",
		"GNOSIS_ENABLED":            "true",
		"GNOSIS_SERVICE_URL":        memoryService.URL,
		"GNOSIS_SERVICE_TOKEN":      "a-token-the-brodec-pod-would-not-even-have",
		"LITELLM_BASE_URL":          model.URL,
		"LITELLM_API_KEY":           "model-key",
		"DRAGONFLY_ADDR":            s.redis.Addr(),
		"BRODEC_LISTEN":             "127.0.0.1:0",
		"BRODEC_GATEWAY_URL":        gateway.URL,
		"BRODEC_GATEWAY_DATA":       dataDir,
		"BRODEC_GATEWAY_PUBLIC_KEY": "",
		"BRODEC_ALLOWED_ROOMS":      brodecTestRoom,
		"BRODEC_ALLOWED_DM_USERS":   "",
		"BRODEC_ADMINS":             "",
	} {
		t.Setenv(name, value)
	}
	return s
}

func (s *brodecSurroundings) sent() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]byte(nil), s.gatewayCalls...)
}

// post delivers a body to the front end the way the gateway does.
func (s *brodecSurroundings) post(t *testing.T, frontEnd *brodecFrontEnd, body []byte) *http.Response {
	t.Helper()
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	request, err := http.NewRequest(http.MethodPost, "http://"+frontEnd.listener.Addr().String()+brodec.EventsPath, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Brodec-Timestamp", timestamp)
	request.Header.Set("X-Brodec-Signature", hex.EncodeToString(ed25519.Sign(s.private, append([]byte(timestamp), body...))))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func brodecFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "brodec", "testdata", "wire", "delivery", name))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimRight(content, "\n")
}

func startBRODECForTest(t *testing.T) *brodecFrontEnd {
	t.Helper()
	frontEnd, err := startBRODEC()
	if err != nil {
		t.Fatalf("start the BRODEC front end: %v", err)
	}
	go func() { _ = frontEnd.server.Serve(frontEnd.listener) }()
	t.Cleanup(frontEnd.stop)
	return frontEnd
}

// The BRODEC front end starts without a Discord token, answers a mention
// with the model, and never calls the memory service, even with the
// service configured and switched on in its environment.
func TestBRODEC_answers_a_mention_without_Discord_and_without_the_memory_service(t *testing.T) {
	// Given
	surroundings := newBRODECSurroundings(t)
	frontEnd := startBRODECForTest(t)

	// When: the gateway's check, a mention, a command and a tap
	ping := surroundings.post(t, frontEnd, brodecFixture(t, "ping.json"))
	mention := surroundings.post(t, frontEnd, brodecFixture(t, "mention.json"))
	command := surroundings.post(t, frontEnd, brodecFixture(t, "command.json"))
	tap := surroundings.post(t, frontEnd, brodecFixture(t, "button.json"))
	deadline := time.Now().Add(5 * time.Second)
	for len(surroundings.sent()) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	frontEnd.stop()

	// Then
	for name, response := range map[string]*http.Response{"ping": ping, "mention": mention, "command": command, "tap": tap} {
		if response.StatusCode != http.StatusOK {
			t.Fatalf("the %s was answered %d", name, response.StatusCode)
		}
	}
	sent := surroundings.sent()
	if len(sent) != 1 {
		t.Fatalf("expected one reply through the gateway, got %d", len(sent))
	}
	var call struct {
		Actions []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"actions"`
	}
	if err := json.Unmarshal(sent[0], &call); err != nil || len(call.Actions) != 1 || call.Actions[0].Type != "reply" || call.Actions[0].Text != "Sweet, bro." {
		t.Fatalf("expected the model's reply, got %s", sent[0])
	}
	if got := surroundings.modelCalls.Load(); got != 1 {
		t.Fatalf("expected one model call, for the mention, got %d", got)
	}
	if got := surroundings.memoryCalls.Load(); got != 0 {
		t.Fatalf("expected no call to the memory service, got %d", got)
	}
	// and what it kept is under BRODEC's own keys
	for _, key := range surroundings.redis.Keys() {
		if !bytes.HasPrefix([]byte(key), []byte("conversation:brodec:")) && !bytes.HasPrefix([]byte(key), []byte("brodec:")) {
			t.Fatalf("a key outside BRODEC's own: %q", key)
		}
	}
}

func TestBRODEC_refuses_an_unsigned_delivery(t *testing.T) {
	// Given
	newBRODECSurroundings(t)
	frontEnd := startBRODECForTest(t)

	// When
	response, err := http.Post("http://"+frontEnd.listener.Addr().String()+brodec.EventsPath, "application/json", bytes.NewReader(brodecFixture(t, "ping.json")))

	// Then
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", response.StatusCode)
	}
}

func getPlatformHealth(t *testing.T) (int, healthResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	serveHealth(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body %q: %v", recorder.Body.String(), err)
	}
	return recorder.Code, body
}

func TestHealth_reports_the_gateway_in_place_of_Discord_on_BRODEC(t *testing.T) {
	// Given
	surroundings := newBRODECSurroundings(t)
	frontEnd := startBRODECForTest(t)

	// When
	code, body := getPlatformHealth(t)
	surroundings.redis.Close()
	codeWithoutStore, bodyWithoutStore := getPlatformHealth(t)
	frontEnd.stop()
	_, bodyAfterStop := getPlatformHealth(t)

	// Then
	if code != http.StatusOK || body.Status != "ok" {
		t.Fatalf("expected 200 ok, got %d %+v", code, body)
	}
	if body.Checks["gateway"] != "ready" || body.Checks["gateway_key"] != "loaded" || body.Checks["dragonfly"] != "ok" || body.Checks["last_delivery"] != "none" {
		t.Fatalf("unexpected checks: %+v", body.Checks)
	}
	if _, reportsDiscord := body.Checks["discord"]; reportsDiscord {
		t.Fatalf("the BRODEC front end has no Discord to report: %+v", body.Checks)
	}
	if codeWithoutStore != http.StatusOK || bodyWithoutStore.Status != "degraded" || bodyWithoutStore.Checks["dragonfly"] != "unreachable" {
		t.Fatalf("expected 200 degraded without the store, got %d %+v", codeWithoutStore, bodyWithoutStore)
	}
	// Without a BRODEC front end, /health is Discord's again.
	if _, reportsDiscord := bodyAfterStop.Checks["discord"]; !reportsDiscord {
		t.Fatalf("expected Discord's health once the BRODEC front end is gone: %+v", bodyAfterStop.Checks)
	}
}

func TestBRODEC_does_not_start_without_a_store_or_the_gateways_directory(t *testing.T) {
	for name, missing := range map[string]string{"no store": "DRAGONFLY_ADDR", "no gateway directory": "BRODEC_GATEWAY_DATA"} {
		t.Run(name, func(t *testing.T) {
			// Given
			newBRODECSurroundings(t)
			t.Setenv(missing, "")

			// When
			frontEnd, err := startBRODEC()

			// Then
			if err == nil {
				frontEnd.stop()
				t.Fatal("expected an error")
			}
		})
	}
}
