package ingest

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

// deletesView is the sample deletes view: the track_deleted table's tombstones, past the cursor up
// to upto_ts, in (deleted_at, id) order; order is "DESC" for a misordered one.
func deletesView(order string) string {
	return `---
version: 1
name: track_deletes
source: postgres
args: [after_ts, after_id, upto_ts]
process: |
  SELECT jsonb_build_object('id', id, 'deleted_at', deleted_at)
  FROM track_deleted
  WHERE (deleted_at, id::text) > ($1, coalesce($2, ''))
    AND deleted_at <= $3
  ORDER BY deleted_at ` + order + `, id
---
`
}

// deletesFixture is newFixture with a deleted flag on tracks, the view's rows carrying it, and a
// tombstone table with its deletes view (in order, unless order says DESC).
func deletesFixture(t *testing.T, order string) (*fixture, *Source) {
	t.Helper()
	f := newFixture(t)
	f.exec(`ALTER TABLE track ADD COLUMN deleted jsonb`)
	f.exec(`CREATE TABLE track_deleted (id bigint NOT NULL, deleted_at timestamptz NOT NULL)`)
	src, err := Load(writeView(t, strings.Replace(trackView, "'lyric', t.lyric,", "'lyric', t.lyric, 'deleted', t.deleted,", 1)), []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := LoadDeletes(writeView(t, deletesView(order)))
	if err != nil {
		t.Fatal(err)
	}
	return f, src.WithDeletes(d)
}

// pass runs one pass, failing the test on its error.
func pass(t *testing.T, in *Ingester) Stats {
	t.Helper()
	st, err := in.Pass(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// A row whose deleted field is true removes its document in the pass that reads it, at its
// updated_at, and leaves quarantine whether or not a document exists; a deleted field neither true
// nor false is the row's error. The mutants, named here: the flag ignored (the document stays);
// a delete not releasing quarantine.
func TestADeletedFlagRemovesItsDocumentInOnePass(t *testing.T) {
	f, src := deletesFixture(t, "")
	f.track(1, "A", "One", 1)
	f.track(2, "A", "Two", 2)
	f.track(3, "", "Three", 3) // no label: quarantined
	in := f.ingester(src, Options{}, "")
	pass(t, in)
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2)) || !slices.Equal(f.quarantined(), []string{"3"}) {
		t.Fatalf("the first pass: %v, quarantined %v", got, f.quarantined())
	}
	f.exec(`UPDATE track SET deleted = 'true', updated_at = $1 WHERE id IN (1, 3)`, at(10))
	f.exec(`UPDATE track SET deleted = '"yes"', updated_at = $1 WHERE id = 2`, at(11))
	st := pass(t, in)
	// track 3 is read twice, by the quarantine's retry and by the pass: a no-op each time
	if got := f.paths(); !slices.Equal(got, trackPaths(2)) || st.DeletedFlag != 1 || st.DeleteNoop < 1 {
		t.Errorf("after the deletes: %v, %+v; want track 1's gone, track 3's a no-op", got, st)
	}
	if q := f.quarantined(); !slices.Equal(q, []string{"2"}) {
		t.Errorf("quarantined %v; want 3 released by its delete, 2 held for its deleted field", q)
	}
	if e := f.scalar(f.store, `SELECT error FROM rag_ingest_error WHERE source_id = '2'`); !strings.Contains(e, "deleted") {
		t.Errorf("track 2's error: %q; want it to name the deleted field", e)
	}
}

// A deletes view's tombstone removes its row's document in the pass that reads it, past its own
// cursor, in the page's batch, and takes the row out of quarantine. One older than the document's
// last write is a no-op, and so is one for an id never indexed, each counted; a row deleted and
// created again before one pass keeps its new document; a tombstone at the same time as the row's
// write deletes it (the tie). The cursor stops at upto (a tombstone past now − lag waits), and
// survives a restart, so nothing is applied twice. The mutants, named here: the deletes cursor not
// moved in the page's batch; deletes read past upto; no source_at guard.
func TestADeletesViewRemovesItsRowsDocuments(t *testing.T) {
	f, src := deletesFixture(t, "")
	for id := 1; id <= 5; id++ {
		f.track(id, "A", "song", id)
	}
	in := f.ingester(src, Options{}, "")
	pass(t, in)
	// 2 deleted outright; 3 deleted at 15 then created again at 20; 4's tombstone at 4, its write's
	// time (the tie); 99 never indexed; 5's tombstone far in the future, past upto
	f.exec(`DELETE FROM track WHERE id IN (2, 4)`)
	f.exec(`INSERT INTO track_deleted VALUES (2, $1), (3, $2), (4, $3), (99, $4), (5, $5)`, at(10), at(15), at(4), at(12), at(2_000_000))
	f.track(3, "A", "song again", 20)
	st := pass(t, in)
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 3, 5)) {
		t.Fatalf("after the tombstones: %v; want 2 and 4 gone, 3 kept (created again), 5 waiting", got)
	}
	if st.DeletedView != 2 || st.DeleteNoop != 2 {
		t.Errorf("the pass's stats %+v; want 2 deleted by the view, 2 no-ops (3's older tombstone, 99)", st)
	}
	c, err := f.st.DeletesCursor(f.ctx)
	if err != nil || !c.UpdatedAt.Equal(at(15)) || c.ID != "3" {
		t.Fatalf("the deletes cursor %+v, %v; want (at 15, 3), short of 5's tombstone past upto", c, err)
	}
	in.Release()
	again := f.ingester(src, Options{}, "")
	if st := pass(t, again); st.DeletedView+st.DeleteNoop != 0 {
		t.Errorf("a pass after a restart applied %+v again", st)
	}
}

