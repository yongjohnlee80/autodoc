package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/sqlite"
	"github.com/yongjohnlee80/golib/msgpack"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// bigMap is a daemon serving one workspace, big, whose documents and links are written straight
// into the store (no follower or indexer runs, so nothing reconciles them away), grown by grow.
type bigMap struct {
	*rig
	db    *store.Store
	ws    int64
	docs  int
	links map[[2]int]bool // the distinct links seeded, by source and destination
}

func serveBigMap(t *testing.T) *bigMap {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	row, err := db.AddWorkspace(ctx, "big", "/roots/big", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fsys := memfs.New()
	ix := index.New(index.NewStore(db, row.ID), fsys, index.WithMatch(md))
	r := &rig{t: t, fsys: fsys, cancel: cancel, done: make(chan error, 1)}
	r.sock = shortSocket(t)
	ln, err := net.Listen("unix", r.sock)
	if err != nil {
		t.Fatal(err)
	}
	r.srv = New(Fixed(Static(WorkspaceInfo{Name: "big", Root: "/roots/big", Index: ix, Docs: docs.New(fsys, md)})), "v-test", WithListener(ln))
	go func() { r.done <- r.srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
	})
	return &bigMap{rig: r, db: db, ws: row.ID, links: map[[2]int]bool{}}
}

// grow adds documents until there are n: each titled, linked to the next and to one far off, so
// the map holds about two links a document.
func (b *bigMap) grow(n int) {
	b.t.Helper()
	ctx := context.Background()
	raw, err := sqlite.OpenNamed(ctx, "big:"+b.db.Path(), "file:"+b.db.Path()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		b.t.Fatal(err)
	}
	defer raw.Close()
	const base = 1_000_000 // document ids of their own
	for lo := b.docs; lo < n; lo += 400 {
		hi := min(lo+400, n)
		var docs, links []string
		var dargs, largs []any
		for i := lo; i < hi; i++ {
			docs = append(docs, "(?, ?, ?, 'v', 1, 'seed', 0, ?)")
			dargs = append(dargs, base+i, b.ws, fmt.Sprintf("notes/%06d-a-document-of-the-map.md", i), fmt.Sprintf("Document %d of the big map", i))
		}
		if _, err := raw.ExecContext(ctx, "INSERT INTO document(id, workspace_id, path, version, active_gen, indexer, indexed_at, title) VALUES "+
			strings.Join(docs, ", "), dargs...); err != nil {
			b.t.Fatal(err)
		}
		for i := lo; i < hi; i++ {
			for _, to := range []int{(i + 1) % hi, (i * 7919) % hi} { // among the documents written
				if to == i || b.links[[2]int{i, to}] {
					continue
				}
				b.links[[2]int{i, to}] = true
				links = append(links, "(?, ?, 1, 'x', 'x', ?, 'wikilink')")
				largs = append(largs, b.ws, base+i, base+to)
			}
		}
		if _, err := raw.ExecContext(ctx, "INSERT INTO link(workspace_id, src_doc, gen_from, raw, name, dst_doc, kind) VALUES "+
			strings.Join(links, ", "), largs...); err != nil {
			b.t.Fatal(err)
		}
	}
	b.docs = n
}

// mapAt asks for big's unrooted map on a new connection that declared proto, with opts.
func (b *bigMap) mapAt(proto int64, opts map[string]any) (map[string]any, error) {
	cli, _, err := helloAs(b.rig, proto, "map-probe")
	if err != nil {
		b.t.Fatal(err)
	}
	defer cli.Close()
	res, err := cli.Call(context.Background(), "graph.map", "big", opts)
	if err != nil {
		return nil, err
	}
	return res.(map[string]any), nil
}

// TestTheOneReplyMapsLimitAndPagesPastIt (docs/decisions/search.md#unrooted-maps-in-pages): the
// workspace grows until the one-reply map, which a session below protocol 36 still gets, fails; the
// size and the refusal are logged, so the decision's estimate is a measurement. At that size and at
// twice it, a session at 36 reads the same map a page at a time, and every page fits.
func TestTheOneReplyMapsLimitAndPagesPastIt(t *testing.T) {
	if testing.Short() {
		t.Skip("grows a workspace past the transport's limit")
	}
	b := serveBigMap(t)
	failedAt, lastSize, lastN := 0, 0, 0
	for _, n := range []int{2000, 4000, 8000, 10000, 12000, 14000, 16000, 24000, 32000, 48000, 64000} {
		b.grow(n)
		res, err := b.mapAt(mapPagesSince-1, nil)
		if err != nil {
			failedAt = n
			// the reply grows with the map: about lastSize/lastN bytes a document
			t.Logf("[OBSERVED] the one-reply map fails at %d documents (%d links): %v; at about %d bytes a document "+
				"it would be about %d bytes, against the transport's limit of %d", n, len(b.links), err, lastSize/max(lastN, 1),
				lastSize/max(lastN, 1)*n, MaxMessage)
			break
		}
		if got := len(res["nodes"].([]any)); got != n {
			t.Fatalf("%d documents: the one reply holds %d", n, got)
		}
		if got := len(res["edges"].([]any)); got != len(b.links) {
			t.Fatalf("%d documents: the one reply holds %d links, %d seeded", n, got, len(b.links))
		}
		size := encodedSize(t, res)
		t.Logf("the one-reply map of %d documents (%d links) is answered: %d bytes encoded, the transport's limit %d", n, len(b.links), size, MaxMessage)
		lastSize, lastN = size, n
	}
	if failedAt == 0 {
		t.Fatalf("the one-reply map was answered at %d documents: the limit the paging works round is not where the decision puts it", b.docs)
	}
	for _, n := range []int{failedAt, 2 * failedAt} {
		b.grow(n)
		nodes, edges, pages := 0, 0, 0
		opts := map[string]any{"limit": int64(index.MaxMapPage)}
		for {
			res, err := b.mapAt(Protocol, opts)
			if err != nil {
				t.Fatalf("%d documents, page %d: %v", n, pages+1, err)
			}
			pages++
			nodes += len(res["nodes"].([]any))
			edges += len(res["edges"].([]any))
			next, _ := res["next"].(string)
			if next == "" {
				break
			}
			opts["after"] = next
		}
		if nodes != n || edges != len(b.links) {
			t.Errorf("%d documents paged: %d nodes, %d links in %d pages; want %d and %d", n, nodes, edges, pages, n, len(b.links))
		}
		t.Logf("%d documents (%d links): %d pages, each answered", n, len(b.links), pages)
	}
}

