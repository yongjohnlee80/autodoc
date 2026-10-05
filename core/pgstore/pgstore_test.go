package pgstore

import (
	"context"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// dsnEnv names the PostgreSQL (with pgvector) these cells run against: a DSN whose user may create
// databases, such as VM43's autodb-r3-pg. Each test creates a scratch database of its own there
// and drops it when it ends. Without it the cells skip, unless AUTODOC_TEST_PG_REQUIRED names a
// runner whose service the cells must not skip on: then a missing URL fails loudly instead.
const dsnEnv = "AUTODOC_TEST_PGURL"

// scratch opens a fresh database with pgstore's migrations applied, dropped when t ends.
func scratch(t *testing.T) dao.DataConn {
	t.Helper()
	dsn := dsnBase(t)
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to %s: %v", dsnEnv, err)
	}
	t.Cleanup(func() { closeConn(admin) })
	name := scratchName(t, admin, "autodoc_pgstore_")
	conn, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name))
	if err != nil {
		t.Fatalf("connect to the scratch database: %v", err)
	}
	t.Cleanup(func() { closeConn(conn) })
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func closeConn(c dao.DataConn) { c.Close() }

const model = "test|embed||64"

// put writes a corpus document with Embed(body) as each chunk's vector, and marks it ready unless
// the corpus says otherwise.
func put(t testing.TB, s *Store, d searchtest.Doc) {
	t.Helper()
	ctx := context.Background()
	var chunks []search.Chunk
	var vecs []Embedding
	for i, body := range d.Chunks {
		c := search.Chunk{Ord: i, Breadcrumb: d.Path, Body: body, ByteStart: i * 100, ByteEnd: i*100 + len(body)}
		chunks = append(chunks, c)
		vecs = append(vecs, Embedding{TextHash: c.TextHash(), Vec: searchtest.Embed(body)})
	}
	if _, err := s.Put(ctx, Document{Path: d.Path, Tags: d.Tags, Facets: d.Facets, Links: d.Links, Chunks: chunks}); err != nil {
		t.Fatal(err)
	}
	if d.Unready {
		return
	}
	if err := s.Embed(ctx, model, vecs); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.MarkReady(ctx, d.Path, model); err != nil || !ready {
		t.Fatalf("mark %s ready: %v, %v", d.Path, ready, err)
	}
}

func openCorpus(t testing.TB, conn dao.DataConn, tenant string, docs []searchtest.Doc) *Store {
	t.Helper()
	s, err := Open(conn, tenant)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetModels(context.Background(), model, ""); err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		put(t, s, d)
	}
	return s
}

// TestConformance is golib's search port conformance suite over pgstore, on PostgreSQL.
func TestConformance(t *testing.T) {
	conn := scratch(t)
	searchtest.Run(t, func(t testing.TB, docs []searchtest.Doc) searchtest.Fixture[int64, *View] {
		s := openCorpus(t, conn, "tenant-a", docs)
		return searchtest.Fixture[int64, *View]{Store: s, Model: model, Change: func(path string, bodies []string) {
			put(t, s, searchtest.Doc{Path: path, Chunks: bodies})
		}}
	})
}

