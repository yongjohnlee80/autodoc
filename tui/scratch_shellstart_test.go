package tui

import (
	"testing"
	"time"
)

// TestScratchRealShellStartTimes (SCRATCH, never merged): opens ten real shells, one at a time, and
// measures how long the UI loop itself spends in each open (newShell, on the loop): a start on the
// loop waits there for the spawn, one off it doesn't. Then how long until each runs.
func TestScratchRealShellStartTimes(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	for i := 1; i <= 10; i++ {
		var held time.Duration
		start := time.Now()
		onLoop(r, func() bool {
			at := time.Now()
			r.h.terminals.newShell()
			held = time.Since(at)
			return true
		})
		for r.shellsRunning() < i {
			if time.Since(start) > 60*time.Second {
				t.Fatalf("LANDED-NEVER shell %d within a minute", i)
			}
			time.Sleep(2 * time.Millisecond)
		}
		t.Logf("LANDED shell %d: the loop held %s in its open; running after %s", i, held, time.Since(start))
	}
}
