package pgstore

import (
	"context"
	"slices"
	"testing"

	"github.com/yongjohnlee80/golib/search"
)

// paths are the paths of docs, in order.
func paths(docs []DocInfo) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.Path
	}
	return out
}

// Cell — what an embedding worker reads besides Pending. Models answers what SetModels recorded,
// "" before it; DocsWithTexts answers each document holding one of the hashes, once, and no other;
// Unready pages the documents not ready, and only them. The mutants, named here: Unready without
// its readiness condition (the ready document is listed too), and DocsWithTexts without its hash
// predicate (every document is listed).
func TestTheEmbeddingWorkersReads(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	other, err := New(conn, "u")
	if err != nil {
		t.Fatal(err)
	}
	if a, tg, err := s.Models(ctx); err != nil || a != "" || tg != "" {
		t.Fatalf("models before any were recorded: %q %q %v", a, tg, err)
	}
	if err := s.SetModels(ctx, "old|m||8", "new|m||8"); err != nil {
		t.Fatal(err)
	}
	if a, tg, err := s.Models(ctx); err != nil || a != "old|m||8" || tg != "new|m||8" {
		t.Fatalf("models: %q %q %v", a, tg, err)
	}
	if a, tg, err := other.Models(ctx); err != nil || a != "" || tg != "" {
		t.Errorf("another tenant's models: %q %q %v", a, tg, err)
	}

	chunk := func(body string) search.Chunk { return search.Chunk{Breadcrumb: "b", Body: body} }
	one, shared, alone := chunk("one"), chunk("shared words"), chunk("alone")
	for _, d := range []Document{
		{Path: "a.md", Chunks: []search.Chunk{one, shared}},
		{Path: "b.md", Chunks: []search.Chunk{shared}},
		{Path: "c.md", Chunks: []search.Chunk{alone}},
	} {
		if _, err := s.Put(ctx, d); err != nil {
			t.Fatal(err)
		}
	}
	// the same text in another tenant is that tenant's
	if _, err := other.Put(ctx, Document{Path: "z.md", Chunks: []search.Chunk{shared}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		hashes [][32]byte
		want   []string
	}{
		{[][32]byte{shared.TextHash()}, []string{"a.md", "b.md"}},
		{[][32]byte{alone.TextHash(), {9}, one.TextHash()}, []string{"a.md", "c.md"}},
		{[][32]byte{{9}}, nil},
		{nil, nil},
	} {
		got, err := s.DocsWithTexts(ctx, c.hashes)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("documents with %d texts: %v, %v; want %v", len(c.hashes), got, err, c.want)
		}
	}

	if got, more, err := s.Unready(ctx, "", 10); err != nil || more || !slices.Equal(paths(got), []string{"a.md", "b.md", "c.md"}) {
		t.Fatalf("unready before any vector: %v %v %v", paths(got), more, err)
	}
	if err := s.Embed(ctx, "old|m||8", []Embedding{{TextHash: shared.TextHash(), Vec: embedVec("s")}}); err != nil {
		t.Fatal(err)
	}
	if ready, err := s.MarkReady(ctx, "b.md", "old|m||8"); err != nil || !ready {
		t.Fatalf("b.md: %v %v", ready, err)
	}
	got, more, err := s.Unready(ctx, "", 1)
	if err != nil || !more || !slices.Equal(paths(got), []string{"a.md"}) {
		t.Fatalf("the first page of one: %v %v %v", paths(got), more, err)
	}
	got, more, err = s.Unready(ctx, "a.md", 1)
	if err != nil || more || !slices.Equal(paths(got), []string{"c.md"}) || got[0].Generation != 1 {
		t.Fatalf("the page after a.md: %+v %v %v", got, more, err)
	}
	if got, _, err := other.Unready(ctx, "", 10); err != nil || !slices.Equal(paths(got), []string{"z.md"}) {
		t.Errorf("another tenant's unready: %v %v", paths(got), err)
	}
}
