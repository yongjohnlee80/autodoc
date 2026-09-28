// Package follow keeps a workspace's index following its root. The index converges on the root's
// content: watch events, poll events and scans are hints naming paths, and the indexer re-reads each
// path it is told about.
//
// The follower never writes the index. It tells a Queue which paths to re-read, and the indexer
// behind it (the store's one writer) decides from the file's current state what to do: delete a
// document that is gone or no longer eligible, skip one whose Version is unchanged, or re-index it.
package follow

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/vfs"
)

// Store is what the follower reads of the index.
type Store interface {
	// Version is the Version a path was indexed at, and whether it is indexed.
	Version(path string) (vfs.Version, bool)
	// PathsUnder lists the indexed paths under dir ("." for all of them).
	PathsUnder(dir string) []string
}

// Queue takes paths for the indexer to re-read. Touch must not block for long: the follower calls
// it while it drains watch events, and a watcher whose consumer stalls overflows.
type Queue interface {
	Touch(path string)
}

// The ways a follower can be following its root, as index.status reports them.
const (
	Starting = "starting"
	Watching = "watch"
	Polling  = "poll"
	Degraded = "degraded"
)

// Status is what index.status reports of a follower.
type Status struct {
	Following string
	// Err is why the follower is degraded: both Watch and Poll failed at setup.
	Err string
	// Retrying lists the subtrees a scan could not read. Their indexed documents are kept, and they
	// are rescanned with backoff until a scan succeeds.
	Retrying []string
}

// Options configures a follower. Zero values take the defaults.
type Options struct {
	PollInterval time.Duration // Poll's listing interval, and degraded mode's full-scan interval (default 2 s)
	MinBackoff   time.Duration // the first retry delay (default 1 s)
	MaxBackoff   time.Duration // the retry delay's cap (default 1 min)
	// Match reports whether a path belongs to the workspace (include, not exclude). Nil matches all.
	Match func(path string) bool
	// Excluded reports whether a path lies in an excluded directory (.git, say). A directory there
	// that cannot be read is not the workspace's concern, so it is not retried. Nil excludes nothing.
	Excluded func(path string) bool
}

// Follower follows one workspace root.
type Follower struct {
	fsys  vfs.FS
	store Store
	queue Queue
	opts  Options

	mu     sync.Mutex
	status Status

	retry  map[string]*retryEntry // unreadable subtrees; touched only by Run's goroutine
	rescan chan struct{}
}

type retryEntry struct {
	due   time.Time
	delay time.Duration
	err   error
}

// New returns a follower of fsys that queues paths for the indexer behind store and queue.
func New(fsys vfs.FS, store Store, queue Queue, opts Options) *Follower {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.MinBackoff <= 0 {
		opts.MinBackoff = time.Second
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = time.Minute
	}
	if opts.MaxBackoff < opts.MinBackoff {
		opts.MaxBackoff = opts.MinBackoff
	}
	return &Follower{fsys: fsys, store: store, queue: queue, opts: opts,
		status: Status{Following: Starting}, retry: map[string]*retryEntry{}, rescan: make(chan struct{}, 1)}
}

// Rescan asks Run to reconcile the whole root, finding every eligible file the index does not have
// at its Version. It never blocks; requests made before one is served are served once.
func (f *Follower) Rescan() {
	select {
	case f.rescan <- struct{}{}:
	default:
	}
}

// Status returns a snapshot of how the follower is following.
func (f *Follower) Status() Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.status
	s.Retrying = append([]string(nil), s.Retrying...)
	return s
}

func (f *Follower) setFollowing(mode string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Following = mode
	f.status.Err = ""
	if err != nil {
		f.status.Err = err.Error()
	}
}

func (f *Follower) publishRetry() {
	paths := make([]string, 0, len(f.retry))
	for p := range f.retry {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	f.mu.Lock()
	f.status.Retrying = paths
	f.mu.Unlock()
}

// Run follows the root until ctx ends. It watches first and reconciles second, so a change made
// during the scan is already in the watch stream; a watch that ends is set up again, with a full
// reconcile. When neither Watch nor Poll can be set up, the follower is degraded: it reconciles the
// whole root every poll interval, which still finds changes everywhere a scan can read, and retries
// the setup with backoff.
func (f *Follower) Run(ctx context.Context) error {
	backoff := f.opts.MinBackoff
	for ctx.Err() == nil {
		events, mode, err := f.watch(ctx)
		if err != nil {
			f.setFollowing(Degraded, err)
			f.reconcile(ctx, ".")
			f.degraded(ctx, backoff)
			backoff = min(2*backoff, f.opts.MaxBackoff)
			continue
		}
		f.setFollowing(mode, nil)
		f.reconcile(ctx, ".")
		start := time.Now()
		f.drain(ctx, events)
		if ctx.Err() != nil {
			break
		}
		// the watch ended without ctx (its root went, a subtree it could not watch): set it up again
		// after a pause, unless it had been running long enough to count as healthy
		if time.Since(start) > f.opts.MaxBackoff {
			backoff = f.opts.MinBackoff
		}
		if !sleepCtx(ctx, backoff) {
			break
		}
		backoff = min(2*backoff, f.opts.MaxBackoff)
	}
	return ctx.Err()
}

// watch sets up a native watch when the driver has one and it succeeds, else a poll.
func (f *Follower) watch(ctx context.Context) (<-chan vfs.Event, string, error) {
	var werr error
	if w, ok := f.fsys.(vfs.Watcher); ok {
		events, err := w.Watch(ctx, ".", vfs.Recursive())
		if err == nil {
			return events, Watching, nil
		}
		werr = err
	}
	events, err := vfs.Poll(ctx, f.fsys, ".", f.opts.PollInterval, vfs.Recursive())
	if err == nil {
		return events, Polling, nil
	}
	if werr != nil {
		return nil, Degraded, errors.Join(werr, err)
	}
	return nil, Degraded, err
}

// drain handles events until the channel closes or ctx ends, rescanning due retry subtrees between.
func (f *Follower) drain(ctx context.Context, events <-chan vfs.Event) {
	for {
		var due <-chan time.Time
		var t *time.Timer
		if next, ok := f.nextRetry(); ok {
			t = time.NewTimer(max(0, time.Until(next)))
			due = t.C
		}
		stop := func() {
			if t != nil {
				t.Stop()
			}
		}
		select {
		case <-ctx.Done():
			stop()
			return
		case ev, ok := <-events:
			stop()
			if !ok {
				return
			}
			f.handle(ctx, ev)
		case <-due:
			f.rescanDue(ctx)
		case <-f.rescan:
			stop()
			f.reconcile(ctx, ".")
		}
	}
}

// degraded runs full reconciles every poll interval for one backoff period, then returns so the
// setup is retried.
func (f *Follower) degraded(ctx context.Context, wait time.Duration) {
	retry := time.NewTimer(wait)
	defer retry.Stop()
	tick := time.NewTicker(f.opts.PollInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-retry.C:
			return
		case <-tick.C:
			f.reconcile(ctx, ".")
		case <-f.rescan:
			f.reconcile(ctx, ".")
		}
	}
}

