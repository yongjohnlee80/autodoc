package follow_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

var match = workspace.NewMatcher([]string{"**/*.md"}, []string{".git/**"}).Match

// model is an indexer reduced to the rules ADR 0203 gives it: for a touched path it Stats the
// entry, deletes the document if the entry is gone or no longer eligible, and records its Version
// otherwise. A Stat that fails for another reason (permissions) leaves the document alone.
type model struct {
	fsys    vfs.FS
	mu      sync.Mutex
	docs    map[string]vfs.Version
	touched map[string]int // every path the follower handed over
}

func newModel(fsys vfs.FS) *model {
	return &model{fsys: fsys, docs: map[string]vfs.Version{}, touched: map[string]int{}}
}

func (m *model) Version(p string) (vfs.Version, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.docs[p]
	return v, ok
}

func (m *model) PathsUnder(dir string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for p := range m.docs {
		if dir == "." || p == dir || strings.HasPrefix(p, dir+"/") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

func (m *model) Touch(p string) {
	fi, err := m.fsys.Stat(context.Background(), p)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.touched[p]++
	switch {
	case errors.Is(err, fs.ErrNotExist):
		delete(m.docs, p)
	case err != nil:
		// unknown: keep what is indexed
	case !fi.IsRegular() || !match(p):
		delete(m.docs, p)
	default:
		m.docs[p] = fi.Version
	}
}

func (m *model) snapshot() map[string]vfs.Version {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]vfs.Version, len(m.docs))
	for k, v := range m.docs {
		out[k] = v
	}
	return out
}

// truth is what the index should hold: every eligible regular file of the root, at its Version.
func truth(t *testing.T, fsys vfs.FS) map[string]vfs.Version {
	t.Helper()
	out := map[string]vfs.Version{}
	for fi, err := range vfs.Walk(context.Background(), fsys, ".") {
		if err != nil {
			t.Fatalf("walking the truth: %v", err)
		}
		if fi.IsRegular() && match(fi.Path) {
			out[fi.Path] = fi.Version
		}
	}
	return out
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	within(t, 10*time.Second, what, cond)
}

func within(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func converges(t *testing.T, m *model, fsys vfs.FS, what string) {
	t.Helper()
	var got, want map[string]vfs.Version
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, want = m.snapshot(), truth(t, fsys)
		if reflect.DeepEqual(got, want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: the index did not converge\n got  %v\n want %v", what, got, want)
}

var fast = follow.Options{PollInterval: 20 * time.Millisecond, MinBackoff: 10 * time.Millisecond, MaxBackoff: 80 * time.Millisecond, Match: match}

func start(t *testing.T, fsys vfs.FS, m *model) *follow.Follower {
	t.Helper()
	return startWith(t, fsys, m, fast)
}

func startWith(t *testing.T, fsys vfs.FS, m *model, opts follow.Options) *follow.Follower {
	t.Helper()
	f := follow.New(fsys, m, m, opts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after its context ended")
		}
	})
	return f
}

func write(t *testing.T, root, p, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(p))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func localRoot(t *testing.T) (string, vfs.FS) {
	t.Helper()
	root := t.TempDir()
	fsys, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fsys.Close() })
	return root, fsys
}

