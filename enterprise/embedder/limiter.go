package embedder

import (
	"context"
	"sync"
)

// limiter is the provider's slots, shared by the worker's batches and the searches' queries: at
// most n calls in flight. A released slot is handed straight to a waiter, a query before a batch
// and first come first served within each, so a waiting query is never overtaken by a batch that
// asked after it, or by one already waiting. A batch call is never interrupted.
type limiter struct {
	mu      sync.Mutex
	free    int
	queries []chan struct{}
	batches []chan struct{}
}

func newLimiter(n int) *limiter { return &limiter{free: n} }

// acquire waits for a slot, or for ctx to end. A slot is free only while no one waits: release
// hands it to a waiter before it frees it.
func (l *limiter) acquire(ctx context.Context, query bool) error {
	l.mu.Lock()
	if l.free > 0 {
		l.free--
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
			l.release()
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

// release gives the slot to the first waiting query, else the first waiting batch, else frees it.
func (l *limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch {
	case len(l.queries) > 0:
		close(l.queries[0])
		l.queries = l.queries[1:]
	case len(l.batches) > 0:
		close(l.batches[0])
		l.batches = l.batches[1:]
	default:
		l.free++
	}
}
