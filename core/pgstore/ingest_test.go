package pgstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
)

// docPaths is the tenant's document paths, in order.
func docPaths(t *testing.T, s *Store) string {
	t.Helper()
	docs, _, err := s.Docs(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range docs {
		out = append(out, d.Path)
	}
	return strings.Join(out, ",")
}

func cursorOf(t *testing.T, s *Store) Cursor {
	t.Helper()
	c, err := s.Cursor(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func page(path string) Document {
	return Document{Path: path, Chunks: []search.Chunk{{Ord: 0, Breadcrumb: path, Body: "row " + path}}}
}

// A page's documents and the cursor it moved commit together or not at all: a batch that Puts,
// sets the cursor and is rolled back leaves both as the last committed page left them; committed,
// it has both. The cursor keeps microseconds (rounded down, never past a row), the refresh times
// beside it are left alone by a cursor move and the other way round, and another tenant's cursor
// is its own. The mutant, named here: SetCursor writing outside the batch's transaction (on the
// pool) — the rolled-back batch then leaves its cursor behind with no documents.
func TestACursorCommitsWithItsPage(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if c := cursorOf(t, s); !c.IsZero() {
		t.Fatalf("a new tenant's cursor: %+v, want the start", c)
	}
	t0 := time.Date(2026, 10, 12, 9, 0, 0, 123456789, time.UTC)
	first := Cursor{UpdatedAt: t0, ID: "a"}
	commitPage := func(c Cursor, commit bool, paths ...string) {
		t.Helper()
		b := s.Begin(ctx)
		for _, p := range paths {
			if _, err := b.Put(ctx, page(p)); err != nil {
				_ = b.Rollback()
				t.Fatal(err)
			}
		}
		if err := b.SetCursor(ctx, c); err != nil {
			_ = b.Rollback()
			t.Fatal(err)
		}
		if commit {
			err = b.Commit()
		} else {
			err = b.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	commitPage(first, true, "a.md")
	got := cursorOf(t, s)
	if !got.UpdatedAt.Equal(t0.Truncate(time.Microsecond)) || got.ID != "a" {
		t.Fatalf("the first page's cursor: %+v, want %v to the microsecond and id a", got, t0)
	}

	// rows sharing that timestamp: the second page's cursor differs by id alone
	second := Cursor{UpdatedAt: t0, ID: "b"}
	commitPage(second, false, "b.md")
	if p, c := docPaths(t, s), cursorOf(t, s); p != "a.md" || c.ID != "a" {
		t.Fatalf("after a rolled-back page: documents %s, cursor %+v; want a.md and the first page's cursor", p, c)
	}
	commitPage(second, true, "b.md")
	if p, c := docPaths(t, s), cursorOf(t, s); p != "a.md,b.md" || c.ID != "b" || !c.UpdatedAt.Equal(got.UpdatedAt) {
		t.Fatalf("after the committed page: documents %s, cursor %+v; want both and id b", p, c)
	}

	// the refresh's times and the cursor are written apart
	r := Refresh{Started: t0.Add(time.Hour), Finished: t0.Add(2 * time.Hour)}
	b := s.Begin(ctx)
	if err := b.SetRefreshed(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	commitPage(Cursor{UpdatedAt: t0.Add(time.Minute), ID: "c"}, true, "c.md")
	if got, err := s.Refreshed(ctx); err != nil || !got.Started.Equal(r.Started.Truncate(time.Microsecond)) || !got.Finished.Equal(r.Finished.Truncate(time.Microsecond)) {
		t.Errorf("the refresh after a cursor move: %+v, %v; want %+v", got, err, r)
	}
	if c := cursorOf(t, s); c.ID != "c" {
		t.Errorf("the cursor after a refresh was recorded: %+v", c)
	}

	// another tenant's cursor and refresh are its own
	u, err := New(conn, "u")
	if err != nil {
		t.Fatal(err)
	}
	if c := cursorOf(t, u); !c.IsZero() {
		t.Errorf("another tenant's cursor: %+v, want the start", c)
	}
	if r, err := u.Refreshed(ctx); err != nil || r != (Refresh{}) {
		t.Errorf("another tenant's refresh: %+v, %v", r, err)
	}
}

// A failed row is quarantined in the batch's transaction: quarantined again it keeps when it first
// failed and takes the new error and time; Quarantined pages the one retried longest ago first; a
// release commits with its batch, or not at all; another tenant's quarantine is its own. The
// mutants, named here: Quarantine as a plain Upsert (a second failure overwrites first_seen), and
// Release outside the batch's transaction (a rolled-back release still releases).
func TestQuarantineKeepsWhenARowFirstFailed(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	at := t1
	was := clock
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = was })
	quarantine := func(id, msg string, commit bool) {
		t.Helper()
		b := s.Begin(ctx)
		if err := b.Quarantine(ctx, id, errors.New(msg)); err != nil {
			_ = b.Rollback()
			t.Fatal(err)
		}
		if commit {
			err = b.Commit()
		} else {
			err = b.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	quarantine("x", "no title", true)
	at = t1.Add(time.Minute)
	quarantine("y", "no id", true)
	at = t1.Add(2 * time.Minute)
	quarantine("x", "render failed", true)
	at = t1.Add(3 * time.Minute)
	quarantine("z", "never kept", false)

	q, err := s.Quarantined(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 2 {
		t.Fatalf("quarantined: %+v; want x and y (z was rolled back)", q)
	}
	// y was last seen at t1+1m, x at t1+2m: y is retried first
	y, x := q[0], q[1]
	if y.SourceID != "y" || x.SourceID != "x" {
		t.Fatalf("the order: %s, %s; want y (seen longest ago) then x", y.SourceID, x.SourceID)
	}
	if !x.FirstSeen.Equal(t1) || !x.LastSeen.Equal(t1.Add(2*time.Minute)) || x.Err != "render failed" {
		t.Errorf("x quarantined twice: %+v; want first seen %v, last seen +2m, the second error", x, t1)
	}
	if one, err := s.Quarantined(ctx, 1); err != nil || len(one) != 1 || one[0].SourceID != "y" {
		t.Errorf("a page of one: %+v, %v; want y", one, err)
	}

	release := func(id string, commit bool) {
		t.Helper()
		b := s.Begin(ctx)
		if err := b.Release(ctx, id); err != nil {
			_ = b.Rollback()
			t.Fatal(err)
		}
		if commit {
			err = b.Commit()
		} else {
			err = b.Rollback()
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	release("x", false)
	if q, _ := s.Quarantined(ctx, 10); len(q) != 2 {
		t.Errorf("after a rolled-back release: %+v; want x still quarantined", q)
	}
	release("x", true)
	release("never-quarantined", true)
	if q, _ := s.Quarantined(ctx, 10); len(q) != 1 || q[0].SourceID != "y" {
		t.Errorf("after the release: %+v; want y alone", q)
	}

	u, err := New(conn, "u")
	if err != nil {
		t.Fatal(err)
	}
	if q, err := u.Quarantined(ctx, 10); err != nil || len(q) != 0 {
		t.Errorf("another tenant's quarantine: %+v, %v", q, err)
	}
	b := s.Begin(ctx)
	if err := b.Quarantine(ctx, "", errors.New("x")); err == nil {
		t.Error("a quarantine with no source id was kept")
	}
	if err := b.Quarantine(ctx, "w", nil); err == nil {
		t.Error("a quarantine with no error was kept")
	}
	_ = b.Rollback()
	if q, _ := s.Quarantined(ctx, 0); q != nil {
		t.Errorf("a page of none: %+v", q)
	}
}

// The tenant's access policy: none is ErrNoPolicy; one with no field, an empty or repeated field,
// or a field with '=' is refused with ErrNoAccess; set, it reads back as written, and a second set
// replaces it; another tenant has none. The mutant, named here: SetPolicy without its check —
// an empty policy is then kept, and a search over it would check nothing.
func TestATenantsAccessPolicy(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Policy(ctx); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("no policy: %v, want ErrNoPolicy", err)
	}
	for _, bad := range [][]string{nil, {}, {""}, {"label_id", "label_id"}, {"a=b"}} {
		if err := s.SetPolicy(ctx, Policy{AccessFields: bad}); !errors.Is(err, ErrNoAccess) {
			t.Errorf("policy %q: %v, want ErrNoAccess", bad, err)
		}
	}
	if _, err := s.Policy(ctx); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("after the refusals: %v, want still no policy", err)
	}
	if err := s.SetPolicy(ctx, Policy{AccessFields: []string{"label_id", "territory"}, View: "track"}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Policy(ctx); err != nil || strings.Join(p.AccessFields, ",") != "label_id,territory" || p.View != "track" {
		t.Errorf("the policy: %+v, %v", p, err)
	}
	if err := s.SetPolicy(ctx, Policy{AccessFields: []string{"org"}, View: "track2"}); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Policy(ctx); err != nil || strings.Join(p.AccessFields, ",") != "org" || p.View != "track2" {
		t.Errorf("the replaced policy: %+v, %v", p, err)
	}
	u, err := New(conn, "u")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.Policy(ctx); !errors.Is(err, ErrNoPolicy) {
		t.Errorf("another tenant's policy: %v, want ErrNoPolicy", err)
	}
}

// Counts are a tenant's documents, the unready ones and its quarantined rows, one tenant's only:
// another tenant's documents and quarantine are not counted. The mutants, named here: the unready
// count without its ready predicate (every document); the counts unfiltered by tenant.
func TestCountsAreTheTenantsDocumentsReadinessAndQuarantine(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", accessDocs) // five documents, b2.md unready
	other := openCorpus(t, conn, "u", accessDocs[:2])
	b := other.Begin(ctx)
	if err := b.Quarantine(ctx, "elsewhere", errors.New("a bad row")); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	b = s.Begin(ctx)
	for _, id := range []string{"x", "y"} {
		if err := b.Quarantine(ctx, id, errors.New("a bad row")); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	c, err := s.Counts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if c != (Counts{Documents: 5, Unready: 1, Quarantined: 2}) {
		t.Errorf("counts %+v, want 5 documents, 1 unready, 2 quarantined", c)
	}
}

// Counts on a context already ended is an error, naming Counts, and counts nothing.
func TestCountsOnAnEndedContextFails(t *testing.T) {
	conn := scratch(t)
	s := openCorpus(t, conn, "t", accessDocs[:1])
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if c, err := s.Counts(ctx); err == nil || !strings.Contains(err.Error(), "pgstore: counts") || c != (Counts{}) {
		t.Errorf("an ended context: %+v, %v", c, err)
	}
}
