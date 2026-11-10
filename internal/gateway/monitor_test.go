package gateway

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSession stands in for the Discord session. Tests move it through the
// states a real session goes through.
type fakeSession struct {
	mu          sync.Mutex
	observation Observation
}

func (s *fakeSession) Observe() Observation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observation
}

func (s *fakeSession) set(observation Observation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.observation = observation
}

// connect puts the session in the connected state with a heartbeat
// acknowledged at the given time.
func (s *fakeSession) connect(ack time.Time) {
	s.set(Observation{Available: true, Connected: true, LastHeartbeatAck: ack})
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

type logRecorder struct {
	lines []string
}

func (r *logRecorder) logf(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *logRecorder) count(substring string) int {
	count := 0
	for _, line := range r.lines {
		if strings.Contains(line, substring) {
			count++
		}
	}
	return count
}

func testConfig() Config {
	return Config{WarnAfter: 90 * time.Second, UnhealthyAfter: 5 * time.Minute, ExitAfter: 7 * time.Minute, CheckInterval: 15 * time.Second, StatusEvery: 15 * time.Minute}
}

func newTestMonitor(t *testing.T) (*Monitor, *fakeSession, *fakeClock, *logRecorder) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)}
	session := &fakeSession{}
	recorder := &logRecorder{}
	monitor := NewMonitor(testConfig(), clock.Now)
	monitor.logf = recorder.logf
	monitor.Attach(session)
	return monitor, session, clock, recorder
}

// beat simulates a healthy gateway for the given time: a new heartbeat
// acknowledgement before every check.
func beat(monitor *Monitor, session *fakeSession, clock *fakeClock, d time.Duration, hooks Hooks) {
	for elapsed := time.Duration(0); elapsed < d; elapsed += monitor.config.CheckInterval {
		session.connect(clock.advance(monitor.config.CheckInterval))
		monitor.tick(hooks)
	}
}

// idle lets time pass without changing what the session reports.
func idle(monitor *Monitor, clock *fakeClock, d time.Duration, hooks Hooks) Status {
	var status Status
	for elapsed := time.Duration(0); elapsed < d; elapsed += monitor.config.CheckInterval {
		clock.advance(monitor.config.CheckInterval)
		status = monitor.tick(hooks)
	}
	return status
}

func TestMonitor_Starting_is_healthy_until_the_allowance_runs_out(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)}
	monitor := NewMonitor(testConfig(), clock.Now)

	if status := monitor.Check(); status.State != StateStarting || !status.Healthy {
		t.Fatalf("status at start = %+v, want starting and healthy", status)
	}
	clock.advance(5 * time.Minute)
	if status := monitor.Check(); !status.Healthy {
		t.Fatalf("status at the allowance = %+v, want still healthy", status)
	}
	clock.advance(time.Second)
	if status := monitor.Check(); status.State != StateStarting || status.Healthy {
		t.Fatalf("status past the allowance = %+v, want starting and unhealthy", status)
	}
}

func TestMonitor_Connected_session_with_new_acks_stays_healthy(t *testing.T) {
	monitor, session, clock, _ := newTestMonitor(t)

	beat(monitor, session, clock, 2*time.Hour, Hooks{Unhealthy: func(Status) { t.Fatal("unhealthy hook called") }, Exit: func(Status) { t.Fatal("exit hook called") }})

	status := monitor.Check()
	if status.State != StateConnected || !status.Healthy || status.ExitDue || status.Silence != 0 {
		t.Fatalf("status = %+v, want connected, healthy, no silence", status)
	}
}

// The failure of 2026-11-05: the session disconnects, the library's reconnect
// blocks while holding the session lock, and nothing ever changes again.
func TestMonitor_Blocked_reconnect_turns_unhealthy_then_exit_due(t *testing.T) {
	monitor, session, clock, _ := newTestMonitor(t)
	beat(monitor, session, clock, time.Minute, Hooks{})

	session.set(Observation{Available: false})

	if status := idle(monitor, clock, 5*time.Minute, Hooks{}); status.State != StateBlocked || !status.Healthy {
		t.Fatalf("status after 5m blocked = %+v, want blocked and still healthy", status)
	}
	if status := idle(monitor, clock, 15*time.Second, Hooks{}); status.Healthy || status.ExitDue {
		t.Fatalf("status after 5m15s blocked = %+v, want unhealthy, exit not due", status)
	}
	if status := idle(monitor, clock, 2*time.Minute, Hooks{}); status.Healthy || !status.ExitDue {
		t.Fatalf("status after 7m15s blocked = %+v, want unhealthy and exit due", status)
	}
}

func TestMonitor_Disconnected_session_turns_unhealthy(t *testing.T) {
	monitor, session, clock, _ := newTestMonitor(t)
	beat(monitor, session, clock, time.Minute, Hooks{})

	session.set(Observation{Available: true, Connected: false, LastHeartbeatAck: clock.Now()})

	status := idle(monitor, clock, 5*time.Minute+15*time.Second, Hooks{})
	if status.State != StateDisconnected || status.Healthy {
		t.Fatalf("status = %+v, want disconnected and unhealthy", status)
	}
}