func (f *Follower) handle(ctx context.Context, ev vfs.Event) {
	switch {
	case ev.Op == vfs.OpOverflow && ev.Path == "":
		// events were lost for everything watched: the kernel's queue overflowed on a watch that goes
		// on, or the watch is ending (a close then follows, and the new watch's scan repeats this one)
		f.reconcile(ctx, ".")
	case ev.Op == vfs.OpOverflow:
		f.reconcile(ctx, ev.Path)
	case ev.Op == vfs.OpRemove:
		// a directory moved out or deleted reports only itself: its indexed files go with it
		f.touch(ev.Path)
		for _, p := range f.store.PathsUnder(ev.Path) {
			f.queue.Touch(p)
		}
	default:
		f.touch(ev.Path)
	}
}

// touch queues p when the indexer could care: it matches the workspace, or it is indexed (and may
// no longer be eligible).
func (f *Follower) touch(p string) {
	if f.match(p) {
		f.queue.Touch(p)
		return
	}
	if _, ok := f.store.Version(p); ok {
		f.queue.Touch(p)
	}
}

func (f *Follower) match(p string) bool { return f.opts.Match == nil || f.opts.Match(p) }

// reconcile queues every eligible file under dir whose Version differs from the index, and every
// indexed path under dir that is gone. vfs.Walk never enters a symlink, and an unreadable directory
// is yielded as an error while the walk continues with its siblings: that subtree goes into the retry
// set, and its indexed documents are UNKNOWN, not gone, so none is touched as missing.
func (f *Follower) reconcile(ctx context.Context, dir string) {
	seen := map[string]bool{}
	failed := map[string]error{}
	for fi, err := range vfs.Walk(ctx, f.fsys, dir) {
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, fs.ErrNotExist) {
				continue // gone before the walk reached it: what was indexed there is missing below
			}
			if f.opts.Excluded != nil && f.opts.Excluded(fi.Path) {
				continue // not the workspace's: nothing indexed is under it
			}
			failed[fi.Path] = err
			continue
		}
		if !fi.IsRegular() || !f.match(fi.Path) {
			continue
		}
		seen[fi.Path] = true
		if v, ok := f.store.Version(fi.Path); !ok || v != fi.Version {
			f.queue.Touch(fi.Path)
		}
	}
	if ctx.Err() != nil {
		return
	}
	f.updateRetry(dir, failed)
	for _, p := range f.store.PathsUnder(dir) {
		if !seen[p] && !f.covered(p) {
			f.queue.Touch(p) // the indexer Stats it and finds it gone or ineligible
		}
	}
}

// updateRetry records a scan of dir: its failed subtrees are (re)scheduled with backoff, and retry
// entries under dir the scan read are done.
func (f *Follower) updateRetry(dir string, failed map[string]error) {
	now := time.Now()
	for p, e := range f.retry {
		if _, still := failed[p]; !still && under(p, dir) && e != nil {
			delete(f.retry, p)
		}
	}
	for p, err := range failed {
		e, ok := f.retry[p]
		if !ok {
			e = &retryEntry{delay: f.opts.MinBackoff}
			f.retry[p] = e
		} else {
			e.delay = min(2*e.delay, f.opts.MaxBackoff)
		}
		e.due, e.err = now.Add(e.delay), err
	}
	f.publishRetry()
}

func (f *Follower) covered(p string) bool {
	for r := range f.retry {
		if under(p, r) {
			return true
		}
	}
	return false
}

func (f *Follower) nextRetry() (time.Time, bool) {
	var next time.Time
	found := false
	for _, e := range f.retry {
		if !found || e.due.Before(next) {
			next, found = e.due, true
		}
	}
	return next, found
}

// rescanDue rescans the retry subtrees whose time has come.
func (f *Follower) rescanDue(ctx context.Context) {
	now := time.Now()
	var due []string
	for p, e := range f.retry {
		if !e.due.After(now) {
			due = append(due, p)
		}
	}
	sort.Strings(due)
	for _, p := range due {
		if _, ok := f.retry[p]; ok { // an earlier rescan may have covered it
			f.reconcile(ctx, p)
		}
	}
}

// under reports whether p is dir or inside it; everything is under ".".
func under(p, dir string) bool {
	return dir == "." || p == dir || strings.HasPrefix(p, dir+"/")
}

// sleepCtx waits for d or ctx, reporting whether d elapsed.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
