package embedder_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
)

// An edit behind the worker's cursor is embedded ahead of the backlog: a document embedded in the
// first round, then edited while hundreds of documents still wait, is ready again while most of
// them still wait, not after the scan has reached the end and started again. The mutant, named
// here: no read of the edited documents (the edit waits for the whole backlog).
func TestAnEditBehindTheCursorIsEmbeddedAheadOfTheBacklog(t *testing.T) {
	st, paths, _ := paceStore(400, 1)
	p := newProvider("m")
	p.delay = 5 * time.Millisecond
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(4), embedder.WithSlots(1))
	stop := run(t, w)
	defer stop()
	first := paths[0]
	waitFor(t, "the first document ready", func() bool { return st.isReady(first) })
	st.put(first, "the first document, edited")
	waiting := func() int {
		n := 0
		for _, p := range paths[1:] {
			if !st.isReady(p) {
				n++
			}
		}
		return n
	}
	before := waiting()
	waitFor(t, "the edit embedded", func() bool { return st.isReady(first) })
	after := waiting()
	if after < before/2 {
		t.Errorf("the edit was embedded with %d of %d documents still waiting (%d when it was made): want it ahead of the backlog", after, len(paths)-1, before)
	}
	t.Logf("%s", fmt.Sprintf("edited with %d waiting; embedded with %d waiting", before, after))
	waitFor(t, "every document ready", allReady(st, paths))
}

// Edited documents whose texts the provider rejects stay edited and unready, and the worker sets
// their texts aside. The read of the edits looks through a bounded number of edited documents a
// round, from a cursor of its own, so those documents cost each round a bounded read however many
// there are, and an edit behind them is still embedded ahead of the backlog. The mutants, named
// here: the read unbounded (each round looks through every set-aside document), and the read
// from the start every round (it never gets past the first set-aside documents, and the edit
// waits for the scan).
func TestSetAsideEditsAreLookedThroughABoundedNumberARound(t *testing.T) {
	const bound, refused = 4, 20
	defer embedder.SetEditsDocs(bound)()
	st, paths, _ := paceStore(400, 1)
	p := newProvider("m")
	p.delay, p.always = 5*time.Millisecond, true
	// the documents edited with a text the provider rejects come first in id order, then the edit
	for i := 1; i <= refused; i++ {
		text := fmt.Sprintf("document %d, a text the provider rejects", i)
		p.reject[text] = true
		st.put(paths[i], text)
	}
	edit := paths[300]
	st.put(edit, "document 300, edited")
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(4), embedder.WithSlots(1))
	stop := run(t, w)
	defer stop()
	waiting := func() int {
		n := 0
		for _, p := range paths[refused+1:] {
			if !st.isReady(p) {
				n++
			}
		}
		return n
	}
	before := waiting()
	waitFor(t, "the edit embedded", func() bool { return st.isReady(edit) })
	after := waiting()
	if after < before/2 {
		t.Errorf("the edit behind %d set-aside documents was embedded with %d of %d documents still waiting: want it ahead of the backlog", refused, after, before)
	}
	st.mu.Lock()
	looked := slices.Clone(st.looked)
	st.mu.Unlock()
	if len(looked) == 0 {
		t.Fatal("no read of the edits")
	}
	if most := slices.Max(looked); most > bound {
		t.Errorf("a read of the edits looked through %d edited documents: want at most %d a round", most, bound)
	}
	t.Logf("embedded with %d of %d waiting; %d reads of the edits, each through at most %d documents", after, before, len(looked), slices.Max(looked))
}
