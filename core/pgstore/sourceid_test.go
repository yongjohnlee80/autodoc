package pgstore

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// Migration 000004 adds rag_document.source_id over a populated 000003 schema: the rows written
// before keep everything and read "" for it; Put writes it, and a Put again replaces it; the revert
// drops exactly the column and keeps the rows; migrating again brings it back. The mutant, named
// here: without 000004 in the script set, Put fails on the missing column.
func TestMigration000004AddsTheSourceIDOverAPopulatedSchema(t *testing.T) {
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := scratchName(t, admin, "autodoc_pgstore_")
	conn, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	if _, err := deploy.New(migrationsUpTo(t, 3), deploy.Ledger(Ledger)).Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	exec(t, conn, `INSERT INTO rag_document (tenant, path, title, version, generation) VALUES ('t', 'old.md', 'Old', 'v1', 2)`)
	if _, err := deploy.New(migrationsUpTo(t, 4), deploy.Ledger(Ledger)).Apply(ctx, conn); err != nil {
		t.Fatalf("000004 onto a populated 000003: %v", err)
	}
	if got := scalar(t, conn, "SELECT title || '|' || version || '|' || generation || '|' || source_id FROM rag_document WHERE path = 'old.md'"); got != "Old|v1|2|" {
		t.Fatalf("the 000003 row after 000004: %q", got)
	}
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(ctx, Document{Path: "new.md", SourceID: "42"}); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, conn, "SELECT source_id FROM rag_document WHERE path = 'new.md'"); got != "42" {
		t.Errorf("Put stored the source id %q, want 42", got)
	}
	b := s.Begin(ctx)
	if _, err := b.Put(ctx, Document{Path: "new.md", SourceID: "43"}); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, conn, "SELECT source_id FROM rag_document WHERE path = 'new.md'"); got != "43" {
		t.Errorf("a batch's Put again stored %q, want 43", got)
	}

	if _, err := deploy.New(migrations, deploy.Ledger(Ledger)).Revert(ctx, conn, 4); err != nil {
		t.Fatalf("reverting 000004: %v", err)
	}
	if got := scalar(t, conn, "SELECT count(*) FROM information_schema.columns WHERE table_name = 'rag_document' AND column_name = 'source_id'"); got != "0" {
		t.Errorf("the revert left source_id: %s columns", got)
	}
	if got := scalar(t, conn, "SELECT string_agg(path, ',' ORDER BY path) FROM rag_document"); got != "new.md,old.md" {
		t.Errorf("the revert lost rows: %q", got)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if got := scalar(t, conn, "SELECT source_id FROM rag_document WHERE path = 'new.md'"); got != "" {
		t.Errorf("migrated again, the column is back empty: %q", got)
	}
}

