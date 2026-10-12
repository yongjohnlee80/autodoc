package embedder

import (
	"context"
	"errors"
	"testing"
	"time"
)

// queued is how many wait for a slot, queries and batches.
func (l *limiter) queued() (queries, batches int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.queries), len(l.batches)
}

// waitQueued polls until l has queries and batches waiting.
func waitQueued(t *testing.T, l *limiter, queries, batches int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		q, b := l.queued()
		if q == queries && b == batches {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting: %d queries and %d batches, want %d and %d", q, b, queries, batches)
		}
		time.Sleep(time.Millisecond)
	}
}

// waiter acquires a slot in the background and says when it has one.
func waiter(l *limiter, query bool) chan error {
	got := make(chan error, 1)
	go func() { got <- l.acquire(context.Background(), query) }()
	return got
}

func granted(t *testing.T, name string, got chan error) {
	t.Helper()
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s was not given the slot", name)
	}
}

func notGranted(t *testing.T, name string, got chan error) {
	t.Helper()
	select {
	case <-got:
		t.Fatalf("%s was given a slot no one released", name)
	case <-time.After(50 * time.Millisecond):
	}
}

// With every slot held, a released slot goes to a waiting query before a batch that waited
// longer, and to the first of two queries: first come first served within a class. The mutants,
// named here: release serving the batches first; a queue served last in first out.
func TestAReleasedSlotGoesToAWaitingQueryFirst(t *testing.T) {
	l := newLimiter(1)
	if err := l.acquire(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	batch := waiter(l, false)
	waitQueued(t, l, 0, 1)
	q1 := waiter(l, true)
	waitQueued(t, l, 1, 1)
	q2 := waiter(l, true)
	waitQueued(t, l, 2, 1)

	l.release()
	granted(t, "the first query", q1)
	notGranted(t, "the second query", q2)
	notGranted(t, "the batch", batch)
	l.release()
	granted(t, "the second query", q2)
	notGranted(t, "the batch", batch)
	l.release()
	granted(t, "the batch", batch)
	l.release()
	if l.free != 1 {
		t.Errorf("%d slots free after every release, want 1", l.free)
	}
}

// A waiter whose context ends while it waits leaves the queue; one handed the slot after its
// context ended, before it could leave, passes the slot on: the slot is never lost and never
// doubled. The mutant, named here: a cancelled waiter that was handed the slot keeping it (the
// count drops).
func TestACancelledWaiterNeverLosesTheSlot(t *testing.T) {
	l := newLimiter(1)
	for i := range 300 {
		if err := l.acquire(context.Background(), false); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		got := make(chan error, 1)
		go func() { got <- l.acquire(ctx, i%2 == 0) }()
		if i%2 == 0 {
			waitQueued(t, l, 1, 0)
		} else {
			waitQueued(t, l, 0, 1)
		}
		if i%3 == 0 {
			cancel() // cancelled while it waits: it leaves the queue
			if err := <-got; !errors.Is(err, context.Canceled) {
				t.Fatalf("try %d: a cancelled waiter answered %v", i, err)
			}
			l.release()
		} else {
			// the slot handed to the waiter after its context ended, before it could leave the
			// queue: woken by the end, it waits for l.mu, held here while the slot is handed over,
			// and then finds itself out of the queue, holding a slot it must pass on
			l.mu.Lock()
			cancel()
			time.Sleep(time.Millisecond) // the waiter wakes to its context and waits for l.mu
			l.handOver()
			l.mu.Unlock()
			if err := <-got; !errors.Is(err, context.Canceled) {
				t.Fatalf("try %d: a waiter cancelled before its handover answered %v", i, err)
			}
		}
		if q, b := l.queued(); l.free != 1 || q != 0 || b != 0 {
			t.Fatalf("try %d: %d free, %d and %d waiting; want 1 free and no one waiting", i, l.free, q, b)
		}
	}
}
