package store

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newHistory(t *testing.T, ttl time.Duration, maxHistory int) (History, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return History{Client: rdb, TTL: ttl, MaxHistory: maxHistory}, server
}

func conversationOf(turns int) []Message {
	messages := []Message{{Role: "system", Content: "persona"}}
	for i := range turns {
		messages = append(messages, Message{Role: "user", Content: "message " + strconv.Itoa(i+1)})
	}
	return messages
}

func TestHistory_keeps_a_conversation_under_the_conversation_prefix(t *testing.T) {
	// Given
	history, server := newHistory(t, time.Hour, 40)
	ctx := context.Background()
	messages := conversationOf(2)

	// When
	missingBefore, _ := history.Exists(ctx, "brodec:room:!a")
	saveErr := history.Save(ctx, "brodec:room:!a", messages)
	existsAfter, _ := history.Exists(ctx, "brodec:room:!a")
	got, getErr := history.Get(ctx, "brodec:room:!a")
	other, otherErr := history.Get(ctx, "brodec:room:!b")

	// Then
	if saveErr != nil || getErr != nil || otherErr != nil {
		t.Fatalf("unexpected errors: %v, %v, %v", saveErr, getErr, otherErr)
	}
	if missingBefore || !existsAfter {
		t.Fatalf("expected the conversation to exist only after it was saved, got %v then %v", missingBefore, existsAfter)
	}
	if !reflect.DeepEqual(got, messages) {
		t.Fatalf("expected %v, got %v", messages, got)
	}
	if other != nil {
		t.Fatalf("expected nothing under another id, got %v", other)
	}
	if keys := server.Keys(); !reflect.DeepEqual(keys, []string{"conversation:brodec:room:!a"}) {
		t.Fatalf("expected the key conversation:brodec:room:!a, got %v", keys)
	}
}

func TestHistory_keeps_the_system_prompt_and_the_newest_messages(t *testing.T) {
	// Given: a limit of 12 and a conversation of 15
	history, _ := newHistory(t, time.Hour, 12)
	ctx := context.Background()

	// When
	if err := history.Save(ctx, "room", conversationOf(15)); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := history.Get(ctx, "room")

	// Then
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got) != 13 || got[0].Role != "system" || got[1].Content != "message 4" || got[12].Content != "message 15" {
		t.Fatalf("expected the system prompt and messages 4 to 15, got %v", got)
	}
}

func TestHistory_forgets_a_conversation_its_lifetime_after_the_last_save(t *testing.T) {
	// Given
	history, server := newHistory(t, 2*time.Hour, 12)
	ctx := context.Background()
	if err := history.Save(ctx, "room", conversationOf(1)); err != nil {
		t.Fatalf("save: %v", err)
	}

	// When: saved again after an hour, then left alone
	server.FastForward(time.Hour)
	if err := history.Save(ctx, "room", conversationOf(2)); err != nil {
		t.Fatalf("save: %v", err)
	}
	server.FastForward(90 * time.Minute)
	keptAfterNinetyMinutes, _ := history.Exists(ctx, "room")
	server.FastForward(31 * time.Minute)
	keptAfterTwoHours, _ := history.Exists(ctx, "room")

	// Then
	if !keptAfterNinetyMinutes || keptAfterTwoHours {
		t.Fatalf("expected the conversation to last 2 hours from the last save, got %v then %v", keptAfterNinetyMinutes, keptAfterTwoHours)
	}
}

// The package-level functions are the Discord bot's history. They keep the
// limits they always had.
func TestSave_keeps_the_limits_the_Discord_bot_has_always_had(t *testing.T) {
	// Given
	server := miniredis.RunT(t)
	previous := client
	Init(server.Addr())
	t.Cleanup(func() {
		_ = client.Close()
		client = previous
	})
	ctx := context.Background()

	// When
	if err := Save(ctx, "guild:1:channel:2", conversationOf(45)); err != nil {
		t.Fatalf("save: %v", err)
	}
	exists, _ := Exists(ctx, "guild:1:channel:2")
	got, err := Get(ctx, "guild:1:channel:2")

	// Then
	if err != nil || !exists {
		t.Fatalf("expected the conversation to be kept, got %v, %v", exists, err)
	}
	if len(got) != 41 || got[1].Content != "message 6" {
		t.Fatalf("expected the system prompt and the newest 40 messages, got %d starting at %q", len(got), got[1].Content)
	}
	if ttl := server.TTL("conversation:guild:1:channel:2"); ttl != 24*time.Hour {
		t.Fatalf("expected 24 hours, got %s", ttl)
	}
	raw, _ := server.Get("conversation:guild:1:channel:2")
	var stored []Message
	if err := json.Unmarshal([]byte(raw), &stored); err != nil || len(stored) != 41 {
		t.Fatalf("expected a JSON list of 41 messages in the store, got %v", err)
	}
}

func TestPackageFunctions_do_nothing_without_a_server(t *testing.T) {
	// Given
	previous := client
	client = nil
	t.Cleanup(func() { client = previous })
	ctx := context.Background()

	// When
	exists, existsErr := Exists(ctx, "room")
	got, getErr := Get(ctx, "room")
	saveErr := Save(ctx, "room", conversationOf(1))

	// Then
	if exists || got != nil || existsErr != nil || getErr != nil || saveErr != nil {
		t.Fatalf("expected nothing and no error, got %v %v %v %v %v", exists, got, existsErr, getErr, saveErr)
	}
}
