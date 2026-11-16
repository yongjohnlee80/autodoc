//go:build server

package index

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// A SQLite store indexed in two workspaces, copied into PostgreSQL, searches the same there: each
// workspace's search finds its own chunks alone, the same paths and breadcrumbs as on SQLite, the
// copied chunks matched by the tsvector PostgreSQL generated as they landed.
func TestACopiedStoreSearchesEachWorkspaceAsBefore(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, options{})
	w, err := e.db.AddWorkspace(ctx, "other", "/other", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fsB := memfs.New()
	other := NewStore(e.db, w.ID)
	ixB := newIndexer(other, fsB, options{BatchDelay: 20 * time.Millisecond, Match: testMatch})
	runB, stopB := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ixB.Run(runB) }()
	stoppedB := false
	stop := func() {
		if !stoppedB {
			stoppedB = true
			stopB()
			<-done
		}
	}
	t.Cleanup(stop) // before the env's own cleanup closes the store

	e.put("same.md", "# Alpha\n\nkestrel in the first workspace\n", "more.md", "# Gamma\n\nanother kestrel here\n")
	for _, f := range []struct{ path, body string }{
		{"same.md", "# Beta\n\nkestrel in the second workspace\n"},
		{"lone.md", "# Delta\n\nonly the second has heron\n"},
	} {
		if _, err := fsB.WriteFile(ctx, f.path, strings.NewReader(f.body)); err != nil {
			t.Fatal(err)
		}
		ixB.Touch(f.path)
	}
	deadline := time.Now().Add(20 * time.Second)
	for _, p := range []string{"same.md", "lone.md"} {
		for {
			_, ok, err := other.Version(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			if ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the second workspace's %s was never indexed", p)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	hits := func(s *Store, q string) []string {
		t.Helper()
		res, err := s.Search(ctx, q, QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, h := range res.Hits {
			out = append(out, h.Path+"#"+h.Breadcrumb)
		}
		return sortedCopy(out)
	}
	queries := []string{"kestrel", "heron", "workspace"}
	before := map[string][]string{}
	for _, q := range queries {
		before["a:"+q], before["b:"+q] = hits(e.store, q), hits(other, q)
	}
	if want := []string{"more.md#Gamma", "same.md#Alpha"}; !reflect.DeepEqual(before["a:kestrel"], want) {
		t.Fatalf("on SQLite, the first workspace's kestrel: %v, want %v", before["a:kestrel"], want)
	}
	// the copy reads one snapshot of the store, which stays open (the env closes it as it stops its
	// indexer, idle now)
	stop()

	loc := store.Location{Engine: store.EngineOfPostgres, DSN: pgtest.ScratchURL(t), Schema: "autodoc_store",
		KeyFile: filepath.Join(t.TempDir(), "store.key")}
	pg, err := store.OpenAt(ctx, loc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	if _, err := store.CopySQLite(ctx, pg, e.db); err != nil {
		t.Fatal(err)
	}
	a, b := NewStore(pg, e.ws), NewStore(pg, w.ID)
	for _, q := range queries {
		if got := hits(a, q); !reflect.DeepEqual(got, before["a:"+q]) {
			t.Errorf("the copy's first workspace, %q: %v, on SQLite %v", q, got, before["a:"+q])
		}
		if got := hits(b, q); !reflect.DeepEqual(got, before["b:"+q]) {
			t.Errorf("the copy's second workspace, %q: %v, on SQLite %v", q, got, before["b:"+q])
		}
	}
}
