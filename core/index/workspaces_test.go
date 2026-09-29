package index

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// served is one workspace of a shared store, indexing its notes.
type served struct {
	name string
	ix   *Indexer
}

// shareAStore opens one store and serves a workspace in it for each entry of notes (a name, then
// path/text pairs), each with its options, until the test ends; it returns once every one is
// indexed, and semantic-ready when it has a provider.
func shareAStore(t *testing.T, opts []Options, notes ...[]string) []served {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	var out []served
	stops := make(chan struct{}, len(notes))
	t.Cleanup(func() {
		cancel()
		for range out {
			<-stops
		}
		_ = db.Close()
	})
	for i, ns := range notes {
		fsys := memfs.New()
		for j := 1; j+1 < len(ns); j += 2 {
			if _, err := fsys.WriteFile(ctx, ns[j], strings.NewReader(ns[j+1])); err != nil {
				t.Fatal(err)
			}
		}
		row, err := db.AddWorkspace(ctx, ns[0], "/"+ns[0], nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		o := opts[i]
		o.Match = testMatch
		ix := NewIndexer(Open(db, row.ID), fsys, o)
		go func() { _ = ix.Run(ctx); stops <- struct{}{} }()
		for j := 1; j+1 < len(ns); j += 2 {
			ix.Touch(ns[j])
		}
		out = append(out, served{ns[0], ix})
	}
	for _, w := range out {
		want := int64(len(notesOf(notes, w.name)) / 2)
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			st, err := w.ix.Status(ctx)
			if err == nil && st.Docs == want && st.PendingJobs == 0 &&
				(st.Embeddings == nil || (st.Embeddings.Pending == 0 && st.Embeddings.Semantic == SemanticReady)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("workspace %s did not index: %+v, %v", w.name, st, err)
			}
		}
	}
	return out
}

func notesOf(notes [][]string, name string) []string {
	for _, ns := range notes {
		if ns[0] == name {
			return ns[1:]
		}
	}
	return nil
}

func search(t *testing.T, w served, q string, opts QueryOpts) []Hit {
	t.Helper()
	res, err := w.ix.Search(context.Background(), q, opts)
	if err != nil {
		t.Fatalf("%s: search %q: %v", w.name, q, err)
	}
	return res.Hits
}

// TestLimitIsTheWorkspacesBest: the workspace is filtered in the query, before the rank and the
// LIMIT: with the other workspace's notes ranking higher store-wide, LIMIT 1 is still this
// workspace's best, and never another's.
func TestLimitIsTheWorkspacesBest(t *testing.T) {
	loud := "# Osprey osprey\n\nosprey osprey osprey osprey\n"
	ws := shareAStore(t, []Options{{}, {}},
		[]string{"a", "quiet.md", "# Coast\n\nA note of the tide, the marsh and, once, an osprey among the gulls and terns.\n"},
		[]string{"b", "loud1.md", loud, "loud2.md", loud, "loud3.md", loud})
	hits := search(t, ws[0], "osprey", QueryOpts{Limit: 1, Mode: ModeLexical})
	if len(hits) != 1 || hits[0].Path != "quiet.md" {
		t.Fatalf("a's LIMIT 1 for osprey = %+v, want its quiet.md", hits)
	}
	if hits := search(t, ws[1], "osprey", QueryOpts{Limit: 1, Mode: ModeLexical}); len(hits) != 1 || hits[0].Path == "quiet.md" {
		t.Fatalf("b's LIMIT 1 for osprey = %+v, want one of its own", hits)
	}
}

// TestSemanticReadsTheWorkspacesVectors: the same text in two workspaces has a vector in each,
// under one model fingerprint, and here they differ. A's semantic search scores A's note by A's
// vector alone: one hit, the one its own vector ranks, as if B were not in the store.
func TestSemanticReadsTheWorkspacesVectors(t *testing.T) {
	pa, pb := newFake("m", "a"), newFake("m", "a")
	pb.seed = "b" // the same model to the store, other vectors
	text := "zebra\n"
	ws := shareAStore(t, []Options{{Provider: pa}, {Provider: pb}},
		[]string{"a", "n.md", text}, []string{"b", "n.md", text})
	alone := shareAStore(t, []Options{{Provider: newFake("m", "a")}}, []string{"a", "n.md", text})
	// through the code snapshot, once the one at the head answers
	bySnapshot := func(w served) []Hit {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			n := w.ix.sem.snapshotScans.Load()
			hits := search(t, w, "zebra", QueryOpts{Mode: ModeSemantic})
			if w.ix.sem.snapshotScans.Load() == n+1 {
				return hits
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never answered from its snapshot", w.name)
			}
		}
	}
	// through SQL: a snapshot a commit behind is not used
	bySQL := func(w served) []Hit {
		t.Helper()
		cur := w.ix.sem.snap.Load()
		w.ix.sem.snap.Store(&codeSnap{fp: cur.fp, watermark: cur.watermark - 1, docs: cur.docs})
		n := w.ix.sem.fallbackScans.Load()
		hits := search(t, w, "zebra", QueryOpts{Mode: ModeSemantic})
		if w.ix.sem.fallbackScans.Load() != n+1 {
			t.Fatalf("%s did not answer through SQL", w.name)
		}
		return hits
	}
	// in this order: the SQL path leaves a stale snapshot behind it
	for _, c := range []struct {
		path  string
		query func(served) []Hit
	}{{"snapshot", bySnapshot}, {"sql", bySQL}} {
		got, want := c.query(ws[0]), c.query(alone[0])
		if len(got) != 1 || len(want) != 1 || got[0].Path != "n.md" || got[0].Score != want[0].Score {
			t.Errorf("%s: a's semantic hits %+v, want one, scored as a alone scores it: %+v", c.path, got, want)
		}
	}
}
