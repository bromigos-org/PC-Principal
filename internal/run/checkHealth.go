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
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	checks := map[string]string{
		"discord":   "down",
		"dragonfly": "skipped",
	}

	overall := http.StatusOK
	status := "ok"

	// --- Discord gateway check ---
	if discordReady.Load() {
		checks["discord"] = "ready"
	} else {
		checks["discord"] = "not_ready"
		overall = http.StatusServiceUnavailable
		status = "degraded"
	}

	// --- DragonflyDB (Redis) check, only if configured ---
	if c := store.Client(); c != nil {
		pingCtx, pingCancel := context.WithTimeout(ctx, 1*time.Second)
		err := c.Ping(pingCtx).Err()
		pingCancel()
		if err != nil {
			checks["dragonfly"] = "unreachable: " + err.Error()
			overall = http.StatusServiceUnavailable
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
