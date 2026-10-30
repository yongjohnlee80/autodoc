package inbox

import "sync"

// Feed tells inbox.watch's waiters that the journal may have grown: a store adapter's
// crew.Signals.Feed, fired after every write that appended to it. It never blocks, and a waiter
// told twice before it looks again looks once.
type Feed struct {
	mu      sync.Mutex
	waiters map[chan struct{}]struct{}
}

// NewFeed is a feed with no waiters.
func NewFeed() *Feed { return &Feed{waiters: map[chan struct{}]struct{}{}} }

// Notify tells every waiter.
func (f *Feed) Notify() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.waiters {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Watch is a channel that hears every Notify from now on, and the func that stops it. A waiter
// watches BEFORE it reads, so a commit between its read and its wait is not missed.
func (f *Feed) Watch() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	f.mu.Lock()
	f.waiters[ch] = struct{}{}
	f.mu.Unlock()
	return ch, func() {
		f.mu.Lock()
		delete(f.waiters, ch)
		f.mu.Unlock()
	}
}
