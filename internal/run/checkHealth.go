package run

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/bromigos-org/pc-principal/internal/store"
)

// discordReady flips to true once the Discord gateway has emitted a Ready event.
// It is reset on disconnect so the probe accurately reports live state.
var discordReady atomic.Bool

// SetDiscordReady is called by the onReady / onDisconnect / onReconnect handlers
// in run.go so the health probe reflects the actual gateway state, not just
// "the process is up".
func SetDiscordReady(ready bool) { discordReady.Store(ready) }

// healthResponse is the JSON body returned by /health.
type healthResponse struct {
	Status    string            `json:"status"`
	Checks    map[string]string `json:"checks"`
	CheckedAt time.Time         `json:"checkedAt"`
}

func healthCheckHandler(w http.ResponseWriter, r *http.Request) {
	// Uptime / liveness signal: process is up and serving.
	// Discord + DragonflyDB are reported as informational fields so the
	// dashboard dot turns green as soon as the HTTP server is reachable,
	// while a degraded status can still be surfaced through the JSON body.
	checks := map[string]string{
		"discord":   "unknown",
		"dragonfly": "skipped",
	}

	overall := http.StatusOK
	status := "ok"

	// --- Discord gateway check (informational) ---
	if discordReady.Load() {
		checks["discord"] = "ready"
	} else {
		checks["discord"] = "not_ready"
		status = "degraded"
	}

	// --- DragonflyDB (Redis) check (informational) ---
	if c := store.Client(); c != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 1*time.Second)
		err := c.Ping(ctx).Err()
		cancel()
		if err != nil {
			checks["dragonfly"] = "unreachable: " + err.Error()
			status = "degraded"
		} else {
			checks["dragonfly"] = "ok"
		}
	}

	body := healthResponse{
		Status:    status,
		Checks:    checks,
		CheckedAt: time.Now().UTC(),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(overall)
	_ = json.NewEncoder(w).Encode(body)
}

func StartHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthCheckHandler)
	mux.HandleFunc("/healthz", healthCheckHandler) // k8s convention alias

	go func() {
		if err := http.ListenAndServe(":8080", mux); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()
}
