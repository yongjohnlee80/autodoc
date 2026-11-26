package embedder

import (
	"context"
	"errors"
	"fmt"
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

	l.release(false)
	granted(t, "the first query", q1)
	notGranted(t, "the second query", q2)
	notGranted(t, "the batch", batch)
	l.release(true)
	granted(t, "the second query", q2)
	notGranted(t, "the batch", batch)
	l.release(true)
	granted(t, "the batch", batch)
	l.release(false)
	if l.free != 1 || l.inQueries != 0 {
		t.Errorf("%d slots free and %d held by queries after every release, want 1 and 0", l.free, l.inQueries)
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
			l.release(false)
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
		if q, b := l.queued(); l.free != 1 || l.inQueries != 0 || q != 0 || b != 0 {
			t.Fatalf("try %d: %d free, %d held by queries, %d and %d waiting; want 1 free, none held and no one waiting", i, l.free, l.inQueries, q, b)
		}
	}
}

// The queries never hold every slot: with three, two queries take a slot each and a third waits,
// though a slot is free, which a batch then takes at once. A slot a batch gives back, with the
// queries at their two, goes to a waiting batch and never to the waiting query; a slot a query
// gives back goes to the waiting query, ahead of a batch that waited longer. With every slot given
// back, three are free and no query holds one. The mutants, named here: the queries allowed every
// slot (the third query is given the free slot); a query's slot not counted back when it is
// released (the waiting query is never given one).
func TestTheQueriesNeverHoldEverySlot(t *testing.T) {
	l := newLimiter(3)
	for i := range 2 {
		if err := l.acquire(context.Background(), true); err != nil {
			t.Fatalf("query %d: %v", i+1, err)
		}
	}
	q3 := waiter(l, true)
	waitQueued(t, l, 1, 0)
	notGranted(t, "a third query, with the worker's slot free", q3)
	if err := l.acquire(context.Background(), false); err != nil {
		t.Fatalf("a batch, with the worker's slot free: %v", err)
	}
	b2 := waiter(l, false)
	waitQueued(t, l, 1, 1)

	l.release(false) // the batch's slot: the queries hold their two, so it is the waiting batch's
	granted(t, "the waiting batch", b2)
	notGranted(t, "the third query, as a batch's slot came back", q3)
	b3 := waiter(l, false)
	waitQueued(t, l, 1, 1)

	l.release(true) // a query's slot: the waiting query's, ahead of the batch
	granted(t, "the third query, as a query's slot came back", q3)
	notGranted(t, "the batch that waits behind it", b3)
	l.release(false)
	granted(t, "the batch, as a batch's slot came back", b3)

	l.release(true)
	l.release(true)
	l.release(false)
	if q, b := l.queued(); l.free != 3 || l.inQueries != 0 || q != 0 || b != 0 {
		t.Errorf("%d free, %d held by queries, %d and %d waiting; want 3 free, none held and no one waiting", l.free, l.inQueries, q, b)
	}
}

// With two slots a query holds one at most, and with one slot, which there is no keeping from the
// queries, a query holds it: for each number of slots, as many queries as may hold one are given
// theirs at once, and one more waits.
func TestTheQueriesSlotsByTheNumberOfSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // the queries left waiting leave
	for n, want := range map[int]int{1: 1, 2: 1, 3: 2, 4: 3, 16: 15, 64: 63} {
		l := newLimiter(n)
		for i := range want {
			// given at once: a wait here is the failure, so it is bounded, not timed
			given, stop := context.WithTimeout(ctx, 10*time.Second)
			err := l.acquire(given, true)
			stop()
			if err != nil {
				t.Fatalf("%d slots: query %d of the %d that may hold one was not given it: %v", n, i+1, want, err)
			}
		}
		notGranted(t, fmt.Sprintf("with %d slots, query %d", n, want+1), func() chan error {
			got := make(chan error, 1)
			go func() { got <- l.acquire(ctx, true) }()
			return got
		}())
	}
}