// TestFollowsExternalChanges: writes, an editor's atomic save, a delete, a move within the root and
// a populated directory moved in all reach the index while the follower watches.
func TestFollowsExternalChanges(t *testing.T) {
	root, fsys := localRoot(t)
	write(t, root, "a.md", "a")
	write(t, root, "notes/b.md", "b")
	write(t, root, "notes/skip.txt", "not a note")
	write(t, root, ".git/c.md", "excluded")
	m := newModel(fsys)
	f := start(t, fsys, m)
	converges(t, m, fsys, "the start reconcile")
	eventually(t, "the watch", func() bool { return f.Status().Following == follow.Watching })

	write(t, root, "c.md", "new")
	converges(t, m, fsys, "a new file")
	write(t, root, "a.md", "changed")
	converges(t, m, fsys, "a changed file")

	// an editor's atomic save: a temp file renamed over the original
	write(t, root, "notes/.b.md.swp", "saved")
	if err := os.Rename(filepath.Join(root, "notes/.b.md.swp"), filepath.Join(root, "notes/b.md")); err != nil {
		t.Fatal(err)
	}
	converges(t, m, fsys, "an atomic save")

	if err := os.Remove(filepath.Join(root, "c.md")); err != nil {
		t.Fatal(err)
	}
	converges(t, m, fsys, "a delete")

	if err := os.Rename(filepath.Join(root, "a.md"), filepath.Join(root, "notes/a-moved.md")); err != nil {
		t.Fatal(err)
	}
	converges(t, m, fsys, "a move within the root")

	outside := t.TempDir()
	write(t, outside, "batch/x.md", "x")
	write(t, outside, "batch/deep/y.md", "y")
	if err := os.Rename(filepath.Join(outside, "batch"), filepath.Join(root, "batch")); err != nil {
		t.Fatal(err)
	}
	converges(t, m, fsys, "a populated directory moved in")

	if err := os.Rename(filepath.Join(root, "batch"), filepath.Join(outside, "batch-back")); err != nil {
		t.Fatal(err)
	}
	converges(t, m, fsys, "a populated directory moved out")
	// the indexer would refuse an excluded file anyway; the follower does not even hand one over,
	// so a busy .git costs the indexer nothing
	m.mu.Lock()
	defer m.mu.Unlock()
	for p := range m.touched {
		if strings.HasPrefix(p, ".git/") || strings.HasSuffix(p, ".txt") {
			t.Errorf("the follower touched %s, which the workspace excludes", p)
		}
	}
}

// TestStartReconcileFindsChangesWhileDown: what changed while nothing followed — a new file, a
// changed one, a deleted one — is found at start.
func TestStartReconcileFindsChangesWhileDown(t *testing.T) {
	root, fsys := localRoot(t)
	write(t, root, "kept.md", "same")
	write(t, root, "changed.md", "new content")
	write(t, root, "added.md", "added while down")
	m := newModel(fsys)
	fi, _ := fsys.Stat(context.Background(), "kept.md")
	m.docs["kept.md"] = fi.Version
	m.docs["changed.md"] = "an old version"
	m.docs["deleted.md"] = "indexed before the delete"
	start(t, fsys, m)
	converges(t, m, fsys, "the start reconcile")
}

// hookFS runs a hook after the first ReadDir of the root, the moment the start scan has its
// listing: a file made then is not in the scan, and only the watch set up before it can see it.
type hookFS struct {
	vfs.FS
	once sync.Once
	hook func()
}

func (h *hookFS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	list, err := h.FS.ReadDir(ctx, name)
	if name == "." {
		h.once.Do(h.hook)
	}
	return list, err
}

func (h *hookFS) Watch(ctx context.Context, dir string, opts ...vfs.WatchOption) (<-chan vfs.Event, error) {
	return h.FS.(vfs.Watcher).Watch(ctx, dir, opts...)
}

// TestChangeDuringStartScanIsNotLost: the watch is up before the scan, so a file created after the
// scan listed the root still reaches the index.
func TestChangeDuringStartScanIsNotLost(t *testing.T) {
	root, inner := localRoot(t)
	write(t, root, "a.md", "a")
	fsys := &hookFS{FS: inner, hook: func() { write(t, root, "late.md", "made during the scan") }}
	m := newModel(fsys)
	start(t, fsys, m)
	eventually(t, "the late file", func() bool { _, ok := m.Version("late.md"); return ok })
	converges(t, m, fsys, "a change during the start scan")
}

// scriptedFS is a memfs whose watch the test drives, and which counts the directories read.
type scriptedFS struct {
	*memfs.FS
	mu      sync.Mutex
	reads   map[string]int
	watches int
	events  chan vfs.Event
	fail    map[string]bool // directories whose ReadDir fails with permission denied
}

func newScripted() *scriptedFS {
	return &scriptedFS{FS: memfs.New(), reads: map[string]int{}, fail: map[string]bool{}}
}

