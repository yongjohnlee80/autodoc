package embedder

import (
	"context"
	"sync"
)

// limiter is the provider's slots, shared by the worker's batches and the searches' queries: at
// most n calls in flight. A released slot is handed straight to a waiter, a query before a batch
// and first come first served within each, so a waiting query is never overtaken by a batch that
// asked after it, or by one already waiting. A batch call is never interrupted.
//
// The queries never hold every slot (docs/decisions/server.md#a-limit-per-client): at most all
// but one, so the worker always has a slot to embed with. Queries go ahead of every batch, and a
// steady stream of them, as many at once as there are slots, would otherwise keep the worker from
// a single call for as long as it lasted: nothing new or edited would become searchable by meaning
// meanwhile. With one slot in all there is none to keep: it is the queries' too, ahead of the
// batches as before.
type limiter struct {
	mu   sync.Mutex
	free int
	// inQueries is how many slots queries hold now, and maxQueries how many they may
	inQueries, maxQueries int
	queries               []chan struct{}
	batches               []chan struct{}
}

func newLimiter(n int) *limiter { return &limiter{free: n, maxQueries: max(n-1, 1)} }

// acquire waits for a slot, or for ctx to end. The caller gives it back with release, saying again
// whether it was a query's. A batch takes a free slot; a query takes one only while the queries
// hold fewer than they may, and otherwise waits its turn, though a slot is free: that one is the
// worker's.
func (l *limiter) acquire(ctx context.Context, query bool) error {
	l.mu.Lock()
	if l.free > 0 && (!query || l.inQueries < l.maxQueries) {
		l.free--
		if query {
			l.inQueries++
		}
		l.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	if query {
		l.queries = append(l.queries, ch)
	} else {
		l.batches = append(l.batches, ch)
	}
	l.mu.Unlock()
	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		l.mu.Lock()
		waiting := l.drop(ch, query)
		l.mu.Unlock()
		if !waiting {
			// handed the slot as ctx ended: it is passed on, never lost
			l.release(query)
		}
		return ctx.Err()
	}
}

// drop removes ch from its queue: false when it is not there, having been handed a slot. l.mu
// is held.
func (l *limiter) drop(ch chan struct{}, query bool) bool {
	q := &l.batches
	if query {
		q = &l.queries
	}
	for i, c := range *q {
		if c == ch {
			*q = append((*q)[:i], (*q)[i+1:]...)
			return true
		}
	}
	return false
}

// release gives back a slot acquire gave, a query's or a batch's, and hands it on (handOver).
func (l *limiter) release(query bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if query {
		l.inQueries--
	}
	l.handOver()
}

// handOver is release's: a slot no one holds goes to the first waiting query, while the queries
// may hold one more; else to the first waiting batch; else it is free. A free slot with queries
// waiting is the one they may not take. l.mu is held.
func (l *limiter) handOver() {
	switch {
	case len(l.queries) > 0 && l.inQueries < l.maxQueries:
		close(l.queries[0])
		l.queries = l.queries[1:]
		l.inQueries++
	case len(l.batches) > 0:
		close(l.batches[0])
		l.batches = l.batches[1:]
	default:
		l.free++
	}
}
