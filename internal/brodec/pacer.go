package brodec

import (
	"context"
	"sync"
	"time"
)

// The platform's limits on what a bot sends: per room one message a second
// with bursts of five and at most twenty a minute, and thirty a second
// across every room. The gateway keeps the same limits; the bot keeps them
// too, so that it never queues more behind the gateway than a room allows.
const (
	roomBurst     = 5.0
	roomPerSecond = 1.0
	roomPerMinute = 20
	allPerSecond  = 30
)

type roomBucket struct {
	tokens float64
	at     time.Time
	minute []time.Time
}

// pacer keeps the bot's sends inside the limits above. A send over a limit
// waits its turn.
type pacer struct {
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error

	mu    sync.Mutex
	rooms map[string]*roomBucket
	all   []time.Time
}

func newPacer(now func() time.Time) *pacer {
	return &pacer{now: now, sleep: sleepContext, rooms: map[string]*roomBucket{}}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// tryTake counts a send into the room and returns zero when it may go now.
// Otherwise it counts nothing and returns how long the send must wait.
func (p *pacer) tryTake(room string) time.Duration {
	now := p.now()
	p.mu.Lock()
	defer p.mu.Unlock()

	for len(p.all) > 0 && now.Sub(p.all[0]) >= time.Second {
		p.all = p.all[1:]
	}
	bucket, ok := p.rooms[room]
	if !ok {
		bucket = &roomBucket{tokens: roomBurst, at: now}
		p.rooms[room] = bucket
	}
	bucket.tokens = min(bucket.tokens+now.Sub(bucket.at).Seconds()*roomPerSecond, roomBurst)
	bucket.at = now
	for len(bucket.minute) > 0 && now.Sub(bucket.minute[0]) >= time.Minute {
		bucket.minute = bucket.minute[1:]
	}

	var wait time.Duration
	if bucket.tokens < 1 {
		wait = max(wait, time.Duration((1-bucket.tokens)/roomPerSecond*float64(time.Second)))
	}
	if len(bucket.minute) >= roomPerMinute {
		wait = max(wait, time.Minute-now.Sub(bucket.minute[0]))
	}
	if len(p.all) >= allPerSecond {
		wait = max(wait, time.Second-now.Sub(p.all[0]))
	}
	if wait > 0 {
		return wait
	}
	bucket.tokens--
	bucket.minute = append(bucket.minute, now)
	p.all = append(p.all, now)
	p.forgetIdleRooms(now)
	return 0
}

// forgetIdleRooms drops the buckets of rooms nothing was sent to for more
// than a minute: such a bucket is full again and its minute is empty, which
// is what a new one starts as. The caller holds the lock.
func (p *pacer) forgetIdleRooms(now time.Time) {
	if len(p.rooms) < 256 {
		return
	}
	for room, bucket := range p.rooms {
		if now.Sub(bucket.at) > time.Minute {
			delete(p.rooms, room)
		}
	}
}

// wait blocks until a send into the room may go, and counts it.
func (p *pacer) wait(ctx context.Context, room string) error {
	for {
		wait := p.tryTake(room)
		if wait == 0 {
			return nil
		}
		if err := p.sleep(ctx, wait+5*time.Millisecond); err != nil {
			return err
		}
	}
}
