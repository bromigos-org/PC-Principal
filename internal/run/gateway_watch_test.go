package run

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bromigos-org/pc-principal/internal/gateway"
)

type fakeGatewaySession struct {
	observation gateway.Observation
}

func (s *fakeGatewaySession) Observe() gateway.Observation { return s.observation }

func getHealth(t *testing.T, monitor *gateway.Monitor) (int, healthResponse) {
	t.Helper()
	recorder := httptest.NewRecorder()
	healthHandler(monitor)(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	var body healthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode health body %q: %v", recorder.Body.String(), err)
	}
	return recorder.Code, body
}

func TestHealthHandler_Fails_once_the_gateway_has_been_silent_too_long(t *testing.T) {
	now := time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)
	session := &fakeGatewaySession{}
	monitor := gateway.NewMonitor(gateway.DefaultConfig(), func() time.Time { return now })
	monitor.Attach(session)

	// Not connected yet: the process is up, so the probe passes.
	code, body := getHealth(t, monitor)
	if code != http.StatusOK || body.Status != "degraded" || body.Checks["discord"] != "not_ready" {
		t.Fatalf("health while starting = %d %+v, want 200 degraded not_ready", code, body)
	}

	now = now.Add(10 * time.Second)
	session.observation = gateway.Observation{Available: true, Connected: true, LastHeartbeatAck: now}
	code, body = getHealth(t, monitor)
	if code != http.StatusOK || body.Status != "ok" || body.Checks["discord"] != "ready" {
		t.Fatalf("health while connected = %d %+v, want 200 ok ready", code, body)
	}

	// The reconnect is stuck: the session can no longer be read.
	session.observation = gateway.Observation{}
	now = now.Add(4 * time.Minute)
	code, body = getHealth(t, monitor)
	if code != http.StatusOK || body.Status != "degraded" || body.Checks["discord"] != "not_ready" {
		t.Fatalf("health 4m into the outage = %d %+v, want 200 degraded not_ready", code, body)
	}

	now = now.Add(time.Minute + time.Second)
	code, body = getHealth(t, monitor)
	if code != http.StatusServiceUnavailable || body.Status != "unhealthy" || body.Checks["discord"] != "not_ready" {
		t.Fatalf("health 5m1s into the outage = %d %+v, want 503 unhealthy not_ready", code, body)
	}
	if got, want := body.Checks["gateway"], "blocked, heartbeat ack age 5m1s"; got != want {
		t.Fatalf("gateway check = %q, want %q", got, want)
	}

	// A new connection makes the probe pass again.
	now = now.Add(time.Second)
	session.observation = gateway.Observation{Available: true, Connected: true, LastHeartbeatAck: now}
	code, body = getHealth(t, monitor)
	if code != http.StatusOK || body.Status != "ok" {
		t.Fatalf("health after reconnect = %d %+v, want 200 ok", code, body)
	}
}

type blockingCloser struct {
	release chan struct{}
}

func (c blockingCloser) Close() error {
	<-c.release
	return nil
}

type instantCloser struct{}

func (instantCloser) Close() error { return nil }

func TestCloseSession_Does_not_wait_for_a_session_that_never_closes(t *testing.T) {
	stuck := blockingCloser{release: make(chan struct{})}
	defer close(stuck.release)

	started := time.Now()
	if closeSession(stuck, 50*time.Millisecond) {
		t.Fatal("closeSession reported a clean close for a session that is stuck")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("closeSession took %s, want it to give up after the timeout", elapsed)
	}

	if !closeSession(instantCloser{}, time.Second) {
		t.Fatal("closeSession reported a timeout for a session that closes at once")
	}
}
