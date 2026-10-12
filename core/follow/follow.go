package follow

import (
	"context"
	"errors"
	"io/fs"
	"sort"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/core/paths"
)

// Store is what the follower reads of the index.
type Store interface {
	// Version is the Version a path was indexed at, and whether it is indexed; a path not indexed
	// is no error.
	Version(ctx context.Context, path string) (vfs.Version, bool, error)
	// PathsUnder lists the indexed paths under dir ("." for all of them).
	PathsUnder(ctx context.Context, dir string) ([]string, error)
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

// options are a follower's settings, each set by an Option; zero values take the defaults.
type options struct {
	pollInterval time.Duration // Poll's listing interval, and degraded mode's full-scan interval (default 2 s)
	minBackoff   time.Duration // the first retry delay (default 1 s)
	maxBackoff   time.Duration // the retry delay's cap (default 1 min)
	match        func(path string) bool
	excluded     func(path string) bool
}

// Option sets one of a follower's settings.
type Option func(*options)

// WithPollInterval is Poll's listing interval, and degraded mode's full-scan interval (default 2 s).
func WithPollInterval(d time.Duration) Option { return func(o *options) { o.pollInterval = d } }

// WithBackoff is a retry's first delay (default 1 s) and its cap (default 1 min).
func WithBackoff(first, most time.Duration) Option {
	return func(o *options) { o.minBackoff, o.maxBackoff = first, most }
}

// WithMatch reports whether a path belongs to the workspace (include, not exclude); default all.
func WithMatch(match func(path string) bool) Option { return func(o *options) { o.match = match } }

// WithExcluded reports whether a path lies in an excluded directory (.git, node_modules). Such a
// directory is never walked, watched or polled — its contents are not the workspace's, and on a
// large root they are most of it — and one that cannot be read is not retried. Default none.
func WithExcluded(excluded func(path string) bool) Option {
	return func(o *options) { o.excluded = excluded }
}

// Follower follows one workspace root.
type Follower struct {
	fsys  vfs.FS
	store Store
	queue Queue
	opts  options

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
func New(fsys vfs.FS, store Store, queue Queue, set ...Option) *Follower {
	var opts options
	for _, s := range set {
		s(&opts)
	}
	if opts.pollInterval <= 0 {
		opts.pollInterval = 2 * time.Second
	}
	if opts.minBackoff <= 0 {
		opts.minBackoff = time.Second
	}
	if opts.maxBackoff <= 0 {
		opts.maxBackoff = time.Minute
	}
	if opts.maxBackoff < opts.minBackoff {
		opts.maxBackoff = opts.minBackoff
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
	backoff := f.opts.minBackoff
	for ctx.Err() == nil {
		events, mode, err := f.watch(ctx)
		if err != nil {
			f.setFollowing(Degraded, err)
			f.reconcile(ctx, ".")
			f.degraded(ctx, backoff)
			backoff = min(2*backoff, f.opts.maxBackoff)
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
		if time.Since(start) > f.opts.maxBackoff {
			backoff = f.opts.minBackoff
		}
		if !clock.Sleep(ctx, backoff) {
			break
		}
		backoff = min(2*backoff, f.opts.maxBackoff)
	}
	return ctx.Err()
}

// watch sets up a native watch when the driver has one and it succeeds, else a poll. Neither looks
// inside an excluded directory.
func (f *Follower) watch(ctx context.Context) (<-chan vfs.Event, string, error) {
	opts := []vfs.WatchOption{vfs.Recursive(), vfs.SkipDirs(f.opts.excluded)}
	var werr error
	if w, ok := f.fsys.(vfs.Watcher); ok {
		events, err := w.Watch(ctx, ".", opts...)
		if err == nil {
			return events, Watching, nil
		}
		werr = err
	}
	events, err := vfs.Poll(ctx, f.fsys, ".", f.opts.pollInterval, opts...)
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
	tick := time.NewTicker(f.opts.pollInterval)
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
		f.touch(ctx, ev.Path)
		indexed, err := f.store.PathsUnder(ctx, ev.Path)
		if err != nil {
			// what was indexed there is unknown: rescanned with backoff, and missing then
			f.updateRetry(ev.Path, map[string]error{ev.Path: err})
			return
		}
		for _, p := range indexed {
			f.queue.Touch(p)
		}
	default:
		f.touch(ctx, ev.Path)
	}
}

// touch queues p when the indexer could care: it matches the workspace, or it is indexed (and may
// no longer be eligible), or the index could not say whether it is (the indexer decides).
func (f *Follower) touch(ctx context.Context, p string) {
	if f.match(p) {
		f.queue.Touch(p)
		return
	}
	if _, ok, err := f.store.Version(ctx, p); ok || err != nil {
		f.queue.Touch(p)
	}
}

func (f *Follower) match(p string) bool { return f.opts.match == nil || f.opts.match(p) }

// reconcile queues every eligible file under dir whose Version differs from the index, and every
// indexed path under dir that is gone — which includes what is indexed inside a directory excluded
// since, as the walk does not enter it. vfs.Walk never enters a symlink, and an unreadable directory
// is yielded as an error while the walk continues with its siblings: that subtree goes into the retry
// set, and its indexed documents are UNKNOWN, not gone, so none is touched as missing.
func (f *Follower) reconcile(ctx context.Context, dir string) {
	seen := map[string]bool{}
	failed := map[string]error{}
	for fi, err := range vfs.Walk(ctx, f.fsys, dir, vfs.WalkSkipDirs(f.opts.excluded)) {
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, fs.ErrNotExist) {
				continue // gone before the walk reached it: what was indexed there is missing below
			}
			if f.opts.excluded != nil && f.opts.excluded(fi.Path) {
				continue // not the workspace's: nothing indexed is under it
			}
			failed[fi.Path] = err
			continue
		}
		if !fi.IsRegular() || !f.match(fi.Path) {
			continue
		}
		seen[fi.Path] = true
		// an index that cannot say has the indexer read it: it compares the version itself
		if v, ok, err := f.store.Version(ctx, fi.Path); err != nil || !ok || v != fi.Version {
			f.queue.Touch(fi.Path)
		}
	}
	if ctx.Err() != nil {
		return
	}
	indexed, err := f.store.PathsUnder(ctx, dir)
	if err != nil && ctx.Err() == nil {
		failed[dir] = err // what is gone under dir is unknown: dir is rescanned with backoff
	}
	f.updateRetry(dir, failed)
	if err != nil {
		return
	}
	for _, p := range indexed {
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
		if _, still := failed[p]; !still && paths.Under(p, dir) && e != nil {
			delete(f.retry, p)
		}
	}
	for p, err := range failed {
		e, ok := f.retry[p]
		if !ok {
			e = &retryEntry{delay: f.opts.minBackoff}
			f.retry[p] = e
		} else {
			e.delay = min(2*e.delay, f.opts.maxBackoff)
		}
		e.due, e.err = now.Add(e.delay), err
	}
	f.publishRetry()
}

func (f *Follower) covered(p string) bool {
	for r := range f.retry {
		if paths.Under(p, r) {
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
