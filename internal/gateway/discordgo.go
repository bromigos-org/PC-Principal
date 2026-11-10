package gateway

import (
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// DiscordGoSession adapts a discordgo session to Session.
type DiscordGoSession struct {
	session *discordgo.Session
}

func NewDiscordGoSession(session *discordgo.Session) DiscordGoSession {
	return DiscordGoSession{session: session}
}

// Observe reads the session without waiting for its lock. discordgo holds
// that lock for the whole of Session.Open, including the reads that have no
// deadline, so a plain RLock here would hang exactly when the answer matters.
func (d DiscordGoSession) Observe() Observation {
	if d.session == nil || !d.session.TryRLock() {
		return Observation{}
	}
	defer d.session.RUnlock()
	return Observation{
		Available: true,
		// DataReady is cleared when the websocket closes and set again by the
		// heartbeat loop of the next open connection.
		Connected:        d.session.DataReady,
		LastHeartbeatAck: d.session.LastHeartbeatAck,
	}
}

// EnableConnectionLogging makes discordgo's gateway connect, resume and
// reconnect messages visible. By default the library logs errors only, so a
// reconnect that never finishes leaves no trace of how far it got.
//
// It raises the session's log level and installs a logger for the whole
// discordgo package that keeps errors as before and adds only the connection
// messages, in the library's own line format.
func EnableConnectionLogging(session *discordgo.Session) {
	session.LogLevel = discordgo.LogInformational
	installLogger.Do(func() { discordgo.Logger = logConnection })
}

var installLogger sync.Once

func logConnection(level, caller int, format string, args ...any) {
	if !isConnectionLog(level, format) {
		return
	}
	// One frame further out than discordgo's own logger, which runs inside
	// the function that calls this one.
	pc, file, line, _ := runtime.Caller(caller + 1)
	name := ""
	if fn := runtime.FuncForPC(pc); fn != nil {
		name = fn.Name()
		name = name[strings.LastIndex(name, ".")+1:]
	}
	log.Printf("[DG%d] %s:%d:%s() %s", level, file[strings.LastIndex(file, "/")+1:], line, name, fmt.Sprintf(format, args...))
}

// isConnectionLog reports whether a discordgo log message should be printed:
// every error, plus the warnings and informational messages about the gateway
// connection. Everything else at those levels is noise, and some of it dumps
// whole event payloads.
func isConnectionLog(level int, format string) bool {
	if level <= discordgo.LogError {
		return true
	}
	if level > discordgo.LogInformational {
		return false
	}
	text := strings.ToLower(format)
	return strings.Contains(text, "reconnect") || strings.Contains(text, "gateway")
}
