package rpc

import (
	"context"
	"net"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// fakeSearcher answers every query with one hit of its own, and records what it was asked.
type fakeSearcher struct {
	mu    sync.Mutex
	asked []search.Query
}

func (f *fakeSearcher) Search(ctx context.Context, q search.Query) (search.Result, error) {
	f.mu.Lock()
	f.asked = append(f.asked, q)
	f.mu.Unlock()
	return search.Result{ModeUsed: search.ModeHybrid, Semantic: search.StateReady, Hits: []search.Hit{{Path: "from-the-fake.md",
		Breadcrumb: "Fake", Snippet: "an answer of its own", Score: 0.5, Relevance: 0.25, Via: []string{"fake"}}}}, nil
}

// TestASearcherSwappedInAnswersSearchQuery: the engine behind search.query is whatever
// index.Options.NewSearcher builds; a searcher of the caller's own answers the API, and receives
// the query as the client sent it.
func TestASearcherSwappedInAnswersSearchQuery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	row, err := db.AddWorkspace(ctx, "kb", "/roots/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeSearcher{}
	fsys := memfs.New()
	ix := index.NewIndexer(index.Open(db, row.ID), fsys, index.Options{Match: md, BatchDelay: 5 * time.Millisecond,
		NewSearcher: func(search.Store[int64, *index.View], search.QueryEmbedder) search.Searcher { return fake }})
	ixDone := make(chan struct{})
	go func() { _ = ix.Run(ctx); close(ixDone) }()
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Fixed(&Workspace{Name: "kb", Root: "/roots/kb", Index: ix, Docs: docs.New(fsys, md)}), "v-test", WithListener(ln))
	srvDone := make(chan error, 1)
	go func() { srvDone <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-srvDone
		<-ixDone
	})
	cli, err := golibrpc.Dial(ctx, sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": Protocol, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	res := call(t, cli, "search.query", "kb", "kestrel nests", map[string]any{"limit": int64(7), "mode": "lexical",
		"tags": []any{"birds"}}).(map[string]any)
	hits := res["hits"].([]any)
	if len(hits) != 1 || res["mode_used"] != "hybrid" || res["semantic"] != "ready" {
		t.Fatalf("search.query = %+v; want the fake's one hit", res)
	}
	if h := hits[0].(map[string]any); h["path"] != "from-the-fake.md" || h["snippet"] != "an answer of its own" || h["score"] != 0.5 {
		t.Errorf("the hit = %+v", h)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.asked) != 1 {
		t.Fatalf("the fake was asked %d times", len(fake.asked))
	}
	q := fake.asked[0]
	if q.Text != "kestrel nests" || q.Mode != search.ModeLexical || q.Limit != 7 || !reflect.DeepEqual(q.Filter.Tags, []string{"birds"}) {
		t.Errorf("the fake was asked %+v", q)
	}
}
