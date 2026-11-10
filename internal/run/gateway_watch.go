package run

import (
	"context"
	"log"
	"os"
	"runtime/pprof"
	"time"

	"github.com/bromigos-org/pc-principal/internal/gateway"
	"github.com/bwmarrin/discordgo"
)

// sessionCloseTimeout bounds the shutdown of the Discord session.
const sessionCloseTimeout = 5 * time.Second

// watchGateway starts the watchdog for the Discord gateway. It must run
// before the session is opened, so that a first connection that hangs is
// caught as well.
func watchGateway(session *discordgo.Session) {
	gateway.EnableConnectionLogging(session)
	gatewayMonitor.Attach(gateway.NewDiscordGoSession(session))
	go gatewayMonitor.Run(context.Background(), gateway.Hooks{Unhealthy: onGatewayUnhealthy, Exit: onGatewayExit})
}

// onGatewayUnhealthy records where every goroutine is at the moment the
// gateway is given up on. A reconnect that hangs inside the Discord library
// logs nothing, so this dump is the only evidence of where it stopped.
func onGatewayUnhealthy(status gateway.Status) {
	log.Printf("Discord gateway is unhealthy (%s); /health now returns 503. Goroutine dump follows.", status.Describe())
	_ = pprof.Lookup("goroutine").WriteTo(os.Stderr, 1)
}

// onGatewayExit ends the process so that its supervisor starts a fresh one.
// It is the backstop for a deployment with no liveness probe on /health.
func onGatewayExit(status gateway.Status) {
	log.Printf("Discord gateway did not recover (%s); exiting so the bot is restarted.", status.Describe())
	os.Exit(1)
}

// closeSession closes the Discord session without waiting forever. Close
// needs the session's lock, and a reconnect stuck inside the library never
// gives it back, which would otherwise hold up shutdown until the process is
// killed.
func closeSession(session interface{ Close() error }, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = session.Close()
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		log.Printf("Discord session did not close within %s; exiting anyway", timeout)
		return false
	}
}
