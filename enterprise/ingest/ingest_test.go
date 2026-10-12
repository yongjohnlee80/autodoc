package ingest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// Rows render, chunk and are written: a pass indexes every row as its document, with its title,
// its access facet and its body's words, and a word of a row finds it.
func TestAPassIndexesEveryRow(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "Blue", 1)
	f.track(2, "B", "Green", 2)
	in := f.ingester(f.source(nil), Options{}, "")
	st := must[Stats](t)(in.Pass(f.ctx))
	if st.Written != 2 || st.Quarantined != 0 {
		t.Fatalf("Pass: %+v", st)
	}
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2)) {
		t.Fatalf("documents %v", got)
	}
	if got := f.lexical("green"); !slices.Equal(got, []string{"track/2.md"}) {
		t.Errorf("a search for green finds %v", got)
	}
	d := f.docs()["track/1.md"]
	if !strings.HasPrefix(d.Version, "2026-01-01T00:00:01Z|") || d.Indexer != in.src.Indexer() {
		t.Errorf("track 1: version %q, indexer %q", d.Version, d.Indexer)
	}
	cur := must[pgstore.Cursor](t)(f.st.Cursor(f.ctx))
	if cur.ID != "2" || !cur.UpdatedAt.Equal(at(2)) {
		t.Errorf("the cursor is %+v, want track 2's key", cur)
	}
	// a second pass reads nothing: the cursor is past every row
	if st := must[Stats](t)(in.Pass(f.ctx)); st.Read != 0 {
		t.Errorf("a second pass read %d rows", st.Read)
	}
}

// More than a page of rows sharing one updated_at are all indexed: the cursor is the last row's
// (updated_at, id), so the next page starts within the timestamp. The mutant, named here: the
// cursor's id never bound (after_id NULL), so the next page starts after the timestamp and skips
// the rest of the tie.
func TestTiedTimestampsAcrossPagesAreAllIndexed(t *testing.T) {
	f := newFixture(t)
	for id := 1; id <= 8; id++ { // the view's pages are 3 rows
		f.track(id, "A", "tied", 5)
	}
	in := f.ingester(f.source(nil), Options{}, "")
	st := must[Stats](t)(in.Pass(f.ctx))
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2, 3, 4, 5, 6, 7, 8)) {
		t.Fatalf("documents %v, want all 8 (%+v)", got, st)
	}
	if st.Pages != 3 {
		t.Errorf("%d pages, want 3", st.Pages)
	}
}

// A crash between a page's writes and its commit replays the page; a crash after a commit does not
// write it again: nothing is lost, and nothing is written twice. The mutants, named here: the
// cursor committed in a transaction of its own before the page's (the crash loses the page's rows),
// and the cursor never moved (the restart writes the first page again, bumping its generations).
func TestACrashReplaysAtMostThePageItInterrupted(t *testing.T) {
	f := newFixture(t)
	for id := 1; id <= 7; id++ {
		f.track(id, "A", "song", id)
	}
	src := f.source(nil)
	in := f.ingester(src, Options{}, "")
	commits := 0
	crash := errors.New("crashed")
	in.beforeCommit = func() error {
		if commits++; commits == 2 {
			return crash // the second page dies before its commit
		}
		return nil
	}
	if _, err := in.Pass(f.ctx); !errors.Is(err, crash) {
		t.Fatalf("Pass: %v, want the crash", err)
	}
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2, 3)) {
		t.Fatalf("after the crash: %v, want the first page alone", got)
	}
	in.Release()

	restarted := f.ingester(src, Options{}, "")
	if _, err := restarted.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	docs := f.docs()
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2, 3, 4, 5, 6, 7)) {
		t.Fatalf("after the restart: %v, want every row", got)
	}
	for p, d := range docs {
		if d.Generation != 1 {
			t.Errorf("%s was written %d times, want once", p, d.Generation)
		}
	}
}

