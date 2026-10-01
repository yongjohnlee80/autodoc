package tui

import (
	"sync/atomic"
	"testing"
)

// TestTheDaemonWarmingUpIsSaid: while index.status says the workspace is warming up, a toast says
// what for; when it is ready, the toast says so.
func TestTheDaemonWarmingUpIsSaid(t *testing.T) {
	var reasons atomic.Pointer[[]string]
	warm := []string{"setting up the embedding provider Gemma"}
	reasons.Store(&warm)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}},
		daemonOpts{warming: func() []string { return *reasons.Load() }})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "warming up: setting up the embedding provider Gemma")
	none := []string{}
	reasons.Store(&none)
	r.s.WaitForText(t, "ready: search and the notes are up to date")
}

// TestTheNotesAreListedOnceTheDaemonIsReady: a daemon that cannot serve the workspace yet fails the
// first listing; the TUI tries again, so the notes appear without anything changing.
func TestTheNotesAreListedOnceTheDaemonIsReady(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n", "b.md", "b\n"}},
		daemonOpts{notReady: 4})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.waitListed(t, 2)
}
