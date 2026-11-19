package embedder_test

import (
	"fmt"
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
