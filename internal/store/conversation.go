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

	keyPrefix = "conversation:"
)

// Message is a single chat turn, shared between the store and LiteLLM requests.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// History keeps conversations on a Redis-compatible server, each under
// "conversation:" and an id, with limits of its own. The package-level
// Exists, Get and Save are the History the Discord bot has always used.
type History struct {
	// Client is the server the conversations are kept on.
	Client redis.Cmdable
	// TTL is how long a conversation is kept after it was last saved.
	TTL time.Duration
	// MaxHistory is the number of messages kept besides the system prompt.
	MaxHistory int
}

var client *redis.Client

func Init(addr string) {
	client = redis.NewClient(&redis.Options{Addr: addr})
}

// Client exposes the underlying redis client so other packages (notably the
// health probe) can ping the connection. Returns nil if Init was never called,
// letting callers treat that as "Dragonfly not configured" rather than an error.
func Client() *redis.Client { return client }

func key(threadID string) string { return keyPrefix + threadID }

// shared is the History of the package-level functions.
func shared() History {
	return History{Client: client, TTL: ttl, MaxHistory: maxHistory}
}

// Exists reports whether a conversation is tracked for the given thread.
func Exists(ctx context.Context, threadID string) (bool, error) {
	if client == nil {
		return false, nil
	}
	return shared().Exists(ctx, threadID)
}

// Get loads the full message history for a thread. Returns nil if not found.
func Get(ctx context.Context, threadID string) ([]Message, error) {
	if client == nil {
		return nil, nil
	}
	return shared().Get(ctx, threadID)
}

// Save persists history and resets the TTL. The system prompt at index 0 is
// always preserved; older messages beyond maxHistory are dropped.
func Save(ctx context.Context, threadID string, msgs []Message) error {
	if client == nil {
		return nil
	}
	return shared().Save(ctx, threadID, msgs)
}

// Exists reports whether a conversation is kept under the id.
func (h History) Exists(ctx context.Context, threadID string) (bool, error) {
	n, err := h.Client.Exists(ctx, key(threadID)).Result()
	return n > 0, err
}

// Get loads the conversation kept under the id. It returns nil when there
// is none.
func (h History) Get(ctx context.Context, threadID string) ([]Message, error) {
	val, err := h.Client.Get(ctx, key(threadID)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var msgs []Message
	return msgs, json.Unmarshal([]byte(val), &msgs)
}

// Save keeps the conversation under the id for another TTL. The system
// prompt at index 0 is always kept; older messages beyond MaxHistory are
// dropped.
func (h History) Save(ctx context.Context, threadID string, msgs []Message) error {
	if len(msgs) > h.MaxHistory+1 {
		msgs = append(msgs[:1], msgs[len(msgs)-h.MaxHistory:]...)
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		return err
	}
	return h.Client.Set(ctx, key(threadID), b, h.TTL).Err()
}
