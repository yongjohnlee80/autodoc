package tui

// SCRATCH (never merged): when each awaited thing lands, under a whole-./tui run on Windows.

import (
	"strings"
	"testing"
	"time"
)

// lands polls cond for up to 90 s and logs when it held: past 3 s is slow, never is a hang.
func (r *running) lands(t *testing.T, what string, cond func() bool) {
	t.Helper()
	start := time.Now()
	for !cond() {
		if time.Since(start) > 90*time.Second {
			t.Fatalf("LANDING %s: NEVER within 90s:\n%s", what, r.s.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("LANDING %s: after %s", what, time.Since(start).Round(time.Millisecond))
}

func (r *running) landsText(t *testing.T, what, text string) {
	t.Helper()
	r.lands(t, what, func() bool { return strings.Contains(r.s.String(), text) })
}

// sampleLoop posts a no-op to r's loop every 50 ms and logs, when stopped, the longest the loop
// took to run one, and when.
func (r *running) sampleLoop(t *testing.T, what string) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	begin := time.Now()
	go func() {
		defer close(done)
		var worst time.Duration
		var at time.Duration
		for {
			select {
			case <-stop:
				t.Logf("LANDING %s: the loop's longest hold %s, at +%s", what, worst.Round(time.Millisecond), at.Round(time.Millisecond))
				return
			case <-time.After(50 * time.Millisecond):
			}
			t0 := time.Now()
			ran := make(chan struct{})
			r.h.p.Post(func() { close(ran) })
			select {
			case <-ran:
			case <-time.After(90 * time.Second):
				t.Logf("LANDING %s: the loop held over 90s", what)
				return
			}
			if d := time.Since(t0); d > worst {
				worst, at = d, t0.Sub(begin)
			}
		}
	}()
	return func() { close(stop); <-done }
}