// A row a business transaction commits late, with an updated_at behind a row already committed, is
// still read when it is within the lag: a pass reads only up to now − lag, so the cursor never
// passes a timestamp a transaction still open could commit. The mutant, named here: upto taken as
// now, with no lag: the first pass moves the cursor past the late row's updated_at for good.
func TestALateRowWithinTheLagIsRead(t *testing.T) {
	f := newFixture(t)
	now := at(100)
	src := f.source(nil)
	in := f.ingester(src, Options{Lag: time.Minute, Now: func(context.Context) (time.Time, error) { return now, nil }}, "")
	f.track(2, "A", "early", 90) // committed first, 10 s before now
	if st := must[Stats](t)(in.Pass(f.ctx)); st.Read != 0 {
		t.Fatalf("a row within the lag was read: %+v", st)
	}
	f.track(1, "A", "late", 80) // its transaction began first and commits now, behind track 2
	now = at(200)
	if _, err := in.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2)) {
		t.Fatalf("documents %v, want the late row too", got)
	}
}

// A bad row before good ones: the good rows index and the cursor passes the bad one, which is
// quarantined with its reason; every pass retries it; once fixed (with no new updated_at, so only
// the retry can find it) it is indexed and leaves quarantine. A quarantined row the source no longer
// returns leaves quarantine too. The mutants, named here: a pass that does not retry the quarantine
// (the fixed row is never indexed), and a retry that releases nothing it did not see (the gone row
// stays quarantined).
func TestABadRowIsQuarantinedAndRetriedUntilItIngests(t *testing.T) {
	f := newFixture(t)
	f.track(1, "", "no label", 1) // a NULL access field
	f.track(2, "A", "good", 2)
	f.track(3, "A", "good too", 3)
	f.track(4, "", "doomed", 4)
	in := f.ingester(f.source(nil), Options{}, "")
	st := must[Stats](t)(in.Pass(f.ctx))
	if got := f.paths(); !slices.Equal(got, trackPaths(2, 3)) {
		t.Fatalf("documents %v, want the good rows", got)
	}
	if got := f.quarantined(); !slices.Equal(got, []string{"1", "4"}) || st.Quarantined != 2 || st.InQuarantine != 2 {
		t.Fatalf("quarantine %v (%+v), want 1 and 4", got, st)
	}
	q := must[[]pgstore.IngestError](t)(f.st.Quarantined(f.ctx, 10))
	if !strings.Contains(q[0].Err, "label_id is missing") {
		t.Errorf("the quarantine's reason: %q", q[0].Err)
	}
	if cur := must[pgstore.Cursor](t)(f.st.Cursor(f.ctx)); cur.ID != "4" {
		t.Errorf("the cursor %+v, want it past the bad rows", cur)
	}

	// still bad: retried and kept
	if st := must[Stats](t)(in.Pass(f.ctx)); st.Quarantined != 2 || st.Read != 0 {
		t.Errorf("a pass with nothing new: %+v, want both retried and failing again", st)
	}
	// fixed in place, without a new updated_at; and track 4 deleted from the source
	f.exec(`UPDATE track SET label_id = 'A' WHERE id = 1`)
	f.exec(`DELETE FROM track WHERE id = 4`)
	st = must[Stats](t)(in.Pass(f.ctx))
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2, 3)) {
		t.Fatalf("documents %v, want the fixed row indexed", got)
	}
	if got := f.quarantined(); len(got) != 0 || st.Released != 2 {
		t.Errorf("quarantine %v (%+v), want it empty: one fixed, one gone", got, st)
	}
}

