package tui

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// A PREFERENCE JUST SET, AND THE STORE'S LIST READ AGAIN (prefs.go: loadPrefs, storePref,
// prefRefused): the list may be answered before a write just sent lands, so what is still being
// written as a list starts is laid over its answer, as what is set while it is read is; a second
// list started while one is in flight shares what the first keeps; and a value the store refuses
// leaves what is shown as the store has it, the refusal said.

// prefsRig is a TUI whose session can hold the zoom's write before it is sent, hold one list's answer
// once it is read, and refuse the zoom's write; each list answered is counted.
type prefsRig struct {
	d *daemon
	r *running

	holdWrite          atomic.Bool
	writeHeld, writeGo chan struct{}
	holdList           atomic.Bool
	listHeld, listGo   chan struct{}
	refuseWrite        atomic.Bool
	listsAnswered      atomic.Int32
}

func newPrefsRig(t *testing.T) *prefsRig {
	t.Helper()
	g := &prefsRig{
		d:         startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}}),
		writeHeld: make(chan struct{}, 1), writeGo: make(chan struct{}),
		listHeld: make(chan struct{}, 1), listGo: make(chan struct{}),
	}
	sess := NewSession(g.d.sock, nil)
	sess.SetBeforeCall(func(method string, p []any) {
		if method == "preference.set" && len(p) > 0 && p[0] == prefZoom && g.holdWrite.CompareAndSwap(true, false) {
			g.writeHeld <- struct{}{}
			<-g.writeGo
		}
	})
	sess.SetCallError(func(method string) error {
		if method == "preference.set" && g.refuseWrite.Load() {
			return errors.New("the store refuses this value")
		}
		return nil
	})
	sess.SetAfterCall(func(method string) {
		if method != "preference.list" {
			return
		}
		if g.holdList.CompareAndSwap(true, false) {
			g.listHeld <- struct{}{}
			<-g.listGo
		}
		g.listsAnswered.Add(1)
	})
	g.r = runTUI(t, sess, Options{})
	t.Cleanup(func() { g.release(g.writeGo); g.release(g.listGo) }) // a failed cell never leaves a worker held
	g.r.connected(t)
	g.r.s.WaitFor(t, "the start's preferences in and written", func(string) bool {
		return onLoop(g.r, func() bool { return g.r.h.started && g.r.h.luaPrefsRead && len(g.r.h.prefWrites) == 0 })
	})
	return g
}

// release closes c once.
func (g *prefsRig) release(c chan struct{}) {
	select {
	case <-c:
	default:
		close(c)
	}
}

// wait waits for c, a hold's signal.
func (g *prefsRig) wait(t *testing.T, what string, c chan struct{}) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(daemonWait):
		t.Fatalf("%s: never reached", what)
	}
}

func (g *prefsRig) setZoom(n int) {
	onLoop(g.r, func() bool { g.r.h.setPref(prefZoom, strconv.Itoa(n), func(p *prefs) { p.zoom = n }); return true })
}

func (g *prefsRig) zoom() int { return onLoop(g.r, func() int { return g.r.h.prefs.zoom }) }

// reload starts a read of the store's list, as a card set's change or a connection does.
func (g *prefsRig) reload() { onLoop(g.r, func() bool { g.r.h.loadPrefs(); return true }) }

// stored is the zoom as the store has it, "" for none.
func (g *prefsRig) stored(t *testing.T) string {
	t.Helper()
	m, err := g.d.db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return m[prefZoom]
}

// settled waits for no list in flight, no reread due and no write waiting.
func (g *prefsRig) settled(t *testing.T, what string) {
	t.Helper()
	g.r.waitWithin(t, what, daemonWait, func() bool {
		return onLoop(g.r, func() bool {
			return g.r.h.prefsLoading == 0 && !g.r.h.prefsReread && len(g.r.h.prefWrites) == 0
		})
	})
}

// A preference set just before the store's list is read again, its write not yet sent when the list
// answers, stays as set: the list never had it, and the write still waiting is laid over the answer.
// Once the write lands, the store has it too. The mutant, named here: loadPrefs not seeding what it
// lays over from the writes waiting (the zoom goes back to the store's 0).
func TestAPreferenceSetJustBeforeTheListIsReadAgainIsKept(t *testing.T) {
	t.Parallel()
	g := newPrefsRig(t)
	g.holdWrite.Store(true)
	g.setZoom(125)
	g.wait(t, "the zoom's write held before it is sent", g.writeHeld)
	before := g.listsAnswered.Load()
	g.reload()
	g.r.waitWithin(t, "the list answered", daemonWait, func() bool {
		return g.listsAnswered.Load() > before && onLoop(g.r, func() bool { return g.r.h.prefsLoading == 0 })
	})
	if z := g.zoom(); z != 125 {
		t.Fatalf("the zoom is %d once the list answered; want 125, set as the list was read, its write still waiting", z)
	}
	g.release(g.writeGo)
	g.settled(t, "the zoom's write answered")
	if z, s := g.zoom(), g.stored(t); z != 125 || s != "125" {
		t.Fatalf("the zoom shown %d, stored %q; want 125 both", z, s)
	}
}

