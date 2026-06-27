package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	ttl        = 24 * time.Hour
	maxHistory = 40 // max non-system messages to keep per thread
)

// Message is a single chat turn, shared between the store and LiteLLM requests.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

var client *redis.Client

func Init(addr string) {
	client = redis.NewClient(&redis.Options{Addr: addr})
}

// Client exposes the underlying redis client so other packages (notably the
// health probe) can ping the connection. Returns nil if Init was never called,
// letting callers treat that as "Dragonfly not configured" rather than an error.
func Client() *redis.Client { return client }

func key(threadID string) string { return "conversation:" + threadID }

// Exists reports whether a conversation is tracked for the given thread.
func Exists(ctx context.Context, threadID string) (bool, error) {
	if client == nil {
		return false, nil
	}
	n, err := client.Exists(ctx, key(threadID)).Result()
	return n > 0, err
}

// Get loads the full message history for a thread. Returns nil if not found.
func Get(ctx context.Context, threadID string) ([]Message, error) {
	if client == nil {
		return nil, nil
	}
	val, err := client.Get(ctx, key(threadID)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var msgs []Message
	return msgs, json.Unmarshal([]byte(val), &msgs)
}

// Save persists history and resets the TTL. The system prompt at index 0 is
// always preserved; older messages beyond maxHistory are dropped.
func Save(ctx context.Context, threadID string, msgs []Message) error {
	if client == nil {
		return nil
	}
	if len(msgs) > maxHistory+1 {
		msgs = append(msgs[:1], msgs[len(msgs)-maxHistory:]...)
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	return client.Set(ctx, key(threadID), b, ttl).Err()
}
