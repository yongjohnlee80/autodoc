package rpc

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestTheAPIsWritesAreIndexedAtOnce: a file written, renamed or removed through the API reaches the
// index with no follower at all — on a large root a watch or a poll can be seconds behind, and a
// note saved in a client should be searchable when the save returns, not after.
func TestTheAPIsWritesAreIndexedAtOnce(t *testing.T) {
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
	fsys := memfs.New()
	ix := index.NewIndexer(index.Open(db, row.ID), fsys, index.Options{Match: md, BatchDelay: 5 * time.Millisecond})
	ixDone := make(chan struct{})
	go func() { _ = ix.Run(ctx); close(ixDone) }() // no follower: nothing else tells the indexer
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
		<-ixDone // before the store closes
	})
	cli, err := golibrpc.Dial(ctx, sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": Protocol, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	docsNow := func() int64 { return call(t, cli, "index.status", "kb").(map[string]any)["docs"].(int64) }
	found := func(word string) bool {
		hits := call(t, cli, "search.query", "kb", word, map[string]any{"limit": int64(5)}).(map[string]any)["hits"].([]any)
		return len(hits) > 0
	}

	v := call(t, cli, "doc.write", "kb", "plan.md", []byte("# Plan\n\nkestrel\n"), "").(map[string]any)["version"].(string)
	eventually(t, "the written note indexed", func() bool { return docsNow() == 1 && found("kestrel") })
	call(t, cli, "doc.write", "kb", "plan.md", []byte("# Plan\n\nosprey\n"), v)
	eventually(t, "the rewrite indexed", func() bool { return found("osprey") && !found("kestrel") })
	call(t, cli, "doc.rename", "kb", "plan.md", "moved.md")
	eventually(t, "the rename indexed", func() bool {
		hits := call(t, cli, "search.query", "kb", "osprey", map[string]any{"limit": int64(5)}).(map[string]any)["hits"].([]any)
		return docsNow() == 1 && len(hits) == 1 && hits[0].(map[string]any)["path"] == "moved.md"
	})
	st, err := fsys.Stat(ctx, "moved.md")
	if err != nil {
		t.Fatal(err)
	}
	call(t, cli, "doc.remove", "kb", "moved.md", string(st.Version))
	eventually(t, "the removal indexed", func() bool { return docsNow() == 0 })
}