// Two lists in flight, the first held once read and the second answering first, both keep a write
// waiting since before the first: neither answer undoes it, whichever order they come in.
func TestTwoListsInFlightBothKeepAWriteWaitingSinceBeforeThem(t *testing.T) {
	t.Parallel()
	g := newPrefsRig(t)
	g.holdWrite.Store(true)
	g.setZoom(125)
	g.wait(t, "the zoom's write held before it is sent", g.writeHeld)
	g.holdList.Store(true)
	before := g.listsAnswered.Load()
	g.reload()
	g.wait(t, "the first list held once read", g.listHeld)
	g.reload()
	g.r.waitWithin(t, "the second list answered", daemonWait, func() bool {
		return g.listsAnswered.Load() > before && onLoop(g.r, func() bool { return g.r.h.prefsLoading == 1 })
	})
	if z := g.zoom(); z != 125 {
		t.Fatalf("the zoom is %d once the second list answered; want 125", z)
	}
	g.release(g.listGo)
	g.r.waitWithin(t, "the first list answered", daemonWait, func() bool {
		return onLoop(g.r, func() bool { return g.r.h.prefsLoading == 0 })
	})
	if z := g.zoom(); z != 125 {
		t.Fatalf("the zoom is %d once the first list answered last; want 125", z)
	}
	g.release(g.writeGo)
	g.settled(t, "the zoom's write answered")
	if z, s := g.zoom(), g.stored(t); z != 125 || s != "125" {
		t.Fatalf("the zoom shown %d, stored %q; want 125 both", z, s)
	}
}

// A preference set and stored while a first list is held once read, then a second list read and
// answered: the first list, answering last with the store as it was before the set, still leaves
// the set value, since a second list keeps what the first keeps. The mutant, named here: loadPrefs
// starting what it lays over afresh for every list (the first answer brings back the store's 0).
func TestASecondListKeepsWhatTheFirstKeeps(t *testing.T) {
	t.Parallel()
	g := newPrefsRig(t)
	g.holdList.Store(true)
	before := g.listsAnswered.Load()
	g.reload()
	g.wait(t, "the first list held once read", g.listHeld)
	g.setZoom(125)
	g.r.waitWithin(t, "the zoom stored", daemonWait, func() bool {
		return onLoop(g.r, func() bool { return len(g.r.h.prefWrites) == 0 }) && g.stored(t) == "125"
	})
	g.reload()
	g.r.waitWithin(t, "the second list answered", daemonWait, func() bool {
		return g.listsAnswered.Load() > before && onLoop(g.r, func() bool { return g.r.h.prefsLoading == 1 })
	})
	g.release(g.listGo)
	g.settled(t, "the first list answered")
	if z := g.zoom(); z != 125 {
		t.Fatalf("the zoom is %d once the first list, read before the set, answered last; want 125", z)
	}
}

// A value the store refuses, with no list in flight, is said, and what is shown goes back to the
// store's: the list is read again. The mutant, named here: a refused write leaving what is shown as
// set (125, while the store has none).
func TestARefusedPreferenceShowsTheStoresValueAndIsSaid(t *testing.T) {
	t.Parallel()
	g := newPrefsRig(t)
	g.refuseWrite.Store(true)
	g.setZoom(125)
	g.r.s.WaitForText(t, "the store refuses this value")
	g.settled(t, "the refusal followed")
	if z, s := g.zoom(), g.stored(t); z != 0 || s != "" {
		t.Fatalf("after the refusal the zoom shown is %d, stored %q; want the store's: 0, none", z, s)
	}
}

// A value the store refuses while a list is in flight, the list read before an earlier value of the
// same preference landed: once the list answers, the list is read once more, so what is shown ends
// as the store has it, the earlier value, not the list's older one nor the refused one. The mutant,
// named here: no reread after a refusal while a list is in flight (the list's older 0 stays shown,
// while the store has 110).
func TestARefusalWhileAListIsReadEndsAsTheStoreHasIt(t *testing.T) {
	t.Parallel()
	g := newPrefsRig(t)
	g.holdList.Store(true)
	g.reload()
	g.wait(t, "the list held once read", g.listHeld)
	g.setZoom(110)
	g.r.waitWithin(t, "110 stored", daemonWait, func() bool {
		return onLoop(g.r, func() bool { return len(g.r.h.prefWrites) == 0 }) && g.stored(t) == "110"
	})
	g.refuseWrite.Store(true)
	g.setZoom(125)
	g.r.s.WaitForText(t, "the store refuses this value")
	g.refuseWrite.Store(false)
	g.release(g.listGo)
	g.settled(t, "the list answered, and read once more")
	if z, s := g.zoom(), g.stored(t); z != 110 || s != "110" {
		t.Fatalf("the zoom shown %d, stored %q; want 110 both, the store's", z, s)
	}
}