// Two tenants with the same paths and texts: neither ever answers with the other's rows, and each
// has its own embeddings and models.
func TestTenantsAreIsolated(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	shared := []searchtest.Doc{
		{Path: "a.md", Tags: []string{"x"}, Facets: map[string][]string{"kind": {"k"}}, Chunks: []string{"common words alpha"}},
		{Path: "b.md", Tags: []string{"x"}, Chunks: []string{"common words beta"}, Links: []string{"a.md"}},
	}
	a := openCorpus(t, conn, "tenant-a", shared)
	b := openCorpus(t, conn, "tenant-b", shared)
	put(t, b, searchtest.Doc{Path: "only-b.md", Tags: []string{"x"}, Facets: map[string][]string{"kind": {"k"}}, Chunks: []string{"common words gamma"}, Links: []string{"a.md"}})

	idsOf := func(s *Store) map[int64]bool {
		ids := map[int64]bool{}
		s.View(ctx, func(v *View) error {
			got, _ := v.Lexical(ctx, []search.Term{{Text: "common"}}, search.Filter{}, 100)
			for _, c := range got {
				ids[c.Doc] = true
			}
			return nil
		})
		return ids
	}
	aIDs, bIDs := idsOf(a), idsOf(b)
	if len(aIDs) != 2 || len(bIDs) != 3 {
		t.Fatalf("lexical: tenant a sees %d documents, b %d; want 2 and 3", len(aIDs), len(bIDs))
	}
	for id := range aIDs {
		if bIDs[id] {
			t.Errorf("document %d answers for both tenants", id)
		}
	}
	a.View(ctx, func(v *View) error {
		f := search.Filter{Tags: []string{"x"}}
		if got, err := v.Semantic(ctx, model, searchtest.Embed("common words gamma"), f, 10); err != nil || len(got) != 2 {
			t.Errorf("semantic: tenant a answered %d hits, %v; want its own 2", len(got), err)
		} else {
			for _, c := range got {
				if !aIDs[c.Doc] || c.Path == "only-b.md" {
					t.Errorf("semantic: tenant a answered %s (%d)", c.Path, c.Doc)
				}
			}
		}
		if got, err := v.List(ctx, search.Filter{Facets: map[string][]string{"kind": {"k"}}}, 10); err != nil || len(got) != 1 || got[0].Path != "a.md" {
			t.Errorf("list: tenant a answered %+v, %v", got, err)
		}
		hits, err := v.Lexical(ctx, []search.Term{{Text: "alpha"}}, search.Filter{}, 1)
		if err != nil || len(hits) != 1 || hits[0].Path != "a.md" {
			t.Fatalf("finding tenant a's a.md: %+v, %v", hits, err)
		}
		sig, err := v.Signals(ctx, []int64{hits[0].Doc})
		if err != nil {
			t.Fatal(err)
		}
		// a's b.md links to a.md; b's b.md and only-b.md do too, under the same path
		if got := sig[hits[0].Doc].InLinks; got != 1 {
			t.Errorf("signals: tenant a counts %d in-links to a.md, want its own 1", got)
		}
		return nil
	})
	// each tenant's embeddings are its own rows: b's text gamma has no vector under a
	n, err := a.rw.emb.DAO().With(eTenant, "tenant-a").Count()
	if err != nil || n != 2 {
		t.Errorf("tenant a holds %d embeddings, %v; want 2", n, err)
	}
	// switching one tenant's model leaves the other's
	if err := b.SetModels(ctx, "other|model||64", ""); err != nil {
		t.Fatal(err)
	}
	a.View(ctx, func(v *View) error {
		if _, err := v.Semantic(ctx, model, searchtest.Embed("common"), search.Filter{}, 5); err != nil {
			t.Errorf("tenant a after b switched: %v", err)
		}
		return nil
	})
	b.View(ctx, func(v *View) error {
		if _, err := v.Semantic(ctx, model, searchtest.Embed("common"), search.Filter{}, 5); err != search.ErrModelChanged {
			t.Errorf("tenant b after its switch: %v, want ErrModelChanged", err)
		}
		return nil
	})
}

// A model flip committed between embedding the query and the View answers ErrModelChanged, and a
// switch in progress reads as StateSwitching.
func TestModelFlip(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	s := openCorpus(t, conn, "t", []searchtest.Doc{{Path: "a.md", Chunks: []string{"words"}}})
	if err := s.SetModels(ctx, "new|model||64", ""); err != nil {
		t.Fatal(err)
	}
	s.View(ctx, func(v *View) error {
		if _, err := v.Semantic(ctx, model, searchtest.Embed("words"), search.Filter{}, 5); err != search.ErrModelChanged {
			t.Errorf("after a flip: %v, want ErrModelChanged", err)
		}
		return nil
	})
	if err := s.SetModels(ctx, model, "new|model||64"); err != nil {
		t.Fatal(err)
	}
	s.View(ctx, func(v *View) error {
		if st, err := v.SemanticState(ctx); err != nil || st != search.StateSwitching {
			t.Errorf("state while a target fills: %q, %v", st, err)
		}
		if got, err := v.Semantic(ctx, model, searchtest.Embed("words"), search.Filter{}, 5); err != nil || len(got) != 1 {
			t.Errorf("the active model still answers while switching: %v, %v", got, err)
		}
		return nil
	})
	// the flip lands inside an open View: the View keeps its snapshot's model
	s.View(ctx, func(v *View) error {
		if st, _ := v.SemanticState(ctx); st != search.StateSwitching {
			t.Fatalf("state %q", st)
		}
		if err := s.SetModels(ctx, "new|model||64", ""); err != nil {
			t.Fatal(err)
		}
		if got, err := v.Semantic(ctx, model, searchtest.Embed("words"), search.Filter{}, 5); err != nil || len(got) != 1 {
			t.Errorf("a View saw a flip committed after its snapshot: %v, %v", got, err)
		}
		return nil
	})
	s.View(ctx, func(v *View) error {
		if _, err := v.Semantic(ctx, model, searchtest.Embed("words"), search.Filter{}, 5); err != search.ErrModelChanged {
			t.Errorf("the next View after the flip: %v, want ErrModelChanged", err)
		}
		return nil
	})
}

