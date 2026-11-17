package pgstore

import (
	"context"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// sweep runs SweepEmbeddings, failing the test on its error.
func sweep(t *testing.T, s *Store) int {
	t.Helper()
	n, err := s.SweepEmbeddings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// The sweep deletes the tenant's vectors, under every model, of texts no chunk of the tenant has:
// a document rewritten with other text leaves its old text's vectors, which go; a text another
// document still has keeps its vectors; a vector written for a text with no chunk (the embedding
// worker after a delete took its last chunk) is collected by the next sweep; another tenant's
// orphan is untouched, and an orphan of the tenant whose text another tenant's chunk has goes; and
// a text one document drops and another gains in the same batch keeps its vector. The mutants,
// named here: the tenant predicate dropped (the other tenant's orphan goes); the chunk's tenant
// dropped from the NOT EXISTS (the orphan another tenant's chunk has is kept).
func TestTheSweepTakesTheVectorsNoTextHas(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	u := openCorpus(t, conn, "u", nil)
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	sourced(t, s, "x.md", "x", at, "the old words of x")
	sourced(t, s, "y.md", "y", at, "a chorus they share", "y alone")
	sourced(t, s, "z.md", "z", at, "a chorus they share")
	sourced(t, u, "u.md", "u", at, "u's old words")
	sourced(t, u, "u2.md", "u2", at, "the old words of x")
	sourced(t, s, "x.md", "x", at.Add(time.Hour), "the new words of x") // rewritten: its old text is an orphan
	sourced(t, s, "y.md", "y", at.Add(time.Hour), "y alone")            // drops the shared text, which z keeps
	sourced(t, u, "u.md", "u", at.Add(time.Hour), "the new words of u") // u's orphan, not t's to sweep
	ghost := search.Chunk{Body: "a text a delete just took", Embed: "a text a delete just took"}.TextHash()
	if err := s.Embed(ctx, model, []Embedding{{TextHash: ghost, Vec: searchtest.Embed("ghost")}}); err != nil {
		t.Fatal(err)
	}
	if n := sweep(t, s); n != 3 {
		t.Errorf("the sweep deleted %d; want 3: the old text's under two models, and the vector with no chunk", n)
	}
	for _, c := range []struct{ tenant, body, want string }{
		{"t", "the old words of x", "0"}, {"t", "the new words of x", "2"}, {"t", "a chorus they share", "2"},
		{"t", "y alone", "2"}, {"t", "a text a delete just took", "0"}, {"u", "the old words of x", "2"},
		{"u", "u's old words", "2"},
	} {
		if got := textsOf(t, conn, c.tenant, c.body); got != c.want {
			t.Errorf("%s's vectors of %q: %s; want %s", c.tenant, c.body, got, c.want)
		}
	}
	if n := sweep(t, s); n != 0 {
		t.Errorf("a second sweep deleted %d; want none", n)
	}

	// in one batch, x drops its text and z gains it: the sweep after it keeps the vector
	b := s.Begin(ctx)
	for _, d := range []struct{ path, body string }{{"x.md", "x again"}, {"z.md", "the new words of x"}} {
		c := search.Chunk{Ord: 0, Breadcrumb: d.path, Body: d.body, Embed: d.body, ByteEnd: len(d.body)}
		if _, err := b.Put(ctx, Document{Path: d.path, Facets: map[string][]string{"label_id": {"A"}}, Chunks: []search.Chunk{c}}); err != nil {
			_ = b.Rollback()
			t.Fatal(err)
		}
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	sweep(t, s)
	if got := textsOf(t, conn, "t", "the new words of x"); got != "2" {
		t.Errorf("a text one document dropped and another gained in one batch: %s vectors; want its 2 kept", got)
	}
}

// A text that gains a chunk between a batch's read of its orphans and their delete keeps its
// vector, and is not counted: the delete evaluates the NOT EXISTS again. The mutant, named here: the
// NOT EXISTS dropped from the delete.
func TestATextThatGainsAChunkMidSweepKeepsItsVector(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	sourced(t, s, "x.md", "x", at, "words a write takes back")
	sourced(t, s, "x.md", "x", at.Add(time.Hour), "other words")
	sweepRead = func(context.Context) {
		body := "words a write takes back"
		c := search.Chunk{Ord: 0, Breadcrumb: "w.md", Body: body, Embed: body, ByteEnd: len(body)}
		if _, err := s.Put(ctx, Document{Path: "w.md", Facets: map[string][]string{"label_id": {"A"}}, Chunks: []search.Chunk{c}}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { sweepRead = func(context.Context) {} })
	if n := sweep(t, s); n != 0 {
		t.Errorf("the sweep counted %d deleted; want 0, the text having gained a chunk", n)
	}
	if got := textsOf(t, conn, "t", "words a write takes back"); got != "2" {
		t.Errorf("the text's vectors after it gained a chunk mid-sweep: %s; want its 2 kept", got)
	}
}

// The sweep goes in batches of sweepBatch, each its own transaction, until a batch comes back short:
// five orphans at a batch of two are all swept.
func TestTheSweepGoesInBatches(t *testing.T) {
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	was := sweepBatch
	sweepBatch = 2
	t.Cleanup(func() { sweepBatch = was })
	var vecs []Embedding
	for _, text := range []string{"one", "two", "three", "four", "five"} {
		vecs = append(vecs, Embedding{TextHash: search.Chunk{Body: text, Embed: text}.TextHash(), Vec: searchtest.Embed(text)})
	}
	if err := s.Embed(context.Background(), model, vecs); err != nil {
		t.Fatal(err)
	}
	if n := sweep(t, s); n != 5 {
		t.Fatalf("five orphans at a batch of two: %d swept; want 5", n)
	}
}
