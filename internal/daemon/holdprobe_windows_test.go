package daemon

// SCRATCH (not for merge): which handle holds a served root on Windows, so that removing it fails
// with a sharing violation (TestConfigure_SaysWhatItCouldNotDo). Each step logs whether the root,
// and each entry under it, can be renamed away and back: a rename fails as a removal does while a
// handle without FILE_SHARE_DELETE is open, and undoes itself, so every step sees the same tree.

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs/local"
)

// movable is what of root can be renamed now: "root ok" or the error, then each entry that can't.
func movable(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	if err := os.Rename(root, root+".probe"); err != nil {
		b.WriteString("root: " + err.Error())
	} else {
		if err := os.Rename(root+".probe", root); err != nil {
			t.Fatalf("the root could not be moved back: %v", err)
		}
		b.WriteString("root ok")
	}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if e := os.Rename(p, p+".probe"); e != nil {
			b.WriteString("; " + strings.TrimPrefix(p, root) + ": " + e.Error())
			return nil
		}
		if e := os.Rename(p+".probe", p); e != nil {
			t.Fatalf("%s could not be moved back: %v", p, e)
		}
		return nil
	})
	return b.String()
}

// The local driver alone: its os.Root, then its watch (ReadDirectoryChangesW), each released.
func TestProbeWhatTheLocalDriverHolds(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.md", "sub/b.md"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte("# x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("PROBE local 0 nothing open: %s", movable(t, root))
	fsys, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("PROBE local 1 local.New (os.Root open): %s", movable(t, root))
	ctx, cancel := context.WithCancel(context.Background())
	events, err := fsys.Watch(ctx, ".")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	t.Logf("PROBE local 2 + Watch(\".\"): %s", movable(t, root))
	cancel()
	for range events { // drained: the watch's goroutine has let its handle go
	}
	time.Sleep(200 * time.Millisecond)
	t.Logf("PROBE local 3 watch cancelled, os.Root open: %s", movable(t, root))
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("PROBE local 4 closed: %s", movable(t, root))
}

// A served workspace: served, its schema poll stopped, halted (indexer and follower), stopped
// (its root closed), each step's hold logged; then the removal the configure cell makes.
func TestProbeWhatAServedRootHolds(t *testing.T) {
	m, _ := openWith(t, Options{Databases: true})
	kbWith(t, m, map[string]string{"a.md": "# A\n"}, "**/*.md")
	indexed(t, m, "kb", 1)
	m.mu.Lock()
	s := m.served["kb"]
	m.mu.Unlock()
	root := s.root
	t.Logf("PROBE served 0 served and indexed: %s", movable(t, root))
	s.schema.mu.Lock()
	if s.schema.cancel != nil {
		s.schema.cancel()
	}
	s.schema.mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	t.Logf("PROBE served 1 schema poll stopped: %s", movable(t, root))
	s.halt()
	time.Sleep(100 * time.Millisecond)
	t.Logf("PROBE served 2 halted (indexer, follower): %s", movable(t, root))
	s.stop()
	time.Sleep(100 * time.Millisecond)
	t.Logf("PROBE served 3 stopped (root closed): %s", movable(t, root))
	if err := os.RemoveAll(root); err != nil {
		t.Logf("PROBE served 4 RemoveAll: %v", err)
	} else {
		t.Logf("PROBE served 4 RemoveAll: ok")
	}
}