// Paths with LIKE's wildcards and the escape character match only themselves and what is under
// them; a View cannot write.
func TestPathFilterIsLiteralAndViewsAreReadOnly(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	docs := []searchtest.Doc{
		{Path: "a%b/x.md", Chunks: []string{"needle"}},
		{Path: "aXb/x.md", Chunks: []string{"needle"}},
		{Path: "a_c/x.md", Chunks: []string{"needle"}},
		{Path: "abc/x.md", Chunks: []string{"needle"}},
		{Path: `a\d/x.md`, Chunks: []string{"needle"}},
		{Path: "a!e/x.md", Chunks: []string{"needle"}},
		{Path: "aZe/x.md", Chunks: []string{"needle"}},
		{Path: "dir/x.md", Chunks: []string{"needle"}},
		{Path: "dirx/y.md", Chunks: []string{"needle"}},
		{Path: "dir", Chunks: []string{"needle"}},
	}
	s := openCorpus(t, conn, "t", docs)
	for _, c := range []struct{ dir, want string }{
		{"a%b", "a%b/x.md"}, {"a_c", "a_c/x.md"}, {`a\d`, `a\d/x.md`}, {"a!e", "a!e/x.md"},
		// a directory admits what is under it, and a file of its own name, never a sibling
		// whose name starts with it
		{"dir", "dir dir/x.md"},
	} {
		dir := c.dir
		s.View(ctx, func(v *View) error {
			got, err := v.Lexical(ctx, []search.Term{{Text: "needle"}}, search.Filter{Paths: []string{dir}}, 100)
			var p []string
			for _, h := range got {
				p = append(p, h.Path)
			}
			if err != nil || strings.Join(p, " ") != c.want {
				t.Errorf("path %q admitted %v, %v; want %s", dir, p, err, c.want)
			}
			return nil
		})
	}
	err := s.View(ctx, func(v *View) error {
		return s.ro.meta.On(v.tx).Set(mTenant, "t").Set(mActive, "x").Upsert()
	})
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Errorf("a write inside a View: %v, want PostgreSQL's read-only refusal", err)
	}
}

// A document with vectors for some chunks only is not ready, and none of its chunks answers by
// meaning, even the ones that have a vector; by words it still answers.
func TestPartlyEmbeddedDocumentIsNotReady(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	s := openCorpus(t, conn, "t", nil)
	c0 := search.Chunk{Ord: 0, Breadcrumb: "p.md", Body: "vectored chunk words"}
	c1 := search.Chunk{Ord: 1, Breadcrumb: "p.md", Body: "unvectored chunk words"}
	if _, err := s.Put(ctx, Document{Path: "p.md", Chunks: []search.Chunk{c0, c1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Embed(ctx, model, []Embedding{{TextHash: c0.TextHash(), Vec: searchtest.Embed(c0.Body)}}); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.MarkReady(ctx, "p.md", model); err != nil || ready {
		t.Fatalf("MarkReady with one of two vectors: %v, %v; want not ready", ready, err)
	}
	s.View(ctx, func(v *View) error {
		if got, err := v.Semantic(ctx, model, searchtest.Embed(c0.Body), search.Filter{}, 5); err != nil || len(got) != 0 {
			t.Errorf("a document not ready answered by meaning: %+v, %v", got, err)
		}
		if got, err := v.Lexical(ctx, []search.Term{{Text: "vectored"}}, search.Filter{}, 5); err != nil || len(got) != 1 {
			t.Errorf("by words: %+v, %v", got, err)
		}
		if st, _ := v.SemanticState(ctx); st != search.StatePartial {
			t.Errorf("state %q, want partial", st)
		}
		return nil
	})
}
