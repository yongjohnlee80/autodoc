package rpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search/rank"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestSearchStagesRefusals: stages with neither retriever, a name that is no stage, a stage twice,
// stages beside a mode, and stages that are not a list of names are InvalidParams, each with its
// constant message.
func TestSearchStagesRefusals(t *testing.T) {
	cli := serve(t).dial(true)
	for _, c := range []struct {
		name string
		opts map[string]any
		msg  string
	}{
		{"no stage", map[string]any{"stages": []any{}}, "search stages must include lexical or semantic: a ranker only re-orders what they find"},
		{"the ranker alone", map[string]any{"stages": []any{"rerank"}}, "search stages must include lexical or semantic: a ranker only re-orders what they find"},
		{"a name that is no stage", map[string]any{"stages": []any{"lexical", "fuzzy"}}, "unknown search stage: the stages are lexical, semantic and rerank"},
		{"a stage twice", map[string]any{"stages": []any{"lexical", "rerank", "lexical"}}, "a search stage is named twice"},
		{"stages and a mode", map[string]any{"stages": []any{"lexical"}, "mode": "lexical"}, "search stages and a search mode together: send one or the other"},
		{"stages and an empty mode", map[string]any{"stages": []any{"lexical"}, "mode": ""}, "search stages and a search mode together: send one or the other"},
		{"stages not a list", map[string]any{"stages": "lexical"}, "search.query: opts.stages must be a list of strings"},
		{"a stage not a name", map[string]any{"stages": []any{int64(1)}}, "search.query: opts.stages must be a list of strings"},
	} {
		_, err := cli.Call(context.Background(), "search.query", "kb", "kestrel", c.opts)
		if code, msg := wireError(err); code != golibrpc.CodeInvalidParams || msg != c.msg {
			t.Errorf("%s: %d %q, want InvalidParams %q", c.name, code, msg, c.msg)
		}
	}
}

// TestSearchStagesReport: the answer says how it was made: the stages asked for (a mode as the
// stages it stands for, auto as all three), those that ran, and why the others did not; and
// meaning alone fails where it cannot answer, never answered by words.
func TestSearchStagesReport(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	call(t, cli, "doc.write", "kb", "a.md", []byte("# A\n\nkestrel\n"), "")
	eventually(t, "a.md indexed", func() bool {
		return len(call(t, cli, "search.query", "kb", "kestrel").(map[string]any)["hits"].([]any)) == 1
	})
	for _, c := range []struct {
		name string
		opts map[string]any
		want map[string]any
	}{
		{"auto", nil, map[string]any{"requested": []any{"lexical", "semantic", "rerank"}, "performed": []any{"lexical"},
			"skipped": map[string]any{"semantic": "no embedding model is in use", "rerank": "no ranker is in use"}}},
		{"mode auto", map[string]any{"mode": "auto"}, map[string]any{"requested": []any{"lexical", "semantic", "rerank"}, "performed": []any{"lexical"},
			"skipped": map[string]any{"semantic": "no embedding model is in use", "rerank": "no ranker is in use"}}},
		{"mode lexical", map[string]any{"mode": "lexical"}, map[string]any{"requested": []any{"lexical", "rerank"}, "performed": []any{"lexical"},
			"skipped": map[string]any{"rerank": "no ranker is in use"}}},
		{"lexical", map[string]any{"stages": []any{"lexical"}}, map[string]any{"requested": []any{"lexical"}, "performed": []any{"lexical"},
			"skipped": map[string]any{}}},
		{"both retrievers", map[string]any{"stages": []any{"semantic", "lexical"}}, map[string]any{"requested": []any{"lexical", "semantic"},
			"performed": []any{"lexical"}, "skipped": map[string]any{"semantic": "no embedding model is in use"}}},
	} {
		params := []any{"kb", "kestrel"}
		if c.opts != nil {
			params = append(params, c.opts)
		}
		res := call(t, cli, "search.query", params...).(map[string]any)
		if !reflect.DeepEqual(res["stages"], c.want) || res["mode_used"] != "lexical" || len(res["hits"].([]any)) != 1 {
			t.Errorf("%s: stages %v, %s, %d hits; want %v", c.name, res["stages"], res["mode_used"], len(res["hits"].([]any)), c.want)
		}
	}
	for _, opts := range []map[string]any{{"stages": []any{"semantic"}}, {"stages": []any{"semantic", "rerank"}}, {"mode": "semantic"}} {
		if _, err := cli.Call(context.Background(), "search.query", "kb", "kestrel", opts); code(err) != CodeUnsupported {
			t.Errorf("%v with no model: %v, want CodeUnsupported, never by words", opts, err)
		}
	}
}

