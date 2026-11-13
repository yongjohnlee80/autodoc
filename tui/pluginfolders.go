package tui

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// folderFence is the installed plugin folders a removal or an update owns, and the reads of them
// that run off the loop (the manager's listing, an update's fetch: git, run in the folder). On
// Windows a process's working folder can be neither removed nor renamed while it runs, so a removal
// or an update waits for the reads already in a folder, and a read never starts in a folder that is
// owned. It is the same rule on every OS: a folder is changed only once nothing reads it.
//
// Concurrency: its methods are safe from any goroutine. own and disown are called on the loop; the
// reads run on workers; awaitReads waits on a worker.
type folderFence struct {
	mu    sync.Mutex
	owned map[string]string // the folder → what owns it ("removed", "updated"), for the refusal
	reads map[string]int    // the folder → the reads running in it
	// ended are, for a folder with reads running, a channel closed when its last one ends
	ended map[string]chan struct{}
	// waiting are the removals and updates waiting for a folder's reads (awaitReads), by folder: what
	// a cell reads to know an operation is held by a read, rather than not yet at its wait
	waiting map[string]int
}

// own marks dir as owned by what until disown: no run or read starts in it meanwhile. It is
// exclusive: when another removal or update owns dir already, it answers false and what that one
// is doing.
func (f *folderFence) own(dir, what string) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if other, ok := f.owned[dir]; ok {
		return false, other
	}
	if f.owned == nil {
		f.owned = map[string]string{}
	}
	f.owned[dir] = what
	return true, ""
}

func (f *folderFence) disown(dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.owned, dir)
}

// owner is what owns dir, if anything does.
func (f *folderFence) owner(dir string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	what, ok := f.owned[dir]
	return what, ok
}

// read runs read as a read of dir, unless dir is owned: then it runs nothing and answers what owns
// it, and false. A removal or an update that owns dir after read began waits for it (awaitReads).
func (f *folderFence) read(dir string, read func()) (string, bool) {
	f.mu.Lock()
	if what, ok := f.owned[dir]; ok {
		f.mu.Unlock()
		return what, false
	}
	if f.reads == nil {
		f.reads, f.ended = map[string]int{}, map[string]chan struct{}{}
	}
	if f.reads[dir] == 0 {
		f.ended[dir] = make(chan struct{})
	}
	f.reads[dir]++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.reads[dir]--; f.reads[dir] == 0 {
			close(f.ended[dir])
			delete(f.reads, dir)
			delete(f.ended, dir)
		}
	}()
	read()
	return "", true
}

// readsEnded is a channel closed once no read runs in dir: closed already when none does.
func (f *folderFence) readsEnded(dir string) <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.ended[dir]; ok {
		return ch
	}
	ch := make(chan struct{})
	close(ch)
	return ch
}

// awaitReads waits, off the loop, until no read runs in dir, bounded by bound: a read is git, each
// call bounded by its own timeout, and a removal waits no longer than bound for it. An error says
// the read did not end; the folder is to be left as it is.
func (f *folderFence) awaitReads(ctx context.Context, dir string, bound time.Duration) error {
	ended := f.readsEnded(dir)
	select {
	case <-ended:
		return nil
	default:
	}
	f.mu.Lock()
	if f.waiting == nil {
		f.waiting = map[string]int{}
	}
	f.waiting[dir]++
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.waiting[dir]--; f.waiting[dir] == 0 {
			delete(f.waiting, dir)
		}
	}()
	// NOTE: deadline: a read is a git process, which nothing but its own minutes-long timeout ends;
	// a removal or an update waits for it this long, then fails rather than waiting for ever
	deadline := time.NewTimer(bound)
	defer deadline.Stop()
	select {
	case <-ended:
		return nil
	case <-deadline.C:
		return fmt.Errorf("a read of its folder did not end within %s: its folder was left as it is", bound)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// folderHold is a removal's or an update's ownership of a plugin folder (plugins.own): its waits
// run on the operation's worker before the folder is changed, and its release, on the loop, once
// the operation has ended, whether or not it succeeded.
type folderHold struct {
	ok    bool
	other string // what owns the folder already, when !ok
	// waits run in order, off the loop: the first error ends the operation, its folder unchanged
	waits []func(context.Context) error
	// releases run on the loop, newest first, before the folder is disowned
	releases []func()
	fence    *folderFence
	dir      string
	released bool
}

// wait runs the hold's waits in order, off the loop; the first error is the operation's.
func (h *folderHold) wait(ctx context.Context) error {
	for _, w := range h.waits {
		if err := w(ctx); err != nil {
			return err
		}
	}
	return nil
}

// release ends the hold, on the loop, once however often it is called: its releases, then the
// folder disowned. A refused hold (!ok) owns nothing and releases nothing.
func (h *folderHold) release() {
	if !h.ok || h.released {
		return
	}
	h.released = true
	for i := len(h.releases) - 1; i >= 0; i-- {
		h.releases[i]()
	}
	h.fence.disown(h.dir)
}