// encodedSize is v's size as msgpack, as the transport writes it.
func encodedSize(t *testing.T, v any) int {
	t.Helper()
	b, err := msgpack.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return len(b)
}

// TestOnlyANewSessionGetsPages (docs/decisions/search.md#unrooted-maps-in-pages): a session below
// protocol 36 gets the whole unrooted map in one reply, with no next, and is refused after and
// limit; a session at 36 gets pages, and next. A rooted map is one reply at any protocol, and is
// refused paging; a limit out of bounds, or a cursor from other kinds, is invalid params.
func TestOnlyANewSessionGetsPages(t *testing.T) {
	r := serve(t)
	w := r.dial(true)
	for _, p := range []string{"a.md", "b.md", "c.md"} {
		call(t, w, "doc.write", "kb", p, []byte("# "+p+"\n[[a]]\n"), "")
	}
	eventually(t, "the three documents indexed", func() bool {
		return len(call(t, w, "graph.map", "kb", map[string]any{"limit": int64(10)}).(map[string]any)["nodes"].([]any)) == 3
	})
	old, _, err := helloAs(r, mapPagesSince-1, "an-older-client")
	if err != nil {
		t.Fatal(err)
	}
	one := call(t, old, "graph.map", "kb", nil).(map[string]any)
	if _, has := one["next"]; has || len(one["nodes"].([]any)) != 3 || len(one["edges"].([]any)) != 3 {
		t.Errorf("a session at %d: %v, want the whole map and no next", mapPagesSince-1, one)
	}
	invalidSays := func(cli *golibrpc.Client, opts map[string]any, says string) {
		t.Helper()
		_, err := cli.Call(context.Background(), "graph.map", "kb", opts)
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.Contains(re.Message, says) {
			t.Errorf("graph.map %v: %v, want invalid params saying %q", opts, err, says)
		}
	}
	invalidSays(old, map[string]any{"limit": int64(1)}, fmt.Sprintf("opts.limit needs protocol %d", mapPagesSince))
	invalidSays(old, map[string]any{"after": "m1.x"}, fmt.Sprintf("opts.after needs protocol %d", mapPagesSince))

	first := call(t, w, "graph.map", "kb", map[string]any{"limit": int64(2)}).(map[string]any)
	next, _ := first["next"].(string)
	if len(first["nodes"].([]any)) != 2 || !strings.HasPrefix(next, "m1.") {
		t.Fatalf("a first page at %d: %v", Protocol, first)
	}
	last := call(t, w, "graph.map", "kb", map[string]any{"limit": int64(2), "after": next}).(map[string]any)
	if n, has := last["next"]; len(last["nodes"].([]any)) != 1 || !has || n != "" {
		t.Errorf("the last page: %v, want one document and next \"\"", last)
	}
	whole := call(t, w, "graph.map", "kb", nil).(map[string]any)
	if n, has := whole["next"]; !has || n != "" || len(whole["nodes"].([]any)) != 3 {
		t.Errorf("a map of one page at %d: %v, want next \"\"", Protocol, whole)
	}
	rooted := call(t, w, "graph.map", "kb", map[string]any{"root": "b.md"}).(map[string]any)
	if _, has := rooted["next"]; has {
		t.Errorf("a rooted map says next: %v", rooted)
	}
	invalidSays(w, map[string]any{"root": "b.md", "limit": int64(1)}, "pages a map without a root")
	invalidSays(w, map[string]any{"limit": int64(0)}, fmt.Sprintf("opts.limit must be 1 to %d", index.MaxMapPage))
	invalidSays(w, map[string]any{"limit": int64(index.MaxMapPage + 1)}, "opts.limit must be 1 to")
	invalidSays(w, map[string]any{"after": int64(2)}, "opts.after is the previous page's next")
	invalidSays(w, map[string]any{"after": next, "kinds": []any{"wikilink"}}, "opts.after is not a cursor of this map")
}
