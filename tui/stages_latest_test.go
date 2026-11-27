package tui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// stageRankerRig is a TUI whose next ranker.list answer can be held once the daemon has answered
// it, and which counts the searches it sends.
type stageRankerRig struct {
	r                    *running
	holdAnswer           atomic.Bool
	answerHeld, answerGo chan struct{}
	searches             atomic.Int64
}

func newStageRankerRig(t *testing.T) *stageRankerRig {
	t.Helper()
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nkestrel\n")})
	g := &stageRankerRig{answerHeld: make(chan struct{}, 1), answerGo: make(chan struct{})}
	sess := NewSession(d.sock, nil)
	sess.SetBeforeCall(func(method string, _ []any) {
		if method == "search.query" {
			g.searches.Add(1)
		}
	})
	sess.SetAfterCall(func(method string) {
		if method == "ranker.list" && g.holdAnswer.CompareAndSwap(true, false) {
			g.answerHeld <- struct{}{}
			<-g.answerGo
		}
	})
	g.r = runTUI(t, sess, Options{})
	t.Cleanup(func() { close(g.answerGo) }) // a failed cell never leaves a worker held
	g.r.ready(t)
	g.settled(t, "the start's ranker reads answered", 0)
	return g
}

func (g *stageRankerRig) loading() int {
	return onLoop(g.r, func() int { return g.r.h.stageRankerLoading })
}

// settled waits for n ranker reads left in flight.
func (g *stageRankerRig) settled(t *testing.T, what string, n int) {
	t.Helper()
	g.r.waitWithin(t, what, daemonWait, func() bool { return g.loading() == n })
}

// held arms the hold and waits for the next ranker.list, started by start, to be held.
func (g *stageRankerRig) held(t *testing.T, start func()) {
	t.Helper()
	g.holdAnswer.Store(true)
	g.r.h.p.Post(start)
	select {
	case <-g.answerHeld:
	case <-time.After(daemonWait):
		t.Fatal("the ranker read: never answered")
	}
}

// useTEI adds a TEI ranker and puts it in use, as another dialog would.
func (g *stageRankerRig) useTEI(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	if _, err := g.r.h.session.Call(ctx, "ranker.add", map[string]any{"name": "tei", "kind": "tei", "base_url": fakeTEI(t).URL}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.r.h.session.Call(ctx, "ranker.use", "tei"); err != nil {
		t.Fatal(err)
	}
}

// A ranker read as the search opens (no ranker yet), answering after the read the ranker's change
// started, is dropped: the Rerank box names the ranker in use. The mutant, named here:
// loadStageRanker's ticket with no sequence (the box names no ranker, and goes).
func TestARankerReadBeforeAChangeLeavesTheRerankBoxNamingIt(t *testing.T) {
	t.Parallel()
	g := newStageRankerRig(t)
	g.held(t, g.r.h.openSearch) // read with no ranker in use
	g.useTEI(t)
	g.r.h.p.Post(func() { core{g.r.h}.SearchModelsChanged(true) })
	g.r.waitWithin(t, "the change's read shown", daemonWait, func() bool {
		return onLoop(g.r, func() bool { return g.r.h.stageRankerID == "tei" }) && g.loading() == 1
	})
	g.answerGo <- struct{}{} // the search's read, of no ranker, answers last
	g.settled(t, "the search's read answered", 0)
	got := onLoop(g.r, func() [2]string { return [2]string{g.r.h.stageRankerID, g.r.h.stageRanker} })
	if got[0] != "tei" || got[1] == "" {
		t.Fatalf("the Rerank box's ranker %q (%q) once the earlier read answered; want tei", got[0], got[1])
	}
}

// A ranker change's read, which searches again, superseded by a later read that does not: the
// search runs again all the same, once the later read answers. The mutant, named here: the owed
// search again replaced by the later read's (the search shows the old ranker's order).
func TestASearchAgainOwedByADroppedRankerReadIsDone(t *testing.T) {
	t.Parallel()
	g := newStageRankerRig(t)
	g.r.h.p.Post(func() { g.r.h.openSearch(); g.r.h.searchLive("kestrel") })
	g.r.waitWithin(t, "the search sent", daemonWait, func() bool { return g.searches.Load() > 0 })
	g.settled(t, "the search's ranker read answered", 0)
	g.useTEI(t)
	g.held(t, func() { core{g.r.h}.SearchModelsChanged(true) }) // owes a search again, held
	before := g.searches.Load()
	g.r.h.p.Post(func() { g.r.h.loadStageRanker(false) }) // a later read, owing none
	g.r.waitWithin(t, "the search sent again once the later read answered", daemonWait, func() bool {
		return g.searches.Load() > before
	})
	g.answerGo <- struct{}{}
	g.settled(t, "the change's read answered", 0)
}
