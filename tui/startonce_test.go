package tui

import (
	"errors"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// startCounter is a feature that counts the program's starts (a Starter).
type startCounter struct{ n int }

func (*startCounter) Name() string                         { return "startcounter" }
func (*startCounter) State() map[string]any                { return nil }
func (*startCounter) Commands() map[string]feature.Command { return nil }
func (c *startCounter) Started()                           { c.n++ }

// TestStartingWaitsForTheStoresPreferences: a preference set while the store's preferences are
// still being read (preference.list's answer held, read but not yet handed back) applies at once but starts nothing; the program
// starts once the list answers, on the store's preferences: About stays shut, as the store says
// (tui.about.atstart false), and the preference set meanwhile still wins over the list's older
// value. It starts once a run, a later read of the list (a card added) starting nothing again (the
// mutants: started from applyPrefs again, the preference set meanwhile dropped, the once lost).
func TestStartingWaitsForTheStoresPreferences(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	sess := NewSession(d.sock, nil)
	held, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	// held once its answer is read: the store's preferences as they were, which the preference set
	// below is written after, so only the setting kept meanwhile (prefsSetMeanwhile) keeps it
	sess.SetAfterCall(func(method string) {
		if method == "preference.list" {
			once.Do(func() { held <- struct{}{}; <-release })
		}
	})
	r := runTUI(t, sess, Options{})
	starts := &startCounter{}
	onLoop(r, func() bool { r.h.reg.Features = append(r.h.reg.Features, starts); return true })
	select {
	case <-held:
	case <-time.After(daemonWait):
		close(release)
		t.Fatal("the store's preferences were never asked for")
	}
	onLoop(r, func() bool { r.h.setPref(prefZoom, "125", func(p *prefs) { p.zoom = 125 }); return true })
	if started, n, zoom := onLoop(r, func() bool { return r.h.started }), onLoop(r, func() int { return starts.n }),
		onLoop(r, func() int { return r.h.prefs.zoom }); started || n != 0 || zoom != 125 {
		close(release)
		t.Fatalf("a preference set before the store's answered: started %v (%d starts), zoom %d; want it applied (125), and nothing started", started, n, zoom)
	}
	close(release)
	r.prefsIn(t)
	r.s.WaitFor(t, "the program started", func(string) bool { return onLoop(r, func() bool { return r.h.started }) })
	if n := onLoop(r, func() int { return starts.n }); n != 1 {
		t.Fatalf("%d starts once the store's preferences are in; want 1", n)
	}
	if strings.Contains(strings.ToLower(r.s.String()), "about autodoc") {
		t.Fatalf("About opened, though the store says not at the start:\n%s", r.s.String())
	}
	if zoom := onLoop(r, func() int { return r.h.prefs.zoom }); zoom != 125 {
		t.Fatalf("the zoom set while the list was read is %d once it answered; want 125, the set one", zoom)
	}

	// the list read again (a card added does): no second start
	onLoop(r, func() bool { r.h.luaPrefsRead = false; r.h.loadPrefs(); return true })
	r.prefsIn(t)
	if n := onLoop(r, func() int { return starts.n }); n != 1 {
		t.Fatalf("%d starts after the store's preferences were read again; want 1", n)
	}
}

// TestAStoreThatCantListStartsOnTheDefaults: when preference.list fails, the run starts all the
// same, once, on the defaults: About opens, as its default says, and the error is said. A later
// list that answers applies the store's preferences as a change (About's start off again, as the
// store says), and starts nothing again (the mutants: no start on the failed list; the once lost).
func TestAStoreThatCantListStartsOnTheDefaults(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	sess := NewSession(d.sock, nil)
	held, release := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	var fail atomic.Bool
	fail.Store(true)
	sess.SetBeforeCall(func(method string, _ []any) {
		if method == "preference.list" {
			once.Do(func() { held <- struct{}{}; <-release }) // the counter joins before the start
		}
	})
	sess.SetCallError(func(method string) error {
		if method == "preference.list" && fail.Load() {
			return errors.New("the store is unreadable")
		}
		return nil
	})
	r := runTUI(t, sess, Options{})
	starts := &startCounter{}
	onLoop(r, func() bool { r.h.reg.Features = append(r.h.reg.Features, starts); return true })
	select {
	case <-held:
	case <-time.After(daemonWait):
		close(release)
		t.Fatal("the store's preferences were never asked for")
	}
	close(release)
	r.waitWithin(t, "the program started on the defaults", daemonWait, func() bool { return onLoop(r, func() bool { return r.h.started }) })
	r.s.WaitForText(t, "the store is unreadable")
	r.s.WaitForText(t, "about autodoc")
	if n := onLoop(r, func() int { return starts.n }); n != 1 {
		t.Fatalf("%d starts on a failed list; want 1", n)
	}

	// the store answers now: its preferences apply, and nothing starts again
	fail.Store(false)
	onLoop(r, func() bool { r.h.luaPrefsRead = false; r.h.loadPrefs(); return true })
	r.prefsIn(t)
	if n, about := onLoop(r, func() int { return starts.n }), onLoop(r, func() bool { return r.h.prefs.aboutAtStart }); n != 1 || about {
		t.Fatalf("the list answering later: %d starts, About at the start %v; want 1, and the store's false", n, about)
	}
}