func (s *scriptedFS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	s.mu.Lock()
	s.reads[name]++
	failing := s.fail[name]
	s.mu.Unlock()
	if failing {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return s.FS.ReadDir(ctx, name)
}

func (s *scriptedFS) Watch(ctx context.Context, dir string, opts ...vfs.WatchOption) (<-chan vfs.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.watches++
	s.events = make(chan vfs.Event, 16)
	return s.events, nil
}

func (s *scriptedFS) send(ev vfs.Event) {
	s.mu.Lock()
	ch := s.events
	s.mu.Unlock()
	ch <- ev
}

func (s *scriptedFS) setFail(dir string, on bool) {
	s.mu.Lock()
	s.fail[dir] = on
	s.mu.Unlock()
}

func (s *scriptedFS) readCounts() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for k, v := range s.reads {
		out[k] = v
	}
	return out
}

func memWrite(t *testing.T, fsys vfs.FS, p, content string) {
	t.Helper()
	ctx := context.Background()
	if dir := filepath.ToSlash(filepath.Dir(p)); dir != "." {
		if err := fsys.MkdirAll(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fsys.WriteFile(ctx, p, strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
}

// TestOverflowRescansExactlyThatDirectory: OpOverflow{dir} reconciles dir's subtree and nothing else.
func TestOverflowRescansExactlyThatDirectory(t *testing.T) {
	s := newScripted()
	memWrite(t, s, "a/x.md", "x")
	memWrite(t, s, "a/sub/z.md", "z")
	memWrite(t, s, "b/y.md", "y")
	m := newModel(s)
	start(t, s, m)
	converges(t, m, s, "the start reconcile")
	eventually(t, "the watch", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.events != nil })

	before := s.readCounts()
	memWrite(t, s, "a/x.md", "changed with no event")
	memWrite(t, s, "a/sub/new.md", "new with no event")
	s.send(vfs.Event{Path: "a", Op: vfs.OpOverflow})
	converges(t, m, s.FS, "an overflow of a/") // the truth walks memfs itself, so it is not counted
	after := s.readCounts()
	for dir, n := range after {
		if n > before[dir] && dir != "a" && dir != "a/sub" {
			t.Errorf("an overflow of a/ read %q (%d times)", dir, n-before[dir])
		}
	}
	if after["a"] == before["a"] {
		t.Error("an overflow of a/ did not read a/")
	}
}

// TestWatchEndIsSetUpAgain: a watch whose channel closes is set up again, and the full reconcile
// that follows finds what changed while it was down.
func TestWatchEndIsSetUpAgain(t *testing.T) {
	s := newScripted()
	memWrite(t, s, "a.md", "a")
	m := newModel(s)
	start(t, s, m)
	converges(t, m, s, "the start reconcile")
	eventually(t, "the first watch", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.watches == 1 })

	s.send(vfs.Event{Path: "", Op: vfs.OpOverflow}) // the watch is ending
	memWrite(t, s, "a.md", "changed while no watch was up")
	memWrite(t, s, "b.md", "added while no watch was up")
	s.mu.Lock()
	close(s.events)
	s.mu.Unlock()
	eventually(t, "the watch set up again", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.watches == 2 })
	converges(t, m, s, "the reconcile after the watch came back")
}

// TestQueueOverflowRescansEverything: OpOverflow{""} on a watch that stays open (the kernel's event
// queue overflowed) means every path may have lost events: the follower rescans the whole root and
// keeps watching.
func TestQueueOverflowRescansEverything(t *testing.T) {
	s := newScripted()
	memWrite(t, s, "a.md", "a")
	m := newModel(s)
	f := start(t, s, m)
	converges(t, m, s.FS, "the start reconcile")
	eventually(t, "the watch", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.events != nil })

	memWrite(t, s, "b.md", "made while the queue overflowed")
	memWrite(t, s, "a.md", "changed while the queue overflowed")
	s.send(vfs.Event{Path: "", Op: vfs.OpOverflow}) // and the channel stays open
	converges(t, m, s.FS, "a queue overflow")
	s.mu.Lock()
	watches := s.watches
	s.mu.Unlock()
	if st := f.Status(); st.Following != follow.Watching || watches != 1 {
		t.Errorf("after an overflow on a live watch: %s, %d watches; want the same watch", st.Following, watches)
	}
}