// A deletes view whose page is out of order writes nothing (ErrOrder), and a failing deletes view
// leaves the rows' pass running: its rows are indexed, its deletes wait, and the ingester's state
// says the view's error until a deletes pass succeeds.
func TestAFailingDeletesViewHoldsOnlyTheDeletes(t *testing.T) {
	f, src := deletesFixture(t, "DESC")
	f.track(1, "A", "One", 1)
	f.track(2, "A", "Two", 2)
	in := f.ingester(src, Options{}, "")
	pass(t, in)
	f.exec(`DELETE FROM track WHERE id IN (1, 2)`)
	f.exec(`INSERT INTO track_deleted VALUES (1, $1), (2, $2)`, at(10), at(11))
	f.track(3, "A", "Three", 12)
	st := pass(t, in)
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2, 3)) || st.DeletedView != 0 {
		t.Fatalf("a misordered deletes page: %v, %+v; want the new row indexed and nothing deleted", got, st)
	}
	if c, err := f.st.DeletesCursor(f.ctx); err != nil || !c.IsZero() {
		t.Errorf("the deletes cursor after a misordered page: %+v, %v; want unmoved", c, err)
	}
	if s := in.State(); !s.Deletes || s.DeletesError != "order" {
		t.Errorf("the state %+v; want the deletes view's order error", s)
	}
	f.exec(`DROP TABLE track_deleted`)
	f.track(4, "A", "Four", 13)
	pass(t, in)
	if got := f.paths(); !slices.Contains(got, "track/4.md") {
		t.Errorf("a failing deletes view held the rows: %v", got)
	}
	if s := in.State(); s.DeletesError != "view" {
		t.Errorf("the state's deletes error %q; want view", s.DeletesError)
	}
}

// A document written before source_at was kept (NULL) is deleted by a tombstone of any time.
func TestADocumentWithoutSourceAtIsDeletedByAnyTombstone(t *testing.T) {
	f, src := deletesFixture(t, "")
	f.track(1, "A", "One", 100)
	in := f.ingester(src, Options{}, "")
	pass(t, in)
	f.exec2(`UPDATE rag_document SET source_at = NULL`)
	f.exec(`DELETE FROM track WHERE id = 1`)
	f.exec(`INSERT INTO track_deleted VALUES (1, $1)`, at(5))
	if st := pass(t, in); st.DeletedView != 1 || len(f.paths()) != 0 {
		t.Errorf("an older tombstone of a document with no source_at: %+v, %v; want it deleted", st, f.paths())
	}
}

// A deletes view is checked as a source view is: postgres, the cursor's three args, each used.
func TestADeletesViewThatCannotBePagedIsRefused(t *testing.T) {
	for name, src := range map[string]string{
		"not postgres": strings.Replace(deletesView(""), "source: postgres", "source: sqlite", 1),
		"no upto":      strings.Replace(deletesView(""), "AND deleted_at <= $3", "", 1),
	} {
		var ve *ViewError
		if _, err := LoadDeletes(writeView(t, src)); !errors.As(err, &ve) {
			t.Errorf("%s: %v; want a ViewError", name, err)
		}
	}
	if _, err := LoadDeletes(writeView(t, deletesView(""))); err != nil {
		t.Errorf("the sample deletes view: %v", err)
	}
}