// A View's CandidateSourceIDs are the source ids of exactly the documents it handed out as
// candidates, read under its access: a View within label A that retrieved every A document answers
// their ids and no B document's, though B's are stored with ids too; a View that retrieved nothing
// answers none; one that listed a single document answers only that one. The mutant, named here:
// reading the ids without the access predicate answers B's ids beside A's when B is recorded.
func TestCandidateSourceIDsAreTheViewsCandidatesWithinItsAccess(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", accessDocs)
	for _, d := range accessDocs {
		exec(t, conn, "UPDATE rag_document SET source_id = $1 WHERE path = $2", "id-"+d.Path, d.Path)
	}
	ids := func(v *View) map[string]string {
		t.Helper()
		got, err := v.CandidateSourceIDs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if err := s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, func(v *View) error {
		if got := ids(v); len(got) != 0 {
			t.Errorf("before any retrieval: %v", got)
		}
		if _, err := v.Lexical(ctx, []search.Term{{Text: "common"}}, search.Filter{}, 100); err != nil {
			return err
		}
		if _, err := v.Semantic(ctx, model, searchtest.Embed("common words gamma"), search.Filter{}, 100); err != nil {
			return err
		}
		want := map[string]string{"a1.md": "id-a1.md", "a2.md": "id-a2.md"}
		if got := ids(v); !maps.Equal(got, want) {
			t.Errorf("within A: %v, want %v", got, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// a candidate recorded for a document outside the access (as a bug in a retrieval would) is
	// still not answered: the read itself is scoped
	if err := s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, func(v *View) error {
		var b1 int64
		if err := s.View(ctx, func(all *View) error {
			got, err := all.List(ctx, search.Filter{Paths: []string{"b1.md"}}, 1)
			if err == nil && len(got) == 1 {
				b1 = got[0].Doc
			}
			return err
		}); err != nil || b1 == 0 {
			t.Fatalf("b1.md's id: %d, %v", b1, err)
		}
		v.saw([]search.Candidate[int64]{{Doc: b1, Path: "b1.md"}})
		if got := ids(v); len(got) != 0 {
			t.Errorf("a B document recorded within A answered: %v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.View(ctx, func(v *View) error {
		got, err := v.List(ctx, search.Filter{}, 1)
		if err != nil {
			return err
		}
		paths := slices.Collect(maps.Keys(ids(v)))
		if len(got) != 1 || !slices.Equal(paths, []string{got[0].Path}) {
			t.Errorf("one listed document answered %v", paths)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// ViewPolicy holds access to the tenant's policy, read in the View's own snapshot: no policy is
// ErrNoPolicy; a scope missing a field, with no or an empty or a repeated value, or with a field
// outside the policy is ErrNoAccess before check runs, so check cannot loosen it; check's own
// refusal is returned as it is. The barrier: a policy that gains a field, committed with a new
// document while check runs (after the policy read fixed the snapshot), is not seen by the
// retrieval, which answers exactly what the old policy admitted over the old documents; the next
// ViewPolicy sees it and refuses the old scope. The mutant, named here: reading the policy before
// the View's transaction lets the retrieval see the new document under the old policy.
func TestViewPolicyHoldsAccessToThePolicyInItsSnapshot(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", accessDocs)
	run := func(access map[string][]string, check func(Policy, map[string][]string) error) ([]string, error) {
		var paths []string
		err := s.ViewPolicy(ctx, access, check, func(v *View) error {
			got, err := v.List(ctx, search.Filter{}, 100)
			for _, c := range got {
				paths = append(paths, c.Path)
			}
			slices.Sort(paths)
			return err
		})
		return paths, err
	}
	if _, err := run(map[string][]string{"label_id": {"A"}}, nil); !errors.Is(err, ErrNoPolicy) {
		t.Fatalf("no policy: %v", err)
	}
	if err := s.SetPolicy(ctx, Policy{AccessFields: []string{"label_id"}, View: "v"}); err != nil {
		t.Fatal(err)
	}
	checked := 0
	loosen := func(Policy, map[string][]string) error { checked++; return nil }
	for name, access := range map[string]map[string][]string{
		"none":          {},
		"no values":     {"label_id": {}},
		"an empty one":  {"label_id": {""}},
		"a repeat":      {"label_id": {"A", "A"}},
		"another field": {"label_id": {"A"}, "territory": {"US"}},
		"another only":  {"territory": {"US"}},
	} {
		if _, err := run(access, loosen); !errors.Is(err, ErrNoAccess) {
			t.Errorf("%s: %v, want ErrNoAccess", name, err)
		}
	}
	if checked != 0 {
		t.Errorf("check ran %d times for scopes the policy refuses", checked)
	}
	errMine := errors.New("check's own refusal")
	if _, err := run(map[string][]string{"label_id": {"A"}}, func(p Policy, _ map[string][]string) error {
		if !slices.Equal(p.AccessFields, []string{"label_id"}) {
			t.Errorf("check was given the policy %+v", p)
		}
		return errMine
	}); !errors.Is(err, errMine) {
		t.Errorf("check's refusal: %v", err)
	}
	if got, err := run(map[string][]string{"label_id": {"A"}}, nil); err != nil || !slices.Equal(got, []string{"a1.md", "a2.md"}) {
		t.Fatalf("within A: %v, %v", got, err)
	}

	// the barrier: while check runs, the policy gains team, and Y (label A, team t2) is written
	barrier := func(Policy, map[string][]string) error {
		if err := s.SetPolicy(ctx, Policy{AccessFields: []string{"label_id", "team"}, View: "v"}); err != nil {
			return err
		}
		put(t, s, searchtest.Doc{Path: "y.md", Facets: map[string][]string{"label_id": {"A"}, "team": {"t2"}}, Chunks: []string{"common words zeta"}})
		return nil
	}
	got, err := run(map[string][]string{"label_id": {"A"}}, barrier)
	if err != nil || !slices.Equal(got, []string{"a1.md", "a2.md"}) {
		t.Fatalf("the snapshot's answer: %v, %v; want a1.md and a2.md, the old policy over the old documents", got, err)
	}
	if _, err := run(map[string][]string{"label_id": {"A"}}, nil); !errors.Is(err, ErrNoAccess) {
		t.Errorf("after the change, the old scope: %v, want ErrNoAccess", err)
	}
	if got, err := run(map[string][]string{"label_id": {"A"}, "team": {"t2"}}, nil); err != nil || !slices.Equal(got, []string{"y.md"}) {
		t.Errorf("the new scope: %v, %v; want y.md", got, err)
	}
}