// Each reconnect attempt that gets as far as Discord's hello refreshes the
// library's heartbeat timestamp. That must not count while the attempt has
// not produced a connection.
func TestMonitor_Ack_time_of_a_failed_attempt_is_not_proof_of_life(t *testing.T) {
	monitor, session, clock, _ := newTestMonitor(t)
	beat(monitor, session, clock, time.Minute, Hooks{})

	var status Status
	for elapsed := time.Duration(0); elapsed <= 5*time.Minute; elapsed += 15 * time.Second {
		session.set(Observation{Available: true, Connected: false, LastHeartbeatAck: clock.advance(15 * time.Second)})
		status = monitor.tick(Hooks{})
	}
	if status.Healthy {
		t.Fatalf("status = %+v, want unhealthy", status)
	}
}

func TestMonitor_Open_connection_without_new_acks_is_stalled(t *testing.T) {
	monitor, session, clock, _ := newTestMonitor(t)
	beat(monitor, session, clock, time.Minute, Hooks{})

	if status := idle(monitor, clock, 90*time.Second, Hooks{}); status.State != StateConnected || !status.Healthy {
		t.Fatalf("status after 90s without an ack = %+v, want connected and healthy", status)
	}
	if status := idle(monitor, clock, 15*time.Second, Hooks{}); status.State != StateStalled || !status.Healthy {
		t.Fatalf("status after 105s without an ack = %+v, want stalled and healthy", status)
	}
	if status := idle(monitor, clock, 4*time.Minute, Hooks{}); status.State != StateStalled || status.Healthy {
		t.Fatalf("status after 5m45s without an ack = %+v, want stalled and unhealthy", status)
	}
}

func TestMonitor_Reconnect_restores_health(t *testing.T) {
	monitor, session, clock, _ := newTestMonitor(t)
	beat(monitor, session, clock, time.Minute, Hooks{})
	session.set(Observation{Available: false})
	if status := idle(monitor, clock, 6*time.Minute, Hooks{}); status.Healthy {
		t.Fatalf("status = %+v, want unhealthy before the reconnect", status)
	}

	session.connect(clock.advance(time.Second))

	status := monitor.Check()
	if status.State != StateConnected || !status.Healthy || status.ExitDue {
		t.Fatalf("status after reconnect = %+v, want connected and healthy", status)
	}
}

func TestMonitor_Tick_calls_unhealthy_once_and_exit_when_due(t *testing.T) {
	monitor, session, clock, recorder := newTestMonitor(t)
	unhealthyCalls, exitCalls := 0, 0
	hooks := Hooks{Unhealthy: func(Status) { unhealthyCalls++ }, Exit: func(Status) { exitCalls++ }}
	beat(monitor, session, clock, time.Minute, hooks)
	session.set(Observation{Available: false})

	idle(monitor, clock, 5*time.Minute, hooks)
	if unhealthyCalls != 0 || exitCalls != 0 {
		t.Fatalf("hooks at 5m: unhealthy=%d exit=%d, want none", unhealthyCalls, exitCalls)
	}
	idle(monitor, clock, 2*time.Minute, hooks)
	if unhealthyCalls != 1 || exitCalls != 0 {
		t.Fatalf("hooks at 7m: unhealthy=%d exit=%d, want one unhealthy call and no exit", unhealthyCalls, exitCalls)
	}
	idle(monitor, clock, 15*time.Second, hooks)
	if unhealthyCalls != 1 || exitCalls != 1 {
		t.Fatalf("hooks at 7m15s: unhealthy=%d exit=%d, want one of each", unhealthyCalls, exitCalls)
	}

	// 90s of silence pass without a line; every check after that logs the
	// state and the heartbeat's age.
	if got, want := recorder.count("Discord gateway silent: state=blocked"), 23; got != want {
		t.Fatalf("silent log lines = %d, want %d: %q", got, want, recorder.lines)
	}
	if !strings.Contains(recorder.lines[len(recorder.lines)-1], "heartbeat ack age 7m15s") {
		t.Fatalf("last log line = %q, want the heartbeat's age", recorder.lines[len(recorder.lines)-1])
	}
}

func TestMonitor_Tick_logs_recovery_and_reports_unhealthy_again_next_time(t *testing.T) {
	monitor, session, clock, recorder := newTestMonitor(t)
	unhealthyCalls := 0
	hooks := Hooks{Unhealthy: func(Status) { unhealthyCalls++ }}
	beat(monitor, session, clock, time.Minute, hooks)

	session.set(Observation{Available: true, Connected: false})
	idle(monitor, clock, 6*time.Minute, hooks)
	beat(monitor, session, clock, time.Minute, hooks)
	session.set(Observation{Available: true, Connected: false})
	idle(monitor, clock, 6*time.Minute, hooks)

	if unhealthyCalls != 2 {
		t.Fatalf("unhealthy calls = %d, want 2", unhealthyCalls)
	}
	if got := recorder.count("Discord gateway recovered: state=connected"); got != 1 {
		t.Fatalf("recovery log lines = %d, want 1: %q", got, recorder.lines)
	}
}

func TestMonitor_Tick_logs_a_status_line_while_healthy(t *testing.T) {
	monitor, session, clock, recorder := newTestMonitor(t)

	beat(monitor, session, clock, time.Hour, Hooks{})

	if got := recorder.count("Discord gateway ok: state=connected, heartbeat ack age 0s"); got != 4 {
		t.Fatalf("status log lines in one hour = %d, want 4: %q", got, recorder.lines)
	}
	if len(recorder.lines) != 4 {
		t.Fatalf("log lines = %q, want only the status lines", recorder.lines)
	}
}
