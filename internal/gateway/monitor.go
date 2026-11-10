// Package gateway watches the Discord gateway connection from the outside and
// decides when it has been silent for too long.
//
// The Discord library reconnects on its own, but a reconnect can block forever
// inside the library without returning an error or logging anything. The
// process then stays up while the bot is offline. The monitor does not trust
// the library's events for that reason. It polls the session, treats a new
// heartbeat acknowledgement as the only proof of life, and reports how long
// the gateway has been silent so the caller can fail its health check and
// restart.
//
// The package has no Discord dependency apart from the small adapter in
// discordgo.go.
package gateway

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"
)

// Observation is one non-blocking look at the Discord session.
type Observation struct {
	// Available is false when the session could not be read without waiting.
	// The library holds its lock for the whole of a connection attempt, so a
	// session that stays unavailable is a connection attempt that is stuck.
	Available bool
	// Connected reports an open gateway connection with a running heartbeat
	// loop.
	Connected bool
	// LastHeartbeatAck is when Discord last acknowledged a heartbeat.
	LastHeartbeatAck time.Time
}

// Session is what the monitor needs from a Discord session. Observe must
// never block.
type Session interface {
	Observe() Observation
}

// State names what the monitor saw on its last look.
type State string

const (
	// StateStarting means no session has connected yet.
	StateStarting State = "starting"
	// StateConnected means the gateway is open and heartbeats are acknowledged.
	StateConnected State = "connected"
	// StateDisconnected means the library reports no open gateway connection.
	StateDisconnected State = "disconnected"
	// StateStalled means the connection looks open but Discord has stopped
	// acknowledging heartbeats.
	StateStalled State = "stalled"
	// StateBlocked means the session could not be read: the library is in the
	// middle of opening or closing the connection, or is stuck doing so.
	StateBlocked State = "blocked"
)

// Config sets the monitor's thresholds.
type Config struct {
	// WarnAfter is how long the gateway may be silent before the monitor logs
	// it on every check. Discord acknowledges a heartbeat about every 41
	// seconds, so the default means at least one was missed.
	WarnAfter time.Duration
	// UnhealthyAfter is how long the gateway may be silent before Status
	// reports unhealthy. The default leaves the library time to notice missed
	// heartbeats and reconnect on its own first.
	UnhealthyAfter time.Duration
	// ExitAfter is how long the gateway may be silent before the monitor asks
	// the process to exit. It is the backstop for deployments that do not
	// restart on a failing health check.
	ExitAfter time.Duration
	// CheckInterval is how often Run looks at the session.
	CheckInterval time.Duration
	// StatusEvery is how often Run logs a line while the gateway is fine, so
	// a quiet log can be told apart from a dead bot.
	StatusEvery time.Duration
}

// DefaultConfig returns the thresholds the bot runs with.
func DefaultConfig() Config {
	return Config{
		WarnAfter:      90 * time.Second,
		UnhealthyAfter: 5 * time.Minute,
		ExitAfter:      7 * time.Minute,
		CheckInterval:  15 * time.Second,
		StatusEvery:    15 * time.Minute,
	}
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig()
	if c.WarnAfter <= 0 {
		c.WarnAfter = defaults.WarnAfter
	}
	if c.UnhealthyAfter <= 0 {
		c.UnhealthyAfter = defaults.UnhealthyAfter
	}
	if c.ExitAfter <= 0 {
		c.ExitAfter = defaults.ExitAfter
	}
	if c.CheckInterval <= 0 {
		c.CheckInterval = defaults.CheckInterval
	}
	if c.StatusEvery <= 0 {
		c.StatusEvery = defaults.StatusEvery
	}
	return c
}

// Status is the monitor's verdict at one moment.
type Status struct {
	State State
	// Silence is how long ago the gateway last proved it was alive: the time
	// since the monitor last saw a new heartbeat acknowledgement on a
	// connected session, or since it started if it has not seen one. It is
	// measured on the monitor's own clock, so it can read up to one check
	// interval short and does not move when the wall clock is stepped.
	Silence time.Duration
	// Healthy is false once Silence passes Config.UnhealthyAfter.
	Healthy bool
	// ExitDue is true once Silence passes Config.ExitAfter.
	ExitDue bool
}

