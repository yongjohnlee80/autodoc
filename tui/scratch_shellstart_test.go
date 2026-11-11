package tui

import (
	"testing"
	"time"
)

// TestScratchRealShellStartTimes (SCRATCH, never merged): opens the terminal panel on a real shell
// and measures, until the shell runs, the loop's longest round trip (onLoop) meanwhile: what a
// user's key would wait for while the shell starts.
func TestScratchRealShellStartTimes(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	start := time.Now()
	r.leader(t, '`')
	var longest time.Duration
	for r.shellsRunning() == 0 {
		if time.Since(start) > 60*time.Second {
			t.Fatalf("LANDED-NEVER the shell within a minute")
		}
		at := time.Now()
		onLoop(r, func() bool { return true })
		if rt := time.Since(at); rt > longest {
			longest = rt
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Logf("LANDED shell running after %s; the loop's longest round trip meanwhile %s", time.Since(start), longest)
}
