package tui

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestAnOpenerThatCantStartOrIsCancelled: an opener that can't start is an error; one whose context
// ends before it fails or the grace passes is left to run, without waiting out the grace.
func TestAnOpenerThatCantStartOrIsCancelled(t *testing.T) {
	if err := startOpener(context.Background(), filepath.Join(t.TempDir(), "no-such-opener")); err == nil {
		t.Error("an opener that can't start gave no error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := startOpener(ctx, "sleep", "1"); err != nil {
		t.Errorf("an opener whose context ended: %v; want it left to run", err)
	}
	if d := time.Since(start); d >= openerGrace {
		t.Errorf("returned after %v: want the context's end, before the grace (%v)", d, openerGrace)
	}
}

// TestOpenWithTheDefaultAppWaitsForTheWorkspacesFolder: a file of a workspace whose folder is not
// listed yet has no path on disk, so Open with Default App says to try again, and opens nothing.
func TestOpenWithTheDefaultAppWaitsForTheWorkspacesFolder(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	r := attached(t, d)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	opened := make(chan string, 1)
	onLoop(r, func() bool {
		r.h.browser = func(_ context.Context, path string) error { opened <- path; return nil }
		r.h.file.ws = "not-listed-yet" // a workspace the list doesn't hold
		r.h.openWithDefaultApp()
		return true
	})
	r.waitNotice(t, onLoop(r, func() string { return r.h.tr("autodoc.notice.html.the_workspace_s_folder_is_not_known_yet_try_agai") }))
	select {
	case p := <-opened:
		t.Fatalf("the default app was given %q, though the workspace's folder isn't known", p)
	default:
	}
}
