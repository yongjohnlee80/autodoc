package api

import (
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// A LIMIT PER CLIENT (docs/decisions/server.md#a-limit-per-client). One client's flood of searches
// must not take what the others, and the server's own work, need. Each client has two limits, both
// checked once its token is matched and before its body is read, and both refused at once:
//
//   - its searches in flight (Client.MaxInFlight): 429 too_many_in_flight, Retry-After 1;
//   - its rate (Client.Rate and Burst): 429 too_many_requests, Retry-After the whole seconds until
//     a search is allowed again.
//
// Refused, never queued: a queue would hold a connection and a handler for every request waiting,
// and the time it waited would come out of the budget of the searches behind it. The rate is
// checked first, so a request it refuses takes no place among those in flight.

// limit is one client's limits and where it stands against them.
type limit struct {
	max      int32        // searches in flight at most; 0 is no cap
	inFlight atomic.Int32 // searches in flight now
	rate     *bucket      // nil is no rate
}

func newLimit(c Client, now time.Time) *limit {
	l := &limit{max: int32(max(c.MaxInFlight, 0))}
	if c.Rate > 0 {
		l.rate = newBucket(c.Rate, max(c.Burst, 1), now)
	}
	return l
}

// enter counts one more search in flight, unless the client is at its cap. A search that entered
// leaves exactly once (leave).
func (l *limit) enter() bool {
	if l.max == 0 {
		return true
	}
	for {
		n := l.inFlight.Load()
		if n >= l.max {
			return false
		}
		// a compare-and-swap, not an add taken back: a refused request never counts, even for a
		// moment, so it can't refuse another that came as a search left
		if l.inFlight.CompareAndSwap(n, n+1) {
			return true
		}
	}
}

// leave is enter's other half.
func (l *limit) leave() {
	if l.max != 0 {
		l.inFlight.Add(-1)
	}
}

// bucket is a token bucket: rate tokens a second, at most burst kept, one taken by each request.
// It is safe for concurrent use.
type bucket struct {
	mu     sync.Mutex
	rate   float64 // tokens a second
	burst  float64
	tokens float64
	last   time.Time // when tokens was last brought up to date
}

// newBucket is a full bucket at now.
func newBucket(rate float64, burst int, now time.Time) *bucket {
	return &bucket{rate: rate, burst: float64(burst), tokens: float64(burst), last: now}
}

// take takes a token at now, or says how long until one is there.
func (b *bucket) take(now time.Time) (wait time.Duration, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill(now)
	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}
	return time.Duration((1 - b.tokens) / b.rate * float64(time.Second)), false
}

// fill brings the tokens up to date at now. A clock that went back adds nothing, and is not taken
// as the bucket's time: the tokens earned up to last stay earned. b.mu is held.
func (b *bucket) fill(now time.Time) {
	if d := now.Sub(b.last); d > 0 {
		b.tokens = math.Min(b.burst, b.tokens+d.Seconds()*b.rate)
		b.last = now
	}
}

// retryAfter is a wait as a Retry-After header carries it: whole seconds, rounded up, at least 1.
func retryAfter(wait time.Duration) int {
	return max(int(math.Ceil(wait.Seconds())), 1)
}
