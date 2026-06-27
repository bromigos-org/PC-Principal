package llm

import (
	"context"

	"github.com/bromigos-org/pc-principal/internal/store"
)

type Message = store.Message

type Client interface {
	Generate(ctx context.Context, messages []store.Message) (string, error)
}
