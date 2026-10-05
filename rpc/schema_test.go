package rpc

import (
	"context"
	"net"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// schemaServer serves one workspace, kb, whose files are checked against src.
func schemaServer(t *testing.T, src string) (*golibrpc.Client, *memfs.FS) {
	t.Helper()
	sch, err := schema.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
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
	ix := index.NewIndexer(index.Open(db, row.ID), fsys, index.Options{Match: md, BatchDelay: 5 * time.Millisecond,
		Schema: func() (*schema.Schema, string) { return sch, "fp" }})
	f := follow.New(fsys, ix, ix, follow.Options{Match: md, PollInterval: 20 * time.Millisecond})
	var cores sync.WaitGroup
	cores.Go(func() { _ = ix.Run(ctx) })
	cores.Go(func() { _ = f.Run(ctx) })
	w := &Workspace{Name: "kb", Root: "/roots/kb", Index: ix, Docs: docs.New(fsys, md), Following: f.Status,
		FrontmatterSchema: func() (*schema.Schema, SchemaStatus) {
			return sch, SchemaStatus{Path: ".autodoc/schema.yaml", Active: true, Fields: len(sch.Fields)}
		}}
	sock := shortSocket(t) // a TempDir named after a long test passes macOS's socket-path limit
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Fixed(w), "v-test", WithListener(ln))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		cores.Wait()
	})
	cli, err := golibrpc.Dial(context.Background(), sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": Protocol, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	return cli, fsys
}

func TestSchemaVerbsOverTheWire(t *testing.T) {
	cli, fsys := schemaServer(t, "version: 1\nfrontmatter:\n  type: {type: string, enum: [note, adr], required: true}\n")
	ctx := context.Background()
	for p, content := range map[string]string{"adr.md": "---\ntype: adr\n---\nkestrel\n", "memo.md": "---\ntype: memo\n---\nkestrel\n"} {
		if _, err := fsys.WriteFile(ctx, p, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, "both notes indexed", func() bool {
		st := call(t, cli, "index.status", "kb").(map[string]any)
		return st["docs"] == int64(2) && st["pending_jobs"] == int64(0)
	})
	if st := call(t, cli, "index.status", "kb").(map[string]any); st["diagnosed"] != int64(1) {
		t.Errorf("diagnosed = %v, want 1 (memo.md)", st["diagnosed"])
	}
	ws := call(t, cli, "workspace.list").([]any)[0].(map[string]any)
	if sch, _ := ws["schema"].(map[string]any); sch["path"] != ".autodoc/schema.yaml" || sch["active"] != true || sch["fields"] != int64(1) {
		t.Errorf("workspace.list schema = %v", ws["schema"])
	}

	paths := func(res any) []string {
		var out []string
		for _, h := range res.(map[string]any)["hits"].([]any) {
			out = append(out, h.(map[string]any)["path"].(string))
		}
		return out
	}
	if got := paths(call(t, cli, "search.query", "kb", "kestrel type:adr")); !reflect.DeepEqual(got, []string{"adr.md"}) {
		t.Errorf("type:adr in the query = %v", got)
	}
	if got := paths(call(t, cli, "search.query", "kb", "kestrel", map[string]any{"facets": map[string]any{"type": "adr"}})); !reflect.DeepEqual(got, []string{"adr.md"}) {
		t.Errorf("opts.facets = %v", got)
	}
	if got := paths(call(t, cli, "search.query", "kb", "kestrel", map[string]any{"facets": map[string]any{"type": []any{"note", "adr"}}})); !reflect.DeepEqual(got, []string{"adr.md"}) {
		t.Errorf("opts.facets list = %v", got)
	}
	for name, opts := range map[string]map[string]any{
		"undeclared field": {"facets": map[string]any{"owner": "me"}},
		"not a map":        {"facets": "type:adr"},
		"bad value type":   {"facets": map[string]any{"type": int64(3)}},
	} {
		if _, err := cli.Call(ctx, "search.query", "kb", "kestrel", opts); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("%s: %v, want invalid params", name, err)
		}
	}

	// doc.validate checks unsaved text with the indexer's validator
	res := call(t, cli, "doc.validate", "kb", "draft.md", []byte("---\ntitle: x\ntype: memo\n---\nbody\n")).(map[string]any)
	ds := res["diagnostics"].([]any)
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %v", ds)
	}
	if d := ds[0].(map[string]any); d["field"] != "type" || d["line"] != int64(3) || d["rule"] != schema.RuleEnum || d["message"] == "" {
		t.Errorf("diagnostic = %v", d)
	}
	res = call(t, cli, "doc.validate", "kb", "draft.md", []byte("---\ntype: adr\n---\nbody\n")).(map[string]any)
	if ds := res["diagnostics"].([]any); len(ds) != 0 {
		t.Errorf("a valid note's diagnostics = %v", ds)
	}
	// a YAML document has no frontmatter to check
	res = call(t, cli, "doc.validate", "kb", "conf.yaml", []byte("type: memo\n")).(map[string]any)
	if ds := res["diagnostics"].([]any); len(ds) != 0 {
		t.Errorf("a YAML file's diagnostics = %v", ds)
	}
	if _, err := cli.Call(ctx, "doc.validate", "kb", "x.md"); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("doc.validate without content: %v", err)
	}
	// the fixed server keeps no schemas to set
	if _, err := cli.Call(ctx, "workspace.set_schema", "kb", "s.yaml"); code(err) != CodeUnsupported {
		t.Errorf("workspace.set_schema on a fixed set: %v", err)
	}
}

func TestDocOutlineOverTheWire(t *testing.T) {
	cli, fsys := schemaServer(t, "version: 1\nfrontmatter:\n")
	ctx := context.Background()
	if _, err := fsys.WriteFile(ctx, "g.md", strings.NewReader("---\na: 1\n---\n# Guide\n\n## Setup\n\ntext\n")); err != nil {
		t.Fatal(err)
	}
	res := call(t, cli, "doc.outline", "kb", "g.md").(map[string]any)
	if res["version"] == "" {
		t.Fatal("no version")
	}
	var got []string
	for _, h := range res["headings"].([]any) {
		m := h.(map[string]any)
		got = append(got, m["id"].(string)+"@"+strconv.FormatInt(m["line"].(int64), 10))
	}
	if !reflect.DeepEqual(got, []string{"guide@4", "setup@6"}) {
		t.Errorf("headings = %v", got)
	}
	if _, err := cli.Call(ctx, "doc.outline", "kb", "missing.md"); code(err) != CodeNotFound {
		t.Errorf("a missing note: %v", err)
	}
}