// Hooks are called by Run. Either may be nil.
type Hooks struct {
	// Unhealthy is called once each time the gateway turns unhealthy.
	Unhealthy func(Status)
	// Exit is called on every check once the gateway has been silent for
	// Config.ExitAfter. It is expected to end the process.
	Exit func(Status)
}

// Monitor tracks how long the Discord gateway has been silent.
type Monitor struct {
	config Config
	now    func() time.Time
	logf   func(format string, args ...any)

	mu            sync.Mutex
	session       Session
	lastAck       time.Time
	lastAlive     time.Time
	everConnected bool

	// Only Run's goroutine touches these.
	warning       bool
	unhealthy     bool
	lastStatusLog time.Time
}

// NewMonitor returns a monitor whose silence clock starts now. A nil now uses
// the wall clock.
func NewMonitor(config Config, now func() time.Time) *Monitor {
	if now == nil {
		now = time.Now
	}
	start := now()
	return &Monitor{config: config.withDefaults(), now: now, logf: log.Printf, lastAlive: start, lastStatusLog: start}
}

// Attach gives the monitor the session to watch. Until then the gateway
// counts as starting, and the silence clock keeps running.
func (m *Monitor) Attach(session Session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.session = session
}

// Check looks at the session once and returns the verdict. It never blocks,
// so a health endpoint can call it directly.
func (m *Monitor) Check() Status {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	state := StateStarting
	if m.session != nil {
		observation := m.session.Observe()
		switch {
		case !observation.Available:
			state = StateBlocked
		case !observation.Connected:
			if m.everConnected {
				state = StateDisconnected
			}
		default:
			m.everConnected = true
			if !observation.LastHeartbeatAck.Equal(m.lastAck) {
				m.lastAck = observation.LastHeartbeatAck
				m.lastAlive = now
			}
			state = StateConnected
		}
	}

	silence := now.Sub(m.lastAlive)
	if state == StateConnected && silence > m.config.WarnAfter {
		state = StateStalled
	}
	return Status{
		State:   state,
		Silence: silence,
		Healthy: silence <= m.config.UnhealthyAfter,
		ExitDue: silence > m.config.ExitAfter,
	}
}

// Run checks the session every Config.CheckInterval until ctx is done. It
// logs the heartbeat's age while the gateway is silent and calls the hooks
// when the thresholds pass.
func (m *Monitor) Run(ctx context.Context, hooks Hooks) {
	ticker := time.NewTicker(m.config.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tick(hooks)
		}
	}
}

// tick is one pass of Run.
func (m *Monitor) tick(hooks Hooks) Status {
	status := m.Check()
	now := m.now()

	if status.Silence <= m.config.WarnAfter {
		if m.warning {
			m.logf("Discord gateway recovered: state=%s, heartbeat ack age %s", status.State, age(status.Silence))
			m.lastStatusLog = now
		} else if status.State == StateConnected && now.Sub(m.lastStatusLog) >= m.config.StatusEvery {
			m.logf("Discord gateway ok: state=%s, heartbeat ack age %s", status.State, age(status.Silence))
			m.lastStatusLog = now
		}
		m.warning = false
		m.unhealthy = false
		return status
	}

	m.warning = true
	m.logf("Discord gateway silent: state=%s, heartbeat ack age %s (health check fails after %s, process exits after %s)", status.State, age(status.Silence), m.config.UnhealthyAfter, m.config.ExitAfter)
	if !status.Healthy && !m.unhealthy {
		m.unhealthy = true
		if hooks.Unhealthy != nil {
			hooks.Unhealthy(status)
		}
	}
	if status.ExitDue && hooks.Exit != nil {
		hooks.Exit(status)
	}
	return status
}

// Describe renders a status for a health response or a log line.
func (s Status) Describe() string {
	return fmt.Sprintf("%s, heartbeat ack age %s", s.State, age(s.Silence))
}

func age(d time.Duration) time.Duration {
	return d.Round(time.Second)
}
