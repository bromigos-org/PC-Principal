package backfill

import "testing"

func TestDefaultConfig_is_disabled_and_bounded(t *testing.T) {
	// When
	config := DefaultConfig()

	// Then
	if config.Enabled {
		t.Fatal("expected backfill disabled by default")
	}
	if config.MaxChannelsPerRun <= 0 || config.MaxMessagesPerChannel <= 0 || config.MemoryBatchSize <= 0 || config.RequestDelay < 0 || config.Backoff <= 0 {
		t.Fatalf("expected bounded positive defaults, got %#v", config)
	}
}

func TestConfig_withDefaults_preserves_zero_as_unlimited_for_traversal_caps(t *testing.T) {
	// Given
	config := Config{Enabled: true, MaxChannelsPerRun: 0, MaxMessagesPerChannel: 0, MemoryBatchSize: 1, Backoff: 1, MaxAttempts: 1}

	// When
	got := config.withDefaults()

	// Then
	if got.MaxChannelsPerRun != 0 || got.MaxMessagesPerChannel != 0 {
		t.Fatalf("expected zero traversal caps to mean unlimited, got %#v", got)
	}
}