// countingRanker scores the longer text higher, and counts its calls.
type countingRanker struct {
	mu    sync.Mutex
	calls int
}

func (c *countingRanker) Rank(_ context.Context, _ string, texts []string) ([]float64, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	out := make([]float64, len(texts))
	for i, x := range texts {
		out[i] = float64(len(x))
	}
	return out, nil
}

func (*countingRanker) Model() rank.Model {
	return rank.Model{Provider: "test", Name: "acme/counter", MaxBatch: 100}
}

func (c *countingRanker) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// rankedServer serves a workspace "kb" whose searches can go through ranker.
func rankedServer(t *testing.T, ranker rank.Ranker) *golibrpc.Client {
	t.Helper()
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
	h := &rank.Holder{}
	h.Set(ranker, rank.DefaultWindow)
	fsys := memfs.New()
	ix := index.NewIndexer(index.Open(db, row.ID), fsys, index.Options{Match: md, BatchDelay: 5 * time.Millisecond,
		Rank: index.Rank{Source: h}})
	ixDone := make(chan struct{})
	go func() { _ = ix.Run(ctx); close(ixDone) }()
	dir, err := os.MkdirTemp("", "ads")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
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
	return cli
}

// TestRerankRunsOnlyWhenAsked: with a ranker in use, a search that leaves rerank out never calls
// it, and its answer is in recall order, rank off; one that asks for it, by stages or by a mode,
// calls it once, and says so.
func TestRerankRunsOnlyWhenAsked(t *testing.T) {
	ranker := &countingRanker{}
	cli := rankedServer(t, ranker)
	call(t, cli, "doc.write", "kb", "short.md", []byte("# Short\n\nkestrel\n"), "")
	call(t, cli, "doc.write", "kb", "long.md", []byte("# Long\n\nkestrel among many other words in a longer chunk of text\n"), "")
	search := func(opts map[string]any) map[string]any {
		t.Helper()
		return call(t, cli, "search.query", "kb", "kestrel", opts).(map[string]any)
	}
	eventually(t, "both indexed", func() bool { return len(search(map[string]any{"stages": []any{"lexical"}})["hits"].([]any)) == 2 })

	before := ranker.count()
	res := search(map[string]any{"stages": []any{"lexical"}})
	if ranker.count() != before {
		t.Errorf("rerank not asked: the ranker was called %d times", ranker.count()-before)
	}
	first := res["hits"].([]any)[0].(map[string]any)
	if _, scored := first["rank_score"]; scored || first["path"] != "short.md" ||
		!reflect.DeepEqual(res["rank"], map[string]any{"state": "off", "model": "", "error": ""}) ||
		!reflect.DeepEqual(res["stages"].(map[string]any)["performed"], []any{"lexical"}) {
		t.Errorf("rerank not asked: first %v, rank %v, stages %v; want recall order, rank off", first, res["rank"], res["stages"])
	}

	for _, opts := range []map[string]any{{"stages": []any{"lexical", "rerank"}}, {"mode": "lexical"}, {}} {
		before := ranker.count()
		res := search(opts)
		first := res["hits"].([]any)[0].(map[string]any)
		if ranker.count() != before+1 || first["path"] != "long.md" || res["rank"].(map[string]any)["state"] != "ready" ||
			!reflect.DeepEqual(res["stages"].(map[string]any)["performed"], []any{"lexical", "rerank"}) {
			t.Errorf("%v: %d calls, first %v, rank %v, stages %v; want one call and the ranker's order", opts,
				ranker.count()-before, first["path"], res["rank"], res["stages"])
		}
	}
}
