package main

import (
	"github.com/bromigos-org/pc-principal/internal/run"
)

func main() {
	run.StartHTTPServer() // Start the HTTP server for health checks
	run.Init()
}
