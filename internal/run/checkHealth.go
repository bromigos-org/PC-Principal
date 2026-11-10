package run

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/bromigos-org/pc-principal/internal/gateway"
	"github.com/bromigos-org/pc-principal/internal/store"
)

// gatewayMonitor decides whether the Discord gateway is alive. It exists from
// process start, so the time before the first connection counts as silence
// too; Init attaches the session once there is one.
var gatewayMonitor = gateway.NewMonitor(gateway.DefaultConfig(), nil)

// healthResponse is the JSON body returned by /health.
type healthResponse struct {
	Status    string            `json:"status"`
	Checks    map[string]string `json:"checks"`
	CheckedAt time.Time         `json:"checkedAt"`
}

// healthHandler serves /health. It answers 200 while the process is up and
// the Discord gateway is alive or has only just gone quiet, and 503 once the
// gateway has been silent for longer than the monitor allows, so that a
// liveness probe restarts a bot that is running but offline.
//
// A missing connection that is still inside the allowance, and an
// unreachable DragonflyDB, are reported as "degraded" in the body with a 200:
// neither is fixed by a restart.
func healthHandler(monitor *gateway.Monitor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		checks := map[string]string{
			"discord":   "not_ready",
			"dragonfly": "skipped",
		}

		overall := http.StatusOK
		status := "ok"

		// --- Discord gateway check ---
		gatewayStatus := monitor.Check()
		checks["gateway"] = gatewayStatus.Describe()
		if gatewayStatus.State == gateway.StateConnected {
			checks["discord"] = "ready"
		} else {
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

		if !gatewayStatus.Healthy {
			overall = http.StatusServiceUnavailable
			status = "unhealthy"
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
}

// platformHealth is the health handler of a front end other than Discord,
// once that front end has started. The health server starts before the
// platform is known, so it asks on every request.
var platformHealth atomic.Pointer[http.HandlerFunc]

// serveHealth answers with the running front end's health: Discord's,
// unless another front end has put its own in place.
func serveHealth(w http.ResponseWriter, r *http.Request) {
	if handler := platformHealth.Load(); handler != nil {
		(*handler)(w, r)
		return
	}
	healthHandler(gatewayMonitor)(w, r)
}

func StartHTTPServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", serveHealth)
	mux.HandleFunc("/healthz", serveHealth) // k8s convention alias

	go func() {
		if err := http.ListenAndServe(":8080", mux); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()
}
