package tui

import (
	"slices"
	"sync"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// THE OUTBOUND QUEUE — what waits to be sent to a running plugin: newest-wins for the page's
// documents (pluginfeed.go), in order for the rest (plugins.go).

type outNote struct {
	method string
	params []any
}

// outQueue is what the host sends a plugin, waiting for the sender. A document is state, not an
// event: one still waiting is dropped when a newer one is sent, which takes the tail, so at most one
// waits (ADR 1791268009 §2.3). Every other notification keeps its order, and more than pluginQueue
// of them waiting is a plugin not reading.
type outQueue struct {
	mu     sync.Mutex
	items  []outNote
	closed bool
	wake   chan struct{}
}

func newOutQueue() *outQueue { return &outQueue{wake: make(chan struct{}, 1)} }

// push queues n; false when the plugin has not read pluginQueue of what it was sent, or the queue
// is closed.
func (q *outQueue) push(n outNote) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if n.method == plugin.MethodDocument {
		q.items = slices.DeleteFunc(q.items, func(o outNote) bool { return o.method == plugin.MethodDocument })
	} else if len(q.items) >= pluginQueue {
		return false
	}
	q.items = append(q.items, n)
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return true
}

// close ends the queue: the sender sends what it holds, then stops.
func (q *outQueue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// pop is the next to send, in order; false once the queue is closed and empty.
func (q *outQueue) pop() (outNote, bool) {
	for {
		q.mu.Lock()
		if len(q.items) > 0 {
			n := q.items[0]
			q.items[0] = outNote{}
			q.items = q.items[1:]
			q.mu.Unlock()
			return n, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return outNote{}, false
		}
		<-q.wake
	}
}

// drop empties the queue, closing it: what waits is never sent.
func (q *outQueue) drop() {
	q.mu.Lock()
	q.items, q.closed = nil, true
	q.mu.Unlock()
}

// waiting is how many notifications wait, and how many of them are documents.
func (q *outQueue) waiting() (all, documents int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, n := range q.items {
		if n.method == plugin.MethodDocument {
			documents++
		}
	}
	return len(q.items), documents
}