// TestUnreadableExcludedDirectoryIsNotRetried: a directory the workspace excludes is nobody's
// concern, so failing to read it neither puts it in the retry set nor holds anything back.
func TestUnreadableExcludedDirectoryIsNotRetried(t *testing.T) {
	s := newScripted()
	memWrite(t, s, ".git/objects/x", "an object")
	memWrite(t, s, "a.md", "a")
	s.setFail(".git", true)
	m := newModel(s)
	opts := fast
	opts.Excluded = workspace.NewMatcher([]string{"**/*.md"}, []string{".git/**"}).Excluded
	f := startWith(t, s, m, opts)
	converges(t, m, s.FS, "the start reconcile")
	time.Sleep(5 * fast.MinBackoff)
	if st := f.Status(); len(st.Retrying) != 0 {
		t.Errorf("retrying %v: an excluded directory is not the workspace's", st.Retrying)
	}
}

// faultFS is memfs with failures to inject: a Watch that fails at setup, and unreadable directories.
type faultFS struct {
	*memfs.FS
	mu         sync.Mutex
	watchErr   error
	unreadable map[string]bool
}

func newFault() *faultFS { return &faultFS{FS: memfs.New(), unreadable: map[string]bool{}} }

func (f *faultFS) ReadDir(ctx context.Context, name string) ([]vfs.FileInfo, error) {
	f.mu.Lock()
	bad := f.unreadable[name]
	f.mu.Unlock()
	if bad {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrPermission}
	}
	return f.FS.ReadDir(ctx, name)
}

func (f *faultFS) Watch(ctx context.Context, dir string, opts ...vfs.WatchOption) (<-chan vfs.Event, error) {
	f.mu.Lock()
	err := f.watchErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return f.FS.Watch(ctx, dir, opts...)
}

func (f *faultFS) set(watchErr error, unreadable ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.watchErr = watchErr
	f.unreadable = map[string]bool{}
	for _, d := range unreadable {
		f.unreadable[d] = true
	}
}

var errNoWatch = errors.New("injected: this subtree cannot be watched")

// TestWatchSetupFailureFallsBackToPoll: Watch failing at setup leaves no gap: the follower polls,
// and still converges.
func TestWatchSetupFailureFallsBackToPoll(t *testing.T) {
	ff := newFault()
	ff.set(errNoWatch)
	memWrite(t, ff, "a.md", "a")
	m := newModel(ff)
	f := start(t, ff, m)
	eventually(t, "polling", func() bool { return f.Status().Following == follow.Polling })
	converges(t, m, ff, "the start reconcile")
	memWrite(t, ff, "a.md", "changed")
	memWrite(t, ff, "b/c.md", "new")
	converges(t, m, ff, "a change seen by polling")
}

// TestDegradedStillConverges: with an unreadable early subtree a/, Watch and Poll both fail at
// setup; the full reconcile every interval still finds a new file under the later sibling b/, keeps
// a/'s indexed documents, and once a/ is readable the follower watches again and indexes a/'s change.
func TestDegradedStillConverges(t *testing.T) {
	ff := newFault()
	memWrite(t, ff, "a/old.md", "indexed before a/ became unreadable")
	memWrite(t, ff, "b/one.md", "b")
	m := newModel(ff)
	fi, _ := ff.Stat(context.Background(), "a/old.md")
	m.docs["a/old.md"] = fi.Version
	ff.set(errNoWatch, "a")
	// a setup retry far slower than the poll interval, so only degraded mode's own full reconcile can
	// find the new file in time
	slowRetry := fast
	slowRetry.MinBackoff, slowRetry.MaxBackoff = 2*time.Second, 2*time.Second
	f := startWith(t, ff, m, slowRetry)
	eventually(t, "degraded", func() bool { return f.Status().Following == follow.Degraded })
	if st := f.Status(); st.Err == "" {
		t.Error("degraded without the reason")
	}

	memWrite(t, ff, "b/two.md", "added while degraded")
	within(t, 500*time.Millisecond, "the new file under b/, before the next setup retry", func() bool { _, ok := m.Version("b/two.md"); return ok })
	if _, ok := m.Version("a/old.md"); !ok {
		t.Fatal("a document under the unreadable a/ was deleted: unreadable is unknown, not empty")
	}
	eventually(t, "a/ reported as retrying", func() bool {
		st := f.Status()
		return len(st.Retrying) == 1 && st.Retrying[0] == "a"
	})

	memWrite(t, ff, "a/old.md", "changed while unreadable")
	ff.set(nil)
	eventually(t, "the watch back", func() bool { return f.Status().Following == follow.Watching })
	converges(t, m, ff, "recovery")
	if st := f.Status(); len(st.Retrying) != 0 {
		t.Errorf("still retrying %v after recovery", st.Retrying)
	}
}

