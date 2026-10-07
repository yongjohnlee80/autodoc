package tui

import (
	"context"
	"fmt"
	"testing"
	"time"
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

func (r *running) recentListedNow() []recentDoc {
	return onLoop(r, func() []recentDoc { return append([]recentDoc(nil), r.h.recentRows...) })
}

// waitRecentStored waits for the daemon's store to hold the recent files want.
func (d *daemon) waitRecentStored(t *testing.T, want string) {
	t.Helper()
	var got string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		all, _ := d.db.Preferences(context.Background())
		if got = all[prefRecent]; got == want {
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
	onLoop(r, func() bool { r.h.recentSelect(1); return true })
	r.waitFile(t, "c.md")
	if ws := onLoop(r, func() string { return r.h.ws }); ws != "notes" {
		t.Fatalf("the recent file of notes opened in %q", ws)
	}
	if got := r.editorText(); got != "charlie\n" {
		t.Fatalf("the page shows %q, want c.md's", got)
	}

	// Kept: a new TUI lists them.
	again := attached(t, d)
	if n := onLoop(again, func() int { return len(again.h.prefs.recent) }); n != 3 {
		t.Errorf("a new TUI reads %d recent files, want 3", n)
	}

	// Clear list forgets them.
	onLoop(r, func() bool { r.h.openRecent(); r.h.clearRecent(); return true })
	r.s.WaitForText(t, "no recent files")
	d.waitRecentStored(t, "[]")
}

// A file of a workspace that is gone, or gone from the workspace in use, is not listed; it stays
// kept, as its workspace may come back.
func TestRecentFilesThatAreGoneAreNotListed(t *testing.T) {
	_, r := runRecentTUI(t, map[string]string{prefRecent: recentJSON([]recentDoc{
		{Workspace: "old", Path: "x.md"}, {Workspace: "kb", Path: "gone.md"},
		{Workspace: "kb", Path: "b.md"}, {Workspace: "notes", Path: "c.md"},
	})})
	r.s.WaitFor(t, "kb in use", func(string) bool { return onLoop(r, func() string { return r.h.ws }) == "kb" })
	onLoop(r, func() bool { r.h.openRecent(); return true })
	r.s.WaitForText(t, "2 recent files")
	got := r.recentListedNow()
	if len(got) != 2 || got[0] != (recentDoc{"kb", "b.md"}) || got[1] != (recentDoc{"notes", "c.md"}) {
		t.Fatalf("listed %v, want kb/b.md and notes/c.md", got)
	}
	if n := onLoop(r, func() int { return len(r.h.prefs.recent) }); n != 4 {
		t.Fatalf("listing dropped kept files: %d left of 4", n)
	}
}

// The list keeps the newest maxRecent, each once; a preference that is not a list reads as none.
func TestRecentFilesKeepTheNewestOnce(t *testing.T) {
	var docs []recentDoc
	for i := range maxRecent + 5 {
		docs = withRecent(docs, recentDoc{"kb", fmt.Sprintf("%d.md", i)})
	}
	if len(docs) != maxRecent || docs[0].Path != fmt.Sprintf("%d.md", maxRecent+4) || docs[maxRecent-1].Path != "5.md" {
		t.Fatalf("kept %d, first %v, last %v", len(docs), docs[0], docs[len(docs)-1])
	}
	docs = withRecent(docs, recentDoc{"kb", "10.md"})
	if len(docs) != maxRecent || docs[0].Path != "10.md" || docs[1].Path != fmt.Sprintf("%d.md", maxRecent+4) {
		t.Fatalf("again: kept %d, first %v, %v", len(docs), docs[0], docs[1])
	}
	for _, d := range docs[1:] {
		if d.Path == "10.md" {
			t.Fatal("10.md listed twice")
		}
	}
	for _, bad := range []string{"nope", `{"workspace":"kb"}`, `[{"workspace":"kb","path":"a.md"},5]`} {
		if got := recentOf(bad); got != nil {
			t.Fatalf("read %v from %s", got, bad)
		}
	}
	if recentJSON(nil) != "[]" {
		t.Fatal("no recent files written as other than []")
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
	onLoop(r, func() bool { r.h.filesAll = nil; r.h.openRecent(); return true })
	r.s.WaitForText(t, "2 recent files, the newest first") // kb's two; c.md waits for notes' files
	onLoop(r, func() bool { r.h.listFiles(); return true })
	r.s.WaitForText(t, "3 recent files, the newest first")
}
