package tui

import (
	"strings"
	"testing"
)

// TestAFailedOpenSaysSoOnTheStatusLine: an open that fails says so on the status line, which said
// "opening <note>…" while it was read: left there, the open read as still under way, the page
// untitled with nothing to say why (an open sent before the workspace was entered failed that way:
// file.read refused the note's relative path, and only a toast said so).
func TestAFailedOpenSaysSoOnTheStatusLine(t *testing.T) {
	t.Parallel()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.ready(t)
	onLoop(r, func() bool { r.h.openPath("gone.md"); return true })
	status := func() string {
		return onLoop(r, func() string { v, _ := r.h.p.Tree().Source("App.status"); return v.Raw })
	}
	r.waitWithin(t, "the status line saying the open of gone.md failed", daemonWait, func() bool {
		s := status()
		return strings.HasPrefix(s, "open gone.md: ")
	})
	if s := status(); strings.Contains(s, "opening gone.md") {
		t.Fatalf("the status line still reads as an open under way: %q", s)
	}
	if f := r.file(); f.open {
		t.Fatalf("a failed open left %+v open", f)
	}
}