// TestUnreadableRootAtStartRecovers: a root that cannot be read at start is degraded, deletes
// nothing, and recovers.
func TestUnreadableRootAtStartRecovers(t *testing.T) {
	ff := newFault()
	memWrite(t, ff, "a.md", "a")
	m := newModel(ff)
	m.docs["a.md"] = "an old version"
	m.docs["gone.md"] = "deleted, but nothing can tell yet"
	ff.set(errNoWatch, ".")
	f := start(t, ff, m)
	eventually(t, "degraded", func() bool { return f.Status().Following == follow.Degraded })
	time.Sleep(5 * fast.PollInterval)
	if len(m.snapshot()) != 2 {
		t.Fatalf("an unreadable root changed the index: %v", m.snapshot())
	}
	ff.set(nil)
	eventually(t, "the watch", func() bool { return f.Status().Following == follow.Watching })
	converges(t, m, ff, "recovery")
}

// TestUnreadableSubtreeRetriesOnItsOwn: a subtree an overflow's rescan cannot read is retried with
// backoff while the watch runs, and converges with no further event once it reads.
func TestUnreadableSubtreeRetriesOnItsOwn(t *testing.T) {
	s := newScripted()
	memWrite(t, s, "a/x.md", "x")
	m := newModel(s)
	f := start(t, s, m)
	converges(t, m, s, "the start reconcile")
	eventually(t, "the watch", func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.events != nil })

	s.setFail("a", true)
	memWrite(t, s, "a/x.md", "changed")
	memWrite(t, s, "a/y.md", "new")
	s.send(vfs.Event{Path: "a", Op: vfs.OpOverflow})
	eventually(t, "a/ retrying", func() bool { return len(f.Status().Retrying) == 1 })
	if _, ok := m.Version("a/x.md"); !ok {
		t.Fatal("a document under the unreadable a/ was deleted")
	}
	s.setFail("a", false)
	converges(t, m, s, "the retry once a/ reads")
	eventually(t, "the retry done", func() bool { return len(f.Status().Retrying) == 0 })
}

// TestIneligibleEntriesAreDeleted: a file replaced by a directory or a symlink, or renamed out of
// include, leaves the index.
func TestIneligibleEntriesAreDeleted(t *testing.T) {
	root, fsys := localRoot(t)
	write(t, root, "dir.md", "becomes a directory")
	write(t, root, "link.md", "becomes a symlink")
	write(t, root, "renamed.md", "leaves include")
	write(t, root, "target.md", "a symlink's target")
	m := newModel(fsys)
	start(t, fsys, m)
	converges(t, m, fsys, "the start reconcile")

	for _, step := range []func() error{
		func() error { return os.Remove(filepath.Join(root, "dir.md")) },
		func() error { return os.Mkdir(filepath.Join(root, "dir.md"), 0o755) },
		func() error { return os.Remove(filepath.Join(root, "link.md")) },
		func() error { return os.Symlink("target.md", filepath.Join(root, "link.md")) },
		func() error { return os.Rename(filepath.Join(root, "renamed.md"), filepath.Join(root, "renamed.txt")) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	converges(t, m, fsys, "ineligible entries")
	got := m.snapshot()
	for _, p := range []string{"dir.md", "link.md", "renamed.md"} {
		if _, ok := got[p]; ok {
			t.Errorf("%s is still indexed", p)
		}
	}
	if len(got) != 1 {
		t.Errorf("index = %v, want only target.md", fmt.Sprint(got))
	}
}
