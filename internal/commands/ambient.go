package commands

import "github.com/bromigos-org/pc-principal/internal/ambient"

var ambientManager *ambient.Manager

func ConfigureAmbient(manager *ambient.Manager) {
	ambientManager = manager
}
