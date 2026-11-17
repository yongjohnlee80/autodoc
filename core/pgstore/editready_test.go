package pgstore

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// rewrite puts the document at path again with texts and status, as an edit of its row does, and
// marks nothing ready.
func rewrite(t *testing.T, s *Store, path, status string, texts ...string) {
	t.Helper()
	var chunks []search.Chunk
	for i, body := range texts {
		chunks = append(chunks, search.Chunk{Ord: i, Breadcrumb: path, Body: body, Embed: body, ByteEnd: len(body)})
	}
	if _, err := s.Put(context.Background(), Document{Path: path, Chunks: chunks,
		Facets: map[string][]string{"label_id": {"A"}, "status": {status}}}); err != nil {
		t.Fatal(err)
	}
}

// readyOf says whether a search by meaning finds the document at path by text's vector, and
// whether the store has it ready.
func readyOf(t *testing.T, s *Store, path, text string) (found, ready bool) {
	t.Helper()
	for _, c := range semantic(t, s, model, searchtest.Embed(text), search.Filter{}, 50) {
		found = found || c.Path == path
	}
	unready, _, err := s.Unready(context.Background(), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	return found, !slices.ContainsFunc(unready, func(d DocInfo) bool { return d.Path == path })
}

// An edited document whose every text already has a vector under the active model stays ready,
// and a search by meaning keeps finding it: a change of its facets alone, or a text it takes from
// another document. One with a text no vector of the active model has goes unready until the
// worker embeds it (a vector under another model is not one); so does every edit while a switch
// fills another model; and a first write waits for the worker as before. The mutants, named here:
// the readiness not computed at an edit (today's behaviour: every edit leaves semantic search);
// the model dropped from the vector's match; the switch's guard dropped.
func TestAnEditedDocumentWhoseTextsAreEmbeddedStaysReady(t *testing.T) {
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	at := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	sourced(t, s, "x.md", "x", at, "a midnight drive", "a long coda")
	sourced(t, s, "y.md", "y", at, "a chorus of strings")
	if found, ready := readyOf(t, s, "x.md", "a midnight drive"); !found || !ready {
		t.Fatalf("x before any edit: found %v, ready %v", found, ready)
	}

	rewrite(t, s, "x.md", "released", "a midnight drive", "a long coda")
	if found, ready := readyOf(t, s, "x.md", "a midnight drive"); !found || !ready {
		t.Errorf("x after an edit of its facets alone: found %v, ready %v; want it still found", found, ready)
	}
	rewrite(t, s, "x.md", "released", "a midnight drive", "a chorus of strings")
	if found, ready := readyOf(t, s, "x.md", "a chorus of strings"); !found || !ready {
		t.Errorf("x after taking y's text, which has a vector: found %v, ready %v; want it found", found, ready)
	}

	// "a new bridge" has a vector under the other model only
	other := search.Chunk{Body: "a new bridge", Embed: "a new bridge"}.TextHash()
	if err := s.Embed(context.Background(), "other|model||64", []Embedding{{TextHash: other, Vec: searchtest.Embed("a new bridge")}}); err != nil {
		t.Fatal(err)
	}
	rewrite(t, s, "x.md", "released", "a midnight drive", "a new bridge")
	if found, ready := readyOf(t, s, "x.md", "a midnight drive"); found || ready {
		t.Errorf("x with a text the active model has no vector of: found %v, ready %v; want it unready", found, ready)
	}
	if ready, err := s.MarkReady(context.Background(), "x.md", model); err != nil || ready {
		t.Fatalf("MarkReady of x unembedded: %v, %v", ready, err)
	}

	if err := s.SetModels(context.Background(), model, "other|model||64"); err != nil {
		t.Fatal(err)
	}
	rewrite(t, s, "x.md", "draft", "a midnight drive")
	if _, ready := readyOf(t, s, "x.md", "a midnight drive"); ready {
		t.Error("x edited while a switch fills another model: ready; want it left to the worker")
	}
	if err := s.SetModels(context.Background(), model, ""); err != nil {
		t.Fatal(err)
	}

	rewrite(t, s, "z.md", "draft", "a midnight drive")
	if _, ready := readyOf(t, s, "z.md", "a midnight drive"); ready {
		t.Error("z's first write: ready; want the worker to mark a first write")
	}
}