// A refresh never deletes the document of a row it read: a row indexed before, whose body now
// fails, keeps its document and goes to quarantine. A row deleted from the source loses its
// document after a refresh that read every row. The mutants, named here: a path counted as seen
// only when its row renders (the failing row's document is deleted), and no deletion at all (the
// gone row's document stays).
func TestARefreshKeepsWhatItReadAndDeletesWhatIsGone(t *testing.T) {
	f := newFixture(t)
	for id := 1; id <= 5; id++ {
		f.track(id, "A", "song", id)
	}
	in := f.ingester(f.source(nil), Options{}, "")
	if _, err := in.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE track SET label_id = NULL WHERE id = 2`) // its document can no longer be made
	f.exec(`DELETE FROM track WHERE id = 4`)
	st := must[Stats](t)(in.Refresh(f.ctx))
	if !st.Complete || st.Deleted != 1 || st.Quarantined != 1 || st.Unchanged != 3 || st.Written != 0 {
		t.Errorf("Refresh: %+v", st)
	}
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2, 3, 5)) {
		t.Errorf("documents %v, want 2 kept and 4 gone", got)
	}
	if got := f.quarantined(); !slices.Equal(got, []string{"2"}) {
		t.Errorf("quarantine %v", got)
	}
	r := must[pgstore.Refresh](t)(f.st.Refreshed(f.ctx))
	if r.Started.IsZero() || r.Finished.Before(r.Started) {
		t.Errorf("the refresh recorded %+v", r)
	}
}

// A scan that fails after some pages deletes nothing: the rows it did not reach would look gone.
// The mutant, named here: the stale paths deleted after a scan that stopped on an error.
func TestARefreshThatFailsDeletesNothing(t *testing.T) {
	f := newFixture(t)
	for id := 1; id <= 7; id++ {
		f.track(id, "A", "song", id)
	}
	src := f.source(nil)
	in := f.ingester(src, Options{}, "")
	if _, err := in.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.exec(`DELETE FROM track WHERE id = 1`)
	broken := &failingConn{DataConn: f.business}
	broken.n.Store(2) // the second page fails
	in.business = broken
	st, err := in.Refresh(f.ctx)
	if err == nil || st.Complete {
		t.Fatalf("Refresh: %+v, %v; want the source's failure", st, err)
	}
	if got := f.paths(); !slices.Equal(got, trackPaths(1, 2, 3, 4, 5, 6, 7)) {
		t.Errorf("documents %v, want every one kept", got)
	}
	if r := must[pgstore.Refresh](t)(f.st.Refreshed(f.ctx)); !r.Finished.IsZero() {
		t.Errorf("a failed refresh was recorded: %+v", r)
	}
	in.business = f.business
	if st := must[Stats](t)(in.Refresh(f.ctx)); !st.Complete || st.Deleted != 1 {
		t.Errorf("the next refresh: %+v, want track 1's document deleted", st)
	}
}

// A field of a joined table that changed without moving the row's updated_at (a view whose
// updated_at forgot that table) is not seen by a pass, but a refresh writes the document again,
// because its content's hash changed. The mutant, named here: a Version of updated_at alone.
func TestARefreshRewritesAContentChangeTheTimestampMissed(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "Blue", 1)
	f.track(2, "A", "Green", 2)
	forgot := func(v string) string { // updated_at is the track's alone
		return strings.ReplaceAll(v, "GREATEST(t.updated_at, a.updated_at)", "t.updated_at")
	}
	in := f.ingester(f.source(forgot), Options{}, "")
	if _, err := in.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	f.exec(`UPDATE artist SET name = 'Zelda' WHERE id = 1`)
	if st := must[Stats](t)(in.Pass(f.ctx)); st.Read != 0 {
		t.Fatalf("a pass read %d rows: the change has no new updated_at", st.Read)
	}
	st := must[Stats](t)(in.Refresh(f.ctx))
	if st.Written != 2 || st.Unchanged != 0 {
		t.Errorf("Refresh: %+v, want both rows written again", st)
	}
	if got := f.lexical("zelda"); len(got) != 2 {
		t.Errorf("a search for the new name finds %v", got)
	}
	// and with nothing changed, a refresh writes nothing
	if st := must[Stats](t)(in.Refresh(f.ctx)); st.Written != 0 || st.Unchanged != 2 {
		t.Errorf("a refresh with nothing changed: %+v", st)
	}
}

// A view edit changes the indexer, so a refresh cuts every row again, though no row changed.
func TestAViewEditRecutsEveryRow(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "Blue", 1)
	in := f.ingester(f.source(nil), Options{}, "")
	if _, err := in.Pass(f.ctx); err != nil {
		t.Fatal(err)
	}
	in.Release()
	edited := f.ingester(f.source(func(v string) string { return strings.Replace(v, "by {{.artist}}", "sung by {{.artist}}", 1) }), Options{}, "")
	if st := must[Stats](t)(edited.Refresh(f.ctx)); st.Written != 1 {
		t.Errorf("Refresh after an edit: %+v", st)
	}
	if got := f.docs()["track/1.md"].Indexer; got != edited.src.Indexer() {
		t.Errorf("indexer %q, want %q", got, edited.src.Indexer())
	}
}

// A refresh on a tenant with no cursor moves it to the end of its scan, so the pass after it does
// not read every row again; a refresh never moves the cursor back.
func TestARefreshMovesABehindCursorForward(t *testing.T) {
	f := newFixture(t)
	for id := 1; id <= 4; id++ {
		f.track(id, "A", "song", id)
	}
	in := f.ingester(f.source(nil), Options{}, "")
	if _, err := in.Refresh(f.ctx); err != nil {
		t.Fatal(err)
	}
	if cur := must[pgstore.Cursor](t)(f.st.Cursor(f.ctx)); cur.ID != "4" {
		t.Errorf("the cursor after a first refresh: %+v", cur)
	}
	if st := must[Stats](t)(in.Pass(f.ctx)); st.Read != 0 {
		t.Errorf("the pass after it read %d rows", st.Read)
	}
}

// A view whose rows break the order within a page is refused before anything of the page is
// written: past upto_ts, or going back in time. A page with no keyed row stops the pass rather
// than spin on it.
func TestAMisorderedPageWritesNothing(t *testing.T) {
	f := newFixture(t)
	f.track(1, "A", "one", 5)
	f.track(2, "A", "two", 1)
	byID := f.source(func(v string) string {
		return strings.Replace(v, "ORDER BY GREATEST(t.updated_at, a.updated_at), t.id", "ORDER BY t.id", 1)
	})
	in := f.ingester(byID, Options{}, "")
	if _, err := in.Pass(f.ctx); !errors.Is(err, ErrOrder) {
		t.Errorf("a page ordered by id: %v, want ErrOrder", err)
	}
	in.Release()
	noUpto := f.ingester(f.source(func(v string) string {
		return strings.Replace(v, "AND GREATEST(t.updated_at, a.updated_at) <= $3", "AND $3::timestamptz IS NOT NULL", 1)
	}), Options{Lag: time.Hour, Now: func(context.Context) (time.Time, error) { return at(3), nil }}, "")
	if _, err := noUpto.Pass(f.ctx); !errors.Is(err, ErrOrder) {
		t.Errorf("a view ignoring upto_ts: %v, want ErrOrder", err)
	}
	noUpto.Release()
	keyless := f.ingester(f.source(func(v string) string {
		return strings.Replace(v, "'updated_at', GREATEST(t.updated_at, a.updated_at))", "'stamp', GREATEST(t.updated_at, a.updated_at))", 1)
	}), Options{}, "")
	if _, err := keyless.Pass(f.ctx); !errors.Is(err, ErrStalled) {
		t.Errorf("rows with no updated_at: %v, want ErrStalled", err)
	}
	if len(f.paths()) != 0 {
		t.Errorf("documents %v, want none", f.paths())
	}
	if got := f.quarantined(); !slices.Equal(got, []string{"1", "2"}) {
		t.Errorf("quarantine %v, want the keyless rows", got)
	}
}

// Writing needs the lock: an ingester that has not acquired it, or has released it, refuses.
func TestWritingNeedsTheLock(t *testing.T) {
	f := newFixture(t)
	in := New(f.st, f.tenant, f.source(nil), f.business, f.open("locks", ""), Options{Schema: f.schema})
	if _, err := in.Pass(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Errorf("Pass unlocked: %v", err)
	}
	if _, err := in.Refresh(f.ctx); !errors.Is(err, ErrNotLocked) {
		t.Errorf("Refresh unlocked: %v", err)
	}
	if in.Tenant() != f.tenant {
		t.Errorf("Tenant %q", in.Tenant())
	}
}
