package ambient

import (
	"context"
	"testing"
	"time"
)

func TestAmbientRepliesWithinActiveSession(t *testing.T) {
	// Given
	clock := &fakeClock{now: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)}
	store := newFakeStore()
	manager := NewManager(DefaultConfig(), store, clock)
	manager.config.Enabled = true
	requireNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))

	// When
	decision, err := manager.Decide(context.Background(), Message{ChannelID: "channel-1", GuildID: "guild-1", UserID: "user-1", Content: "keep going"})

	// Then
	requireNoError(t, err)
	if !decision.Reply || decision.Stop {
		t.Fatalf("expected ambient reply decision, got %#v", decision)
	}
}

func TestAmbientDoesNotReplyOutsideActiveSession(t *testing.T) {
	// Given
	manager := NewManager(Config{Enabled: true}, newFakeStore(), &fakeClock{now: time.Now().UTC()})

	// When
	decision, err := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "random chatter"})

	// Then
	requireNoError(t, err)
	if decision.Reply || decision.Stop {
		t.Fatalf("expected no ambient action without active state, got %#v", decision)
	}
}

func TestAmbientStopsOnStopPhrases(t *testing.T) {
	// Given
	clock := &fakeClock{now: time.Now().UTC()}
	store := newFakeStore()
	manager := NewManager(Config{Enabled: true}, store, clock)
	requireNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))

	// When
	decision, err := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "shut up pc principal"})

	// Then
	requireNoError(t, err)
	if !decision.Stop || decision.Reply {
		t.Fatalf("expected stop decision, got %#v", decision)
	}
	if _, ok := store.states[Key("channel-1")]; ok {
		t.Fatal("expected stop phrase to clear ambient state")
	}
}

func TestAmbientExpiresAfterTTL(t *testing.T) {
	// Given
	clock := &fakeClock{now: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)}
	store := newFakeStore()
	manager := NewManager(Config{Enabled: true}, store, clock)
	requireNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))
	clock.now = clock.now.Add(defaultSessionTTL)

	// When
	decision, err := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "still there?"})

	// Then
	requireNoError(t, err)
	if decision.Reply || decision.Stop {
		t.Fatalf("expected expired ambient state to suppress reply, got %#v", decision)
	}
	if _, ok := store.states[Key("channel-1")]; ok {
		t.Fatal("expected expired ambient state to be deleted")
	}
}

func TestAmbientEnforcesCooldownsAndReplyCaps(t *testing.T) {
	// Given
	clock := &fakeClock{now: time.Date(2026, 6, 27, 12, 0, 0, 0, time.UTC)}
	store := newFakeStore()
	manager := NewManager(Config{Enabled: true}, store, clock)
	requireNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))

	// When
	first, firstErr := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "one"})
	requireNoError(t, firstErr)
	requireNoError(t, manager.RecordReply(context.Background(), "channel-1"))
	second, secondErr := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "two"})
	clock.now = clock.now.Add(defaultChannelReplyCooldown)
	third, thirdErr := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "three"})
	requireNoError(t, manager.RecordReply(context.Background(), "channel-1"))
	clock.now = clock.now.Add(defaultChannelReplyCooldown)
	capped, cappedErr := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "four"})

	// Then
	requireNoError(t, secondErr)
	requireNoError(t, thirdErr)
	requireNoError(t, cappedErr)
	if !first.Reply || second.Reply || !third.Reply || capped.Reply {
		t.Fatalf("expected first/third replies only before cap, got first=%#v second=%#v third=%#v capped=%#v", first, second, third, capped)
	}
}

func TestAmbientEnforcesMaxUserTurns(t *testing.T) {
	// Given
	clock := &fakeClock{now: time.Now().UTC()}
	store := newFakeStore()
	manager := NewManager(Config{Enabled: true}, store, clock)
	store.states[Key("channel-1")] = State{ChannelID: "channel-1", GuildID: "guild-1", ActiveUserID: "user-1", UserTurns: defaultMaxUserTurns, ExpiresAt: clock.now.Add(time.Minute)}

	// When
	decision, err := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-1", Content: "one more"})

	// Then
	requireNoError(t, err)
	if decision.Reply || decision.Stop {
		t.Fatalf("expected max user turns to end ambient replies, got %#v", decision)
	}
}

func TestAmbientDoesNotReplyForOtherVisibleUsers(t *testing.T) {
	// Given
	clock := &fakeClock{now: time.Now().UTC()}
	store := newFakeStore()
	manager := NewManager(Config{Enabled: true}, store, clock)
	requireNoError(t, manager.Activate(context.Background(), "channel-1", "guild-1", "user-1"))

	// When
	decision, err := manager.Decide(context.Background(), Message{ChannelID: "channel-1", UserID: "user-2", Content: "visible chatter"})

	// Then
	requireNoError(t, err)
	if decision.Reply || decision.Stop {
		t.Fatalf("expected other user message to ingest silently only, got %#v", decision)
	}
}

type fakeClock struct {
	now time.Time
}

func (c *fakeClock) Now() time.Time { return c.now }

type fakeStore struct {
	states map[string]State
	ttls   map[string]time.Duration
}

func newFakeStore() *fakeStore {
	return &fakeStore{states: map[string]State{}, ttls: map[string]time.Duration{}}
}

func (s *fakeStore) Load(ctx context.Context, key string) (State, bool, error) {
	state, ok := s.states[key]
	return state, ok, nil
}

func (s *fakeStore) Save(ctx context.Context, key string, state State, ttl time.Duration) error {
	s.states[key] = state
	s.ttls[key] = ttl
	return nil
}

func (s *fakeStore) Delete(ctx context.Context, key string) error {
	delete(s.states, key)
	return nil
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
