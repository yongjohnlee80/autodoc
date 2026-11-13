package tui

import (
	"context"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/explorer"
)

// recent_test.go: File › Recent files… (SPC r) — the files opened, newest first, across
// workspaces, kept in the daemon's store; one of another workspace opens there.

func runRecentTUI(t *testing.T, prefs map[string]string) (*daemon, *running) {
	t.Helper()
	d := startDaemonWith(t, "", map[string][]string{
		"kb":    {"a.md", "alpha\n", "b.md", "bravo\n"},
		"notes": {"c.md", "charlie\n"},
	}, daemonOpts{prefs: prefs})
	return d, attached(t, d)
}

func (r *running) recentListedNow() []explorer.RecentDoc {
	return onLoop(r, func() []explorer.RecentDoc { return append([]explorer.RecentDoc(nil), r.h.recent.Rows.All()...) })
}

// waitRecentStored waits for the daemon's store to hold the recent files want.
func (d *daemon) waitRecentStored(t *testing.T, want string) {
	t.Helper()
	var got string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		all, _ := d.db.Preferences(context.Background())
		if got = all[explorer.PrefRecent]; got == want {
			return
		}
	}
	t.Fatalf("stored recent files %s, want %s", got, want)
}

func TestRecentFilesAreTheOnesOpenedNewestFirstAcrossWorkspaces(t *testing.T) {
	d, r := runRecentTUI(t, nil)
	r.s.WaitFor(t, "kb in use", func(string) bool { return onLoop(r, func() string { return r.h.ws }) == "kb" })
	for _, p := range []string{"a.md", "b.md"} {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	onLoop(r, func() bool { r.h.openIn("notes", "c.md"); return true })
	r.waitFile(t, "c.md")
	// a.md again: first, once
	onLoop(r, func() bool { r.h.openIn("kb", "a.md"); return true })
	r.waitFile(t, "a.md")
	d.waitRecentStored(t, `[{"workspace":"kb","path":"a.md"},{"workspace":"notes","path":"c.md"},{"workspace":"kb","path":"b.md"}]`)

	r.leader(t, 'r')
	r.s.WaitForText(t, "3 recent files, the newest first")
	r.s.WaitForText(t, "notes")
	// The row of another workspace opens there.
	onLoop(r, func() bool { r.h.recent.Choose(1); return true })
	r.waitFile(t, "c.md")
	if ws := onLoop(r, func() string { return r.h.ws }); ws != "notes" {
		t.Fatalf("the recent file of notes opened in %q", ws)
	}
	if got := r.editorText(); got != "charlie\n" {
		t.Fatalf("the page shows %q, want c.md's", got)
	}

	// Kept: a new TUI lists them.
	again := attached(t, d)
	if n := onLoop(again, func() int { return len(again.h.recent.Docs()) }); n != 3 {
		t.Errorf("a new TUI reads %d recent files, want 3", n)
	}

	// Clear list forgets them, at once: reopening the dialog, and a listing of the workspace's
	// files, bring none back (the live list is the preference, set through its row).
	onLoop(r, func() bool { r.h.recent.Open(); r.h.recent.Clear(); return true })
	r.s.WaitForText(t, "no recent files")
	d.waitRecentStored(t, "[]")
	if n := onLoop(r, func() int { r.h.recent.Open(); r.h.listFiles(); return r.h.recent.Rows.Len() + len(r.h.recent.Docs()) }); n != 0 {
		t.Errorf("after Clear list and a reopening, %d recent files came back", n)
	}
}

// A file of a workspace that is gone, or gone from the workspace in use, is not listed; it stays
// kept, as its workspace may come back.
func TestRecentFilesThatAreGoneAreNotListed(t *testing.T) {
	_, r := runRecentTUI(t, map[string]string{explorer.PrefRecent: explorer.RecentJSON([]explorer.RecentDoc{
		{Workspace: "old", Path: "x.md"}, {Workspace: "kb", Path: "gone.md"},
		{Workspace: "kb", Path: "b.md"}, {Workspace: "notes", Path: "c.md"},
	})})
	r.s.WaitFor(t, "kb in use", func(string) bool { return onLoop(r, func() string { return r.h.ws }) == "kb" })
	onLoop(r, func() bool { r.h.recent.Open(); return true })
	r.s.WaitForText(t, "2 recent files")
	got := r.recentListedNow()
	if len(got) != 2 || got[0] != (explorer.RecentDoc{Workspace: "kb", Path: "b.md"}) || got[1] != (explorer.RecentDoc{Workspace: "notes", Path: "c.md"}) {
		t.Fatalf("listed %v, want kb/b.md and notes/c.md", got)
	}
	if n := onLoop(r, func() int { return len(r.h.recent.Docs()) }); n != 4 {
		t.Fatalf("listing dropped kept files: %d left of 4", n)
	}
}

// TestTheRecentListFollowsTheFilesOfAWorkspaceJustEntered: Recent files opened before the workspace's
// files have been listed (just entered) lists its files once they come, not only the other
// workspaces'. CI caught the dialog at "1 recent files" with three stored.
func TestTheRecentListFollowsTheFilesOfAWorkspaceJustEntered(t *testing.T) {
	d, r := runRecentTUI(t, nil)
	r.s.WaitFor(t, "kb in use", func(string) bool { return onLoop(r, func() string { return r.h.ws }) == "kb" })
	for _, p := range []string{"a.md", "b.md"} {
		onLoop(r, func() bool { r.h.openPath(p); return true })
		r.waitFile(t, p)
	}
	onLoop(r, func() bool { r.h.openIn("notes", "c.md"); return true })
	r.waitFile(t, "c.md")
	d.waitRecentStored(t, `[{"workspace":"notes","path":"c.md"},{"workspace":"kb","path":"b.md"},{"workspace":"kb","path":"a.md"}]`)
	// notes in use, its files not listed yet: as just after entering it
	onLoop(r, func() bool { r.h.resetFiles(); r.h.recent.Open(); return true })
	r.s.WaitForText(t, "2 recent files, the newest first") // kb's two; c.md waits for notes' files
	onLoop(r, func() bool { r.h.listFiles(); return true })
	r.s.WaitForText(t, "3 recent files, the newest first")
}
