package index

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// An idle writer sleeps: once its batches are committed and its garbage collected, nothing wakes it
// until an event (a touch, a result, vectors, an op) or a time of its own falls due. A daemon runs
// one writer for each workspace it serves, and a writer that polled on a heartbeat kept an idle
// daemon busy with nothing to do.
func TestAnIdleWriterSleeps(t *testing.T) {
	var wakes atomic.Int64
	e := newEnv(t, options{BatchDelay: 20 * time.Millisecond, onWake: func() { wakes.Add(1) }})
	e.put("a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
	e.remove("b.md") // dead chunks: garbage for the writer to collect when idle
	e.eventually("the dead chunks collected", func() bool {
		var dead int
		_ = scanOne(context.Background(), e.raw, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
		return dead == 0
	})
	// the collection that found the last garbage is followed by at most one that finds none
	time.Sleep(100 * time.Millisecond)
	before := wakes.Load()
	time.Sleep(500 * time.Millisecond) // 25 BatchDelays: a heartbeat at BatchDelay/2 wakes 50 times
	if n := wakes.Load() - before; n > 1 {
		t.Errorf("an idle writer woke %d times in 500 ms with nothing due, want at most 1", n)
	}
	// and an event still wakes it at once
	e.put("c.md", "# C\n\ngamma\n")
}
