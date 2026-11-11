package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/explorer"
)

// TestANoteIndexedBeforeTheFirstPollIsListed: a note indexed after the workspace's entry listed its
// files, but before the first progress poll answered, is listed: that poll's cursor already
// counts the note, and the files are listed again after it. Forced: the first index.status of the
// workspace is held until the entry's listing has landed and the daemon has indexed the note.
func TestANoteIndexedBeforeTheFirstPollIsListed(t *testing.T) {
	t.Parallel()
	root := fileDir(t, "a.md", "a\n", "b.md", "b\n")
	d := startManaged(t, map[string]string{"alpha": root})
	var held atomic.Bool
	hold := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(hold) }) }
	sess := NewSession(d.sock, nil)
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "index.status" && len(params) > 0 && params[0] == "alpha" && held.CompareAndSwap(false, true) {
			<-hold
		}
	})
	r := runTUI(t, sess, Options{Workspace: "alpha"})
	t.Cleanup(release) // registered last, so it runs first: before the window and the daemon stop
	r.waitAttached(t, "alpha")
	listed := func(p string) func() bool {
		return func() bool { return onLoop(r, func() bool { return slices.Contains(r.h.files.Paths, p) }) }
	}
	r.waitWithin(t, "the entry's listing", daemonWait, listed("b.md"))
	r.waitWithin(t, "the first progress poll held", daemonWait, held.Load)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	rewrite(t, root, "sub/c.md", "c\n")
	r.waitWithin(t, "sub/c.md indexed by the daemon", indexWait, func() bool {
		paths, err := explorer.ListAll(context.Background(), r.h.call, "alpha")
		return err == nil && slices.Contains(paths, "sub/c.md")
	})
	if listed("sub/c.md")() {
		t.Fatal("sub/c.md listed before the first poll answered: the cell forces nothing")
	}
	release() // the first poll answers now, its cursor counting the note
	r.waitWithin(t, "sub/c.md listed after the first poll", daemonWait, listed("sub/c.md"))
}
