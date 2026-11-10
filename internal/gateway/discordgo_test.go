package gateway

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/gorilla/websocket"
)

// fakeGateway is a local stand-in for the Discord gateway. The first
// connection is identified and made ready, then dropped on request. Every
// later connection is a resume, which the gateway answers with resumeReply,
// or not at all when resumeReply is empty.
//
// The gateway is never shut down. discordgo has no way to stop a reconnect
// that is in progress, so the session under test stays stuck on this gateway
// for the life of the test binary; closing the gateway would only send that
// session into a retry loop behind the next test's back.
type fakeGateway struct {
	server      *httptest.Server
	resumeReply string
	connections atomic.Int32
	drop        chan struct{}
}

func newFakeGateway(t *testing.T, resumeReply string) *fakeGateway {
	t.Helper()
	gateway := &fakeGateway{resumeReply: resumeReply, drop: make(chan struct{})}
	mux := http.NewServeMux()
	mux.HandleFunc("/gateway", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"url":%q}`, gateway.websocketURL())
	})
	mux.HandleFunc("/ws/", gateway.serve)
	gateway.server = httptest.NewServer(mux)
	return gateway
}

func (g *fakeGateway) websocketURL() string {
	return "ws" + strings.TrimPrefix(g.server.URL, "http") + "/ws/"
}

func (g *fakeGateway) serve(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	number := g.connections.Add(1)
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"op":10,"d":{"heartbeat_interval":41250}}`)); err != nil {
		return
	}
	if _, _, err := conn.ReadMessage(); err != nil { // identify or resume
		return
	}
	if number == 1 {
		ready := fmt.Sprintf(`{"op":0,"s":1,"t":"READY","d":{"v":10,"session_id":"session","resume_gateway_url":%q,"user":{"id":"1"},"guilds":[]}}`, g.websocketURL())
		if err := conn.WriteMessage(websocket.TextMessage, []byte(ready)); err != nil {
			return
		}
		<-g.drop
		return
	}
	if g.resumeReply != "" {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(g.resumeReply))
	}
	select {}
}

// syncBuffer lets the test read log output while other goroutines write it.
type syncBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// observeWithin fails the test if Observe does not return promptly: a health
// endpoint calls it, so it must never wait on the session.
func observeWithin(t *testing.T, session DiscordGoSession) Observation {
	t.Helper()
	result := make(chan Observation, 1)
	go func() { result <- session.Observe() }()
	select {
	case observation := <-result:
		return observation
	case <-time.After(2 * time.Second):
		t.Fatal("Observe blocked")
		return Observation{}
	}
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// These run the real discordgo session against the local fake gateway. They
// cover the two ways a reconnect can leave the bot offline with the process
// still running: the gateway refuses the resume, or it never answers it.
// Either way the session must not look alive, looking at it must not block,
// and the monitor must turn unhealthy.
func TestDiscordGoSession_Reconnect_that_never_completes_is_not_alive(t *testing.T) {
	cases := []struct {
		name        string
		resumeReply string
	}{
		{name: "gateway answers the resume with invalid session", resumeReply: `{"op":9,"d":false}`},
		{name: "gateway never answers the resume", resumeReply: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeGateway(t, tc.resumeReply)

			previousEndpoint, previousOutput := discordgo.EndpointGateway, log.Writer()
			logs := &syncBuffer{}
			log.SetOutput(logs)
			discordgo.EndpointGateway = fake.server.URL + "/gateway"
			t.Cleanup(func() {
				discordgo.EndpointGateway = previousEndpoint
				log.SetOutput(previousOutput)
			})

			session, err := discordgo.New("Bot test-token")
			if err != nil {
				t.Fatalf("discordgo.New: %v", err)
			}
			EnableConnectionLogging(session)
			observer := NewDiscordGoSession(session)
			clock := &fakeClock{now: time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)}
			monitor := NewMonitor(testConfig(), clock.Now)
			monitor.logf = func(string, ...any) {}
			monitor.Attach(observer)

			if err := session.Open(); err != nil {
				t.Fatalf("Open: %v", err)
			}
			waitFor(t, "the session to report a connection", func() bool {
				observation := observeWithin(t, observer)
				return observation.Available && observation.Connected
			})
			if status := monitor.Check(); status.State != StateConnected || !status.Healthy {
				t.Fatalf("status while connected = %+v, want connected and healthy", status)
			}

			close(fake.drop)
			waitFor(t, "the resume attempt", func() bool { return fake.connections.Load() >= 2 })

			// Give the reconnect time to finish if it is ever going to.
			for range 25 {
				time.Sleep(20 * time.Millisecond)
				if observation := observeWithin(t, observer); observation.Available && observation.Connected {
					t.Fatalf("observation = %+v, want a session that is not alive", observation)
				}
			}

			clock.advance(5*time.Minute + time.Second)
			status := monitor.Check()
			output := logs.String()
			if status.Healthy || status.State == StateConnected {
				t.Fatalf("status = %+v, want unhealthy", status)
			}
			t.Logf("session state after the failed resume: %q", status.State)
			t.Logf("connection log:\n%s", output)

			for _, want := range []string{"trying to reconnect to gateway", "sending resume packet to gateway"} {
				if !strings.Contains(output, want) {
					t.Errorf("log output lacks %q:\n%s", want, output)
				}
			}
			if strings.Contains(output, "First Packet") || strings.Contains(output, "test-token") {
				t.Errorf("log output contains an event dump or the token:\n%s", output)
			}
		})
	}
}

func TestIsConnectionLog(t *testing.T) {
	cases := []struct {
		level  int
		format string
		want   bool
	}{
		{discordgo.LogError, "error unmarshalling %s event, %s", true},
		{discordgo.LogError, "error reconnecting to gateway, %s", true},
		{discordgo.LogWarning, "error reading from gateway %s websocket, %s", true},
		{discordgo.LogWarning, "unknown event: Op: %d, Seq: %d, Type: %s, Data: %s", false},
		{discordgo.LogWarning, "Expected READY/RESUMED, instead got:\n%#v\n", false},
		{discordgo.LogInformational, "trying to reconnect to gateway", true},
		{discordgo.LogInformational, "successfully reconnected to gateway", true},
		{discordgo.LogInformational, "connecting to gateway %s", true},
		{discordgo.LogInformational, "sending resume packet to gateway", true},
		{discordgo.LogInformational, "Closing and reconnecting in response to Op9", true},
		{discordgo.LogInformational, "Gateway session is not resumable, discarding its information", true},
		{discordgo.LogInformational, "First Packet:\n%#v\n", false},
		{discordgo.LogInformational, "called", false},
		{discordgo.LogInformational, "Rate Limiting %s, retry in %v", false},
		{discordgo.LogDebug, "sending gateway websocket heartbeat seq %d", false},
	}
	for _, tc := range cases {
		if got := isConnectionLog(tc.level, tc.format); got != tc.want {
			t.Errorf("isConnectionLog(%d, %q) = %v, want %v", tc.level, tc.format, got, tc.want)
		}
	}
}
