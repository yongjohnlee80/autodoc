package index

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// readCount counts the statements that read.
type readCount struct {
	dao.NopHook
	n atomic.Int64
}

func (c *readCount) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	if q.Op == dao.OpSelect || q.Op == dao.OpGet || q.Op == dao.OpCount {
		c.n.Add(1)
	}
	return nil
}

// signalsInTwoReads is the signals cell, on e's engine: a View's signals for any number of
// documents are two statements, the in-links and the tags, and answer what one document's read
// answered: distinct linking documents from bodies, a document's link to itself not counted, its
// tags in key order, none as an empty list.
func signalsInTwoReads(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	e.put("a.md", "---\ntags: [y, x]\n---\n[[b]] [[c]] [[c]]\n", "b.md", "[[c]] [[b]]\n",
		"c.md", "---\ntags: [z]\n---\nend\n", "d.md", "[[c]]\n")
	c := &readCount{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(c)}
	id := func(p string) int64 {
		t.Helper()
		var out int64
		if err := s.read(ctx, func(tx *store.Tx) error {
			var err error
			out, err = s.docID(tx, p)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return out
	}
	a, b, cc, d := id("a.md"), id("b.md"), id("c.md"), id("d.md")
	want := map[int64]struct {
		in   int
		tags []string
	}{a: {0, []string{"x", "y"}}, b: {1, []string{}}, cc: {3, []string{"z"}}, d: {0, []string{}}}
	for _, ids := range [][]int64{{cc}, {a, b}, {a, b, cc, d}} {
		c.n.Store(0)
		var got map[int64]search.Signals
		if err := (searchStore{s: s}).View(ctx, func(v *View) error {
			var err error
			got, err = v.Signals(ctx, ids)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if n := c.n.Load(); n != 2 {
			t.Errorf("signals of %d documents: %d reads; want 2", len(ids), n)
		}
		for _, x := range ids {
			if g, w := got[x], want[x]; g.InLinks != w.in || !slices.Equal(g.Tags, w.tags) || g.Tags == nil {
				t.Errorf("document %d: %+v; want %d in-links, tags %v", x, g, w.in, w.tags)
			}
		}
	}
}

// manyInLinks is the bound of the signals' first read: every link row into the candidates, so a
// document 200 others link to is read as 200 pairs, in one statement, and counted 200.
func manyInLinks(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	pc := []string{"hub.md", "the hub\n"}
	for i := range 200 {
		pc = append(pc, fmt.Sprintf("spoke/%03d.md", i), "[[hub]]\n")
	}
	e.put(pc...)
	c := &readCount{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(c)}
	var hub int64
	if err := s.read(ctx, func(tx *store.Tx) error { var err error; hub, err = s.docID(tx, "hub.md"); return err }); err != nil {
		t.Fatal(err)
	}
	c.n.Store(0)
	var got map[int64]search.Signals
	if err := (searchStore{s: s}).View(ctx, func(v *View) error {
		var err error
		got, err = v.Signals(ctx, []int64{hub})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if n := c.n.Load(); n != 2 || got[hub].InLinks != 200 {
		t.Errorf("a document 200 others link to: %d reads, %d in-links; want 2 and 200", n, got[hub].InLinks)
	}
}

// chunkTextsInTwoReads is the reranker texts' cell, on e's engine: the texts of any number of hits
// are two reads, the documents and the chunks at their byte starts, each hit its own chunk's
// breadcrumb and body; a hit at another generation is still stale.
func chunkTextsInTwoReads(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	e.put("a.md", "# A\n\nalpha words\n\n## Two\n\nalpha more\n", "b.md", "# B\n\nalpha beta\n", "c.md", "# C\n\nalpha gamma\n")
	res := e.search("alpha", QueryOpts{Mode: ModeLexical})
	if len(res.Hits) < 4 {
		t.Fatalf("hits %d; want the four sections", len(res.Hits))
	}
	var hits []search.Hit
	for _, h := range res.Hits {
		hits = append(hits, search.Hit{Path: h.Path, Generation: h.Generation, ByteStart: h.ByteStart, ByteEnd: h.ByteEnd})
	}
	c := &readCount{}
	s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(c)}
	for _, n := range []int{1, len(hits)} {
		c.n.Store(0)
		texts, err := s.chunkTexts(ctx, hits[:n])
		if err != nil {
			t.Fatal(err)
		}
		if r := c.n.Load(); r != 2 {
			t.Errorf("the texts of %d hits: %d reads; want 2", n, r)
		}
		for i, x := range texts {
			if !strings.Contains(x, "alpha") || !strings.Contains(x, "\n") || strings.Count(x, "alpha") != 1 {
				t.Errorf("hit %d (%s %d-%d): text %q; want its own chunk", i, hits[i].Path, hits[i].ByteStart, hits[i].ByteEnd, x)
			}
		}
	}
	stale := slices.Clone(hits)
	stale[1].Generation++
	if _, err := s.chunkTexts(ctx, stale); !errors.Is(err, rank.ErrStale) {
		t.Errorf("a hit at another generation: %v; want rank.ErrStale", err)
	}
}

// TestSignalsAreTwoReads: the signals cell on SQLite. The mutant, named here: one in-link count
// and one tag read per document (2 per document read).
func TestSignalsAreTwoReads(t *testing.T) { signalsInTwoReads(t, newEnv(t, options{})) }

// TestSignalsBoundByTheirInLinks: manyInLinks on SQLite.
func TestSignalsBoundByTheirInLinks(t *testing.T) { manyInLinks(t, newEnv(t, options{})) }

// TestRerankerTextsAreTwoReads: chunkTextsInTwoReads on SQLite. The mutant, named here: a read
// of the document and one of its chunk per hit.
func TestRerankerTextsAreTwoReads(t *testing.T) { chunkTextsInTwoReads(t, newEnv(t, options{})) }
