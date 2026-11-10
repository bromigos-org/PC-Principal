package run

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bromigos-org/pc-principal/internal/brodec"
	"github.com/bromigos-org/pc-principal/internal/llm"
	"github.com/redis/go-redis/v9"
)

const (
	// platformEnv picks the chat platform the process serves. Unset means
	// Discord.
	platformEnv     = "PCP_PLATFORM"
	platformDiscord = "discord"
	platformBRODEC  = "brodec"

	brodecShutdownTimeout = 10 * time.Second
)

// selectedPlatform reads the value of PCP_PLATFORM. A value that names no
// platform is an error: starting the Discord bot because of a typo would be
// the wrong surprise.
func selectedPlatform(value string) (string, error) {
	switch platform := strings.ToLower(strings.TrimSpace(value)); platform {
	case "", platformDiscord:
		return platformDiscord, nil
	case platformBRODEC:
		return platformBRODEC, nil
	default:
		return "", fmt.Errorf("%s=%q names no platform: use %q or %q", platformEnv, value, platformDiscord, platformBRODEC)
	}
}

// brodecFrontEnd is the BRODEC front end while it runs.
type brodecFrontEnd struct {
	bot      *brodec.Bot
	server   *http.Server
	listener net.Listener
	redis    *redis.Client
}

// runBRODEC runs the BRODEC front end in place of the Discord one, until
// the process is told to stop.
//
// Nothing of Discord's is started: no Discord session, so no token is
// needed, no event ingestion, no backfill. The long-term memory service is
// not configured in this mode at all; its settings are not even read.
func runBRODEC() {
	frontEnd, err := startBRODEC()
	if err != nil {
		log.Fatalf("BRODEC front end: %v", err)
	}
	go func() {
		if err := frontEnd.server.Serve(frontEnd.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("BRODEC event endpoint: %v", err)
		}
	}()
	log.Printf("BRODEC front end is running; the gateway delivers to http://%s%s. Press CTRL-C to exit.", frontEnd.listener.Addr(), brodec.EventsPath)

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc
	frontEnd.stop()
}

// startBRODEC builds the front end from the environment and opens its
// listener.
func startBRODEC() (*brodecFrontEnd, error) {
	config := brodec.LoadConfigFromEnv()
	addr := os.Getenv("DRAGONFLY_ADDR")
	if addr == "" {
		return nil, errors.New("DRAGONFLY_ADDR is not set: on BRODEC the short context, the polls and the once-a-day notice are kept there")
	}
	// A client of its own, on the same server the Discord front end uses.
	// The keys differ: everything here is under "conversation:brodec:" or
	// "brodec:".
	client := redis.NewClient(&redis.Options{Addr: addr})
	log.Printf("Connected to DragonflyDB at %s", addr)

	bot, err := brodec.New(config, brodec.Deps{Redis: client, LLM: llm.NewLiteLLMClient(nil)})
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	listener, err := net.Listen("tcp", config.Listen)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("listen for the gateway on %s: %w", config.Listen, err)
	}
	mux := http.NewServeMux()
	mux.Handle(brodec.EventsPath, bot)
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}
	log.Printf("BRODEC allowlists: %d rooms, %d people in direct messages, %d admins", len(config.AllowedRooms), len(config.AllowedDMUsers), len(config.Admins))

	handler := brodecHealthHandler(bot, client)
	platformHealth.Store(&handler)
	return &brodecFrontEnd{bot: bot, server: server, listener: listener, redis: client}, nil
}

// stop closes the endpoint and waits for answers that are on their way.
func (f *brodecFrontEnd) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), brodecShutdownTimeout)
	defer cancel()
	_ = f.server.Shutdown(ctx)
	f.bot.Shutdown(ctx)
	platformHealth.Store(nil)
	_ = f.redis.Close()
}

// brodecHealthHandler serves /health for the BRODEC front end: the state of
// the gateway beside the bot, where the Discord front end reports Discord's.
//
// It always answers 200. The gateway is another container; restarting the
// bot does not bring it back, so its absence is reported as "degraded" and
// left to the gateway's own supervisor.
func brodecHealthHandler(bot *brodec.Bot, dragonfly *redis.Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		health := bot.Health(ctx)

		checks := map[string]string{
			"gateway":       health.Gateway,
			"gateway_key":   health.Key,
			"last_delivery": "none",
			"dragonfly":     "ok",
		}
		if !health.LastDelivery.IsZero() {
			checks["last_delivery"] = health.LastDelivery.UTC().Format(time.RFC3339)
		}
		status := "ok"
		if !health.Ready {
			status = "degraded"
		}
		if err := dragonfly.Ping(ctx).Err(); err != nil {
			checks["dragonfly"] = "unreachable"
			status = "degraded"
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: status, Checks: checks, CheckedAt: time.Now().UTC()})
	}
}
