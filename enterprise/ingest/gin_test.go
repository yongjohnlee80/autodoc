package ingest

import (
	"strconv"
	"testing"
)

// pending is the text index's GIN pending list, in pages.
func (f *fixture) pending() int {
	f.t.Helper()
	f.exec2(`CREATE EXTENSION IF NOT EXISTS pgstattuple`)
	n, err := strconv.Atoi(f.scalar(f.store, `SELECT pending_pages::text FROM pgstatginindex('rag_chunk_tsv'::regclass)`))
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// exec2 runs q on the store's pool.
func (f *fixture) exec2(q string) {
	f.t.Helper()
	if _, err := f.store.ExecContext(f.ctx, q); err != nil {
		f.t.Fatalf("%s: %v", q, err)
	}
}

// A load merges the text index's pending list: a pass that wrote mergeAfter documents or more, and
// a refresh that wrote any. A pass that wrote fewer leaves the list to the next load or VACUUM.
// The mutants, named here: a pass that never merges; a refresh that never merges.
func TestALoadMergesTheTextIndexsPendingList(t *testing.T) {
	f := newFixture(t)
	was := mergeAfter
	mergeAfter = 3
	t.Cleanup(func() { mergeAfter = was })
	for i := 1; i <= 5; i++ {
		f.track(i, "A", "Track "+strconv.Itoa(i), i)
	}
	in := f.ingester(f.source(nil), Options{}, "")
	if st, err := in.Pass(f.ctx); err != nil || st.Written != 5 {
		t.Fatalf("the first pass: %+v, %v", st, err)
	}
	if n := f.pending(); n != 0 {
		t.Errorf("after a pass of 5 documents: %d pending pages; want them merged", n)
	}
	f.track(6, "A", "Track 6", 6)
	if st, err := in.Pass(f.ctx); err != nil || st.Written != 1 {
		t.Fatalf("a pass of one: %+v, %v", st, err)
	}
	if n := f.pending(); n == 0 {
		t.Fatal("after a pass of one document: no pending page; it is left for a load, and the cell needs one")
	}
	f.track(2, "A", "Track 2 again", 7)
	if st, err := in.Refresh(f.ctx); err != nil || !st.Complete || st.Written != 1 {
		t.Fatalf("the refresh: %+v, %v", st, err)
	}
	if n := f.pending(); n != 0 {
		t.Errorf("after a refresh that wrote: %d pending pages; want them merged", n)
	}
}
