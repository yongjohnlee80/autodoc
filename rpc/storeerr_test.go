package rpc

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// TestTheIndexVerbsFailAsTheirReads: a read of the index that fails (the store closed under the
// workspace) comes back from each index verb as an error, never as an empty answer. The indexer
// does not run: nothing else touches the store.
func TestTheIndexVerbsFailAsTheirReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.AddWorkspace(ctx, "kb", "/roots/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fsys := memfs.New()
	ws := Static(WorkspaceInfo{Name: "kb", Root: "/roots/kb", Index: index.New(index.NewStore(db, row.ID), fsys, index.WithMatch(md)),
		Docs: docs.New(fsys, md)})
	r := &rig{t: t, sock: shortSocket(t)}
	ln, err := ipc.Listen(r.sock, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- New(Fixed(ws), "v-test", WithListener(ln)).Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
	})
	cli := r.dial(true)
	verbs := []struct {
		verb   string
		params []any
	}{
		{"index.documents", []any{"kb", map[string]any{}}},
		{"index.status", []any{"kb"}},
		{"index.list", []any{"kb", "", int64(10)}},
		{"index.changes", []any{"kb", int64(0), int64(10)}},
		{"index.models", []any{"kb"}},
	}
	for _, c := range verbs { // the instrument: with the store open, each call as made here answers
		call(t, cli, c.verb, c.params...)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range verbs {
		if _, err := cli.Call(context.Background(), c.verb, c.params...); err == nil {
			t.Errorf("%s answered with the store closed", c.verb)
		}
	}
}
