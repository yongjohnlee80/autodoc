package ingest

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search/searchtest"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// embedAll writes a vector for every chunk text of the tenant that has none, as the embedding
// worker would.
func (f *fixture) embedAll() {
	f.t.Helper()
	rows, err := f.store.QueryContext(f.ctx, `SELECT DISTINCT text_hash FROM rag_chunk k WHERE tenant = $1
		AND NOT EXISTS (SELECT 1 FROM rag_embedding e WHERE e.tenant = k.tenant AND e.text_hash = k.text_hash)`, f.tenant)
	if err != nil {
		f.t.Fatal(err)
	}
	var vecs []pgstore.Embedding
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			f.t.Fatal(err)
		}
		e := pgstore.Embedding{Vec: searchtest.Embed(string(h))}
		copy(e.TextHash[:], h)
		vecs = append(vecs, e)
	}
	rows.Close()
	if err := f.st.Embed(f.ctx, "test|embed||64", vecs); err != nil {
		f.t.Fatal(err)
	}
}

// vectors is how many vectors the tenant has.
func (f *fixture) vectors() string {
	f.t.Helper()
	return f.scalar(f.store, `SELECT count(*)::text FROM rag_embedding WHERE tenant = $1`, f.tenant)
}

// A pass that wrote sweeps the tenant's orphan vectors at most once every sweepEvery, and a
// complete refresh sweeps always; each sweep's count reaches the ingester's state. A text one row
// dropped and another gained in the same pass keeps its vector. The mutants, named here: no sweep
// after a refresh; the interval ignored (a pass within it sweeps).
func TestAnIngesterSweepsAfterARefreshAndEveryFifteenMinutesOfWrites(t *testing.T) {
	f := newFixture(t)
	var now atomic.Int64 // seconds past t0
	clock := func() time.Time { return t0.Add(time.Duration(now.Load()) * time.Second) }
	f.track(1, "A", "One", 1)
	f.track(2, "A", "Two", 2)
	in := f.ingester(f.source(nil), Options{Clock: clock}, "")
	pass(t, in) // the first pass sweeps (none yet), finding nothing
	f.embedAll()
	if got := f.vectors(); got != "2" {
		t.Fatalf("%s vectors after embedding two tracks; want 2", got)
	}

	// track 1 is rewritten, and track 2 takes track 1's old text in the same pass: one orphan
	// (track 2's old text), and track 1's old text kept, track 2 having it now
	now.Store(60)
	f.exec(`UPDATE track SET title = 'Three', lyric = 'words of Three', updated_at = $1 WHERE id = 1`, at(10))
	f.exec(`UPDATE track SET title = 'One', lyric = 'words of One', updated_at = $1 WHERE id = 2`, at(11))
	if st := pass(t, in); st.Swept != 0 {
		t.Errorf("a pass within %v of the last sweep swept %d", sweepEvery, st.Swept)
	}
	f.embedAll()
	if got := f.vectors(); got != "3" {
		t.Fatalf("%s vectors; want 3: One's (now track 2's), Two's (an orphan), Three's", got)
	}

	now.Store(int64(sweepEvery/time.Second) + 1)
	f.track(3, "A", "Four", 20)
	if st := pass(t, in); st.Swept != 1 {
		t.Errorf("a pass that wrote, past the interval, swept %d; want Two's orphan", st.Swept)
	}
	f.embedAll()
	if got := f.vectors(); got != "3" {
		t.Errorf("%s vectors after the sweep; want 3: the text track 2 gained in the pass track 1 dropped it is kept", got)
	}

	// a refresh sweeps at once, however recent the last sweep: track 3 rewritten in place leaves
	// its old text's vector, which the pass (within the interval) keeps and the refresh takes
	f.exec(`UPDATE track SET title = 'Five', lyric = 'words of Five', updated_at = $1 WHERE id = 3`, at(30))
	if st := pass(t, in); st.Swept != 0 {
		t.Errorf("a pass within the interval swept %d", st.Swept)
	}
	st, err := in.Refresh(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Swept != 1 {
		t.Errorf("the refresh swept %d; want Four's orphan", st.Swept)
	}
	if got := in.State().VectorsSwept; got != 2 {
		t.Errorf("the state's vectors_swept %d; want 2", got)
	}
}
