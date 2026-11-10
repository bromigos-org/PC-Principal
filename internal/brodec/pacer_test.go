package brodec

import (
	"context"
	"testing"
	"time"
)

func TestPacer_lets_a_burst_of_five_through_then_one_a_second(t *testing.T) {
	// Given
	now := time.Now()
	p := newPacer(func() time.Time { return now })

	// When
	for i := range 5 {
		if wait := p.tryTake("!a"); wait != 0 {
			t.Fatalf("send %d of the burst had to wait %s", i+1, wait)
		}
	}
	sixth := p.tryTake("!a")
	otherRoom := p.tryTake("!b")
	now = now.Add(1001 * time.Millisecond)
	afterASecond := p.tryTake("!a")

	// Then
	if sixth <= 0 || sixth > time.Second {
		t.Fatalf("expected the sixth send to wait up to a second, got %s", sixth)
	}
	if otherRoom != 0 {
		t.Fatalf("another room has its own burst, got a wait of %s", otherRoom)
	}
	if afterASecond != 0 {
		t.Fatalf("expected a send a second later to go, got a wait of %s", afterASecond)
	}
}

func TestPacer_allows_twenty_a_minute_in_a_room(t *testing.T) {
	// Given
	start := time.Now()
	now := start
	p := newPacer(func() time.Time { return now })

	// When: twenty sends as fast as the burst and the refill allow
	sent := 0
	for sent < roomPerMinute {
		if wait := p.tryTake("!a"); wait == 0 {
			sent++
		} else {
			now = now.Add(wait)
		}
	}
	twentyFirst := p.tryTake("!a")
	now = start.Add(time.Minute + time.Millisecond)
	afterTheMinute := p.tryTake("!a")

	// Then
	if twentyFirst < 30*time.Second {
		t.Fatalf("expected the 21st send to wait for the minute to pass, got %s", twentyFirst)
	}
	if afterTheMinute != 0 {
		t.Fatalf("expected a send to go once the minute has passed, got a wait of %s", afterTheMinute)
	}
}

func TestPacer_allows_thirty_a_second_across_all_rooms(t *testing.T) {
	// Given
	now := time.Now()
	p := newPacer(func() time.Time { return now })

	// When: thirty rooms, one send each, in the same instant
	for i := range allPerSecond {
		if wait := p.tryTake("!room-" + string(rune('A'+i))); wait != 0 {
			t.Fatalf("send %d had to wait %s", i+1, wait)
		}
	}
	thirtyFirst := p.tryTake("!one-more")

	// Then
	if thirtyFirst <= 0 || thirtyFirst > time.Second {
		t.Fatalf("expected the 31st send to wait up to a second, got %s", thirtyFirst)
	}
}

func TestPacer_wait_sleeps_until_the_send_may_go(t *testing.T) {
	// Given: a pacer whose sleep moves the clock instead of waiting
	now := time.Now()
	p := newPacer(func() time.Time { return now })
	var slept time.Duration
	p.sleep = func(_ context.Context, d time.Duration) error {
		slept += d
		now = now.Add(d)
		return nil
	}
	for range 5 {
		p.tryTake("!a")
	}

	// When
	err := p.wait(context.Background(), "!a")

	// Then
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if slept <= 0 || slept > 2*time.Second {
		t.Fatalf("expected about a second of waiting, got %s", slept)
	}
}

func TestPacer_wait_gives_up_when_the_work_is_cancelled(t *testing.T) {
	// Given
	now := time.Now()
	p := newPacer(func() time.Time { return now })
	for range 5 {
		p.tryTake("!a")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// When
	err := p.wait(ctx, "!a")

	// Then
	if err == nil {
		t.Fatal("expected the cancelled wait to return an error")
	}
}
