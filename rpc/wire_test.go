package rpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE WIRE GOLDENS: what every verb answers, as the encoder receives it, pinned per domain in
// testdata/wire/<domain>.golden (-update rewrites them). A change to how a result is built (a map
// becoming a struct) must leave them as they are: each key, each value's wire kind, each list's
// order. Values that differ from run to run (versions, cursors, clocks, tokens) keep their kind only.

// wireKind is v's kind as msgpack encodes it.
func wireKind(v any) string {
	switch v.(type) {
	case nil:
		return "nil"
	case bool:
		return "bool"
	case int, int8, int16, int32, int64:
		return "int"
	case uint, uint8, uint16, uint32, uint64:
		return "uint"
	case float32:
		return "float32"
	case float64:
		return "float64"
	case string:
		return "str"
	case []byte:
		return "bin"
	case []any:
		return "array"
	case map[string]any:
		return "map"
	}
	return fmt.Sprintf("unencodable %T", v)
}

// wireDump is v as the encoder sees it, deterministically: map keys sorted, each scalar with its
// kind, and the keys in volatile as their kind alone.
func wireDump(v any, volatile map[string]bool) string {
	var b strings.Builder
	var dump func(v any)
	dump = func(v any) {
		switch x := v.(type) {
		case []any:
			b.WriteString("[")
			for i, e := range x {
				if i > 0 {
					b.WriteString(", ")
				}
				dump(e)
			}
			b.WriteString("]")
		case map[string]any:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			b.WriteString("{")
			for i, k := range keys {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "%s: ", k)
				if volatile[k] {
					fmt.Fprintf(&b, "<%s>", wireKind(x[k]))
					continue
				}
				dump(x[k])
			}
			b.WriteString("}")
		case nil:
			b.WriteString("nil")
		case string:
			fmt.Fprintf(&b, "%q", x)
		case []byte:
			fmt.Fprintf(&b, "bin(%q)", x)
		default:
			fmt.Fprintf(&b, "%s(%v)", wireKind(x), x)
		}
	}
	dump(v)
	return b.String()
}

// asWire is v as the encoder receives it, a map.
func asWire(t *testing.T, v any) map[string]any {
	t.Helper()
	w, err := module.Wire(v)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := w.(map[string]any)
	return m
}

// volatile are the keys whose values differ from run to run.
var volatile = map[string]bool{"version": true, "generation": true, "cursor": true, "oldest_retained": true,
	"indexed_at": true, "at": true, "seq": true, "instance": true, "pid": true, "addr": true, "client": true,
	"store_id": true, "pending_jobs": true, "following": true, "chunks": true, "score": true, "relevance": true,
	"next": true, "more": true}

// wireRec calls verbs in this process, through the handler golib serves, and records what each
// answers.
type wireRec struct {
	t    *testing.T
	srv  *Server
	sess *golibrpc.Session
	out  map[string]*strings.Builder // by domain
}

func newWireRec(t *testing.T, srv *Server) *wireRec {
	sess := &golibrpc.Session{}
	sess.SetValue(sessHello, true)
	sess.SetValue(sessProtocol, Protocol)
	sess.SetValue(sessClient, "wire#00000000")
	return &wireRec{t: t, srv: srv, sess: sess, out: map[string]*strings.Builder{}}
}

// call records verb(params...)'s answer: its result, or its code and message.
func (w *wireRec) call(verb string, params ...any) any {
	w.t.Helper()
	v, ok := w.srv.verbs[verb]
	if !ok {
		w.t.Fatalf("no verb %s", verb)
	}
	out, err := w.srv.chain(v)(context.Background(), &golibrpc.Request{Method: verb, Params: params, Peer: unixPeer, Session: w.sess})
	domain, _, _ := strings.Cut(verb, ".")
	b := w.out[domain]
	if b == nil {
		b = &strings.Builder{}
		w.out[domain] = b
	}
	fmt.Fprintf(b, "%s %s\n", verb, wireDump(params, nil))
	if err != nil {
		code, msg := wireError(err)
		fmt.Fprintf(b, "  error %d %q\n", code, msg)
		return nil
	}
	fmt.Fprintf(b, "  %s\n", wireDump(out, volatile))
	return out
}

// check compares each domain's record with its golden file.
func (w *wireRec) check() {
	w.t.Helper()
	for domain, b := range w.out {
		golden := filepath.Join("testdata", "wire", domain+".golden")
		if *update {
			if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
				w.t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(b.String()), 0o644); err != nil {
				w.t.Fatal(err)
			}
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			w.t.Fatal(err)
		}
		if got := b.String(); got != string(want) {
			w.t.Errorf("%s answers differ from %s:\n%s", domain, golden, lineDiff(string(want), got))
		}
	}
}

// lineDiff is the lines of want and got that differ.
func lineDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := range max(len(wl), len(gl)) {
		var a, b string
		if i < len(wl) {
			a = wl[i]
		}
		if i < len(gl) {
			b = gl[i]
		}
		if a != b {
			out = append(out, fmt.Sprintf("line %d\n  want %s\n   got %s", i+1, a, b))
		}
	}
	return strings.Join(out, "\n")
}

// wireWorkspaces is a set of workspaces that changes as the daemon's does, answering fixed values.
type wireWorkspaces struct{ Workspaces }

func (wireWorkspaces) Add(_ context.Context, c config.Workspace) (Workspace, error) {
	return Static(WorkspaceInfo{Name: c.Name, Root: c.Root, Include: c.Include, Exclude: c.Exclude}), nil
}
func (wireWorkspaces) Rename(context.Context, string, string) error                  { return nil }
func (wireWorkspaces) Remove(context.Context, string) error                          { return nil }
func (wireWorkspaces) SetPatterns(context.Context, string, []string, []string) error { return nil }
func (wireWorkspaces) SetProvider(context.Context, string, string) error             { return nil }
func (wireWorkspaces) SetTextExtensions(_ context.Context, _ string, exts []string) ([]string, error) {
	return append([]string{".log"}, exts...), nil
}
func (wireWorkspaces) SetSchema(_ context.Context, _, path string) (SchemaStatus, error) {
	return SchemaStatus{Path: path, Active: true, Fields: 3, Err: "line 4: bad", Line: 4}, nil
}
func (wireWorkspaces) SetSectionTokens(context.Context, string, int) error      { return nil }
func (wireWorkspaces) Focus(string) error                                       { return nil }
func (wireWorkspaces) SetEmbeddingPolicy(context.Context, string, string) error { return nil }
func (wireWorkspaces) Configure(context.Context, string, store.Changes) error   { return nil }
func (wireWorkspaces) Capabilities() map[string]bool                            { return map[string]bool{"databases": true} }

// wireEmbeddings answers fixed values.
type wireEmbeddings struct{}

func (wireEmbeddings) Providers(context.Context) ([]store.ProviderInfo, string, string, error) {
	return []store.ProviderInfo{{ID: 1, Name: "local", Kind: "ollama", BaseURL: "http://h:11434", Model: "nomic", Context: 2048, HasKey: false}},
		"local", "a last error", nil
}
func (wireEmbeddings) AddProvider(_ context.Context, sp store.ProviderSpec) (store.ProviderInfo, error) {
	return store.ProviderInfo{ID: 2, Name: sp.Name, Kind: sp.Kind, BaseURL: sp.BaseURL, Model: sp.Model, Context: sp.Context, HasKey: sp.Key != nil}, nil
}
func (wireEmbeddings) UpdateProvider(context.Context, string, store.ProviderSpec) error { return nil }
func (wireEmbeddings) RemoveProvider(context.Context, string) error                     { return nil }
func (wireEmbeddings) Use(context.Context, string) error                                { return nil }
func (wireEmbeddings) Models(_ context.Context, name string, sp store.ProviderSpec) ([]string, error) {
	return []string{"m-" + name, "k-" + sp.Kind}, nil
}
func (wireEmbeddings) Usage(context.Context, string, int) ([]store.Usage, error) {
	return []store.Usage{{ProviderID: 1, Day: "2026-10-05", Requests: 3, Texts: 60, Tokens: 900, Failures: 1, Limited: 0}}, nil
}
func (wireEmbeddings) Log(context.Context, string, int) ([]store.LogEntry, error) {
	return []store.LogEntry{{ID: 1, ProviderID: 1, At: 1700000000, Texts: 20, Tokens: 300, Millis: 900, Outcome: "ok"}}, nil
}
func (wireEmbeddings) CancelSwitch(context.Context) (string, error) { return "nomic", nil }
func (wireEmbeddings) ModelContext(context.Context, string, store.ProviderSpec) (int, error) {
	return 8192, nil
}

// wireServer is a daemon over memfs with every option set: workspace kb (followed and indexed,
// with every optional field), a manager of workspaces, preferences, embedding providers, rankers,
// the event log and a local peer's files. Its socket's client has said hello.
func wireServer(t *testing.T) (*Server, *golibrpc.Client, *memfs.FS) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fsys := memfs.New()
	row, err := db.AddWorkspace(ctx, "kb", "/roots/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := index.NewIndexer(index.Open(db, row.ID), fsys, index.Options{Match: md, BatchDelay: 5 * time.Millisecond})
	f := follow.New(fsys, ix, ix, follow.Options{Match: md, PollInterval: 20 * time.Millisecond})
	ix.SetRescanner(f)
	var cores sync.WaitGroup
	cores.Go(func() { _ = ix.Run(ctx) })
	cores.Go(func() { _ = f.Run(ctx) })
	sch, err := schema.Parse([]byte("version: 1\nfrontmatter:\n  type: {type: string, enum: [note, adr], required: true}\n"))
	if err != nil {
		t.Fatal(err)
	}
	kb := Static(WorkspaceInfo{Name: "kb", Root: "/roots/kb", EmbeddingPolicy: store.EmbeddingAlways, SectionTokens: 512,
		Include: []string{"**/*.md"}, Exclude: []string{".git/**"}, Index: ix, Docs: docs.New(fsys, md), Following: f.Status,
		Warming: func() []string { return nil }, EmbeddingQueue: func() (string, string) { return "idle", "" },
		FrontmatterSchema: func() (*schema.Schema, SchemaStatus) {
			return sch, SchemaStatus{Path: "schema.yaml", Active: true, Fields: 1}
		},
		TextExtensions: func() []string { return []string{".log"} }, TextCollisions: func() []string { return nil },
		Databases: func() Databases {
			return Databases{UID: "u1", Destination: "sqlite", VectorIndex: "", ViewArgs: map[string]any{"k": "v"},
				Connections: []store.ConnectionSummary{{Role: store.RoleDestination, Engine: "postgres", Host: "db", Database: "rag", User: "me", HasPassword: true}}}
		},
		Provider: ProviderChoice{Override: "local", Err: "not set up"}})
	broken := Static(WorkspaceInfo{Name: "broken", Root: "/roots/broken", Err: errFixed})
	files := memfs.New()
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(wireWorkspaces{Fixed(kb, broken)}, "v-test", WithListener(ln), WithPreferences(&memPrefs{m: map[string]string{"tui.theme": "retro"}}),
		WithEmbeddings(wireEmbeddings{}), WithRankers(&fakeRankers{}), WithEvents(db), WithFiles(docs.New(files, nil)))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; cores.Wait() })
	r := &rig{t: t, sock: sock}
	return srv, r.dial(true), fsys
}

// TestWireGoldens records every verb's answers against the goldens.
func TestWireGoldens(t *testing.T) {
	srv, cli, fsys := wireServer(t)
	ctx := context.Background()
	w := newWireRec(t, srv)
	for _, f := range [][2]string{
		{"plan.md", "---\ntype: adr\nupdated: 2026-10-05\ntags: [kb]\n---\n# Plan\n\nkestrel [[target]] and [[nowhere]]\n\n## Detail\n\nmore kestrel\n"},
		{"target.md", "---\nupdated: 2026-01-01\n---\n# Target\n\nback to [[plan]]\n"},
	} {
		if _, err := fsys.WriteFile(ctx, f[0], strings.NewReader(f[1])); err != nil {
			t.Fatal(err)
		}
		eventually(t, f[0]+" indexed", func() bool {
			res := call(t, cli, "index.list", "kb", "", int64(10)).(map[string]any)
			for _, d := range res["docs"].([]any) {
				if d.(map[string]any)["path"] == f[0] {
					return true
				}
			}
			return false
		})
	}

	// sys
	w.call("sys.hello", map[string]any{"protocol": Protocol, "name": "wire"})
	w.call("sys.hello", map[string]any{})
	w.call("sys.capabilities")
	w.call("sys.contracts", "graph.resolve")

	// preference
	w.call("preference.list")
	w.call("preference.set", "tui.theme", "light")
	w.call("preference.list")

	// workspace
	w.call("workspace.list")
	w.call("workspace.add", "new", "/roots/new", []any{"**/*.md"}, []any{})
	w.call("workspace.add", "new", "/roots/new")
	w.call("workspace.rename", "kb", "kb2")
	w.call("workspace.remove", "kb")
	w.call("workspace.set_patterns", "kb", []any{"**/*.md"}, []any{})
	w.call("workspace.set_provider", "kb", "")
	w.call("workspace.set_text_extensions", "kb", []any{".txt2"})
	w.call("workspace.set_schema", "kb", "schema.yaml")
	w.call("workspace.section_size", "kb", int64(256))
	w.call("workspace.focus", "kb")
	w.call("workspace.embedding_policy", "kb", "never")
	w.call("workspace.configure", "kb", map[string]any{"name": "kb", "section_tokens": int64(256)})
	w.call("workspace.configure", "kb", map[string]any{"nope": true})

	// search
	w.call("search.query", "kb", "kestrel")
	w.call("search.query", "kb", "kestrel", map[string]any{"limit": int64(1), "stages": []any{"lexical"}})
	w.call("search.query", "kb", "type:adr", map[string]any{"facets": map[string]any{"type": "adr"}, "tags": []any{"kb"}, "paths": []any{"plan.md"}})
	w.call("search.query", "kb", "kestrel", map[string]any{"retrieval": map[string]any{"abstract_chunk": true}, "mode": ""})
	w.call("search.query", "kb", "kestrel", map[string]any{"fuzz": true})
	w.call("search.query", "kb", "kestrel", map[string]any{"limit": "1"})
	w.call("search.query", "kb", "kestrel", "opts")
	w.call("search.query", "nope", "kestrel")
	w.call("search.query", "broken", "kestrel")

	// index
	w.call("index.status", "kb")
	w.call("index.list", "kb", "", int64(10))
	w.call("index.changes", "kb", int64(0), int64(10))
	w.call("index.documents", "kb")
	w.call("index.documents", "kb", map[string]any{"sort": "path", "fields": []any{"type", "tags"}, "diagnosed": false, "limit": int64(1)})
	w.call("index.documents", "kb", map[string]any{"diagnosed": true})
	w.call("index.documents", "kb", map[string]any{"sort": "size"})
	w.call("index.documents", "kb", map[string]any{"after": int64(-1)})
	w.call("index.reindex", "kb", "plan.md")
	w.call("index.models", "kb")
	w.call("index.purge_model", "kb", "nope")

	// graph
	w.call("graph.links", "kb", "plan.md")
	w.call("graph.links", "kb", "plan.md", map[string]any{"kinds": []any{"body"}})
	w.call("graph.links", "kb", "plan.md", "x")
	w.call("graph.backlinks", "kb", "plan.md")
	w.call("graph.neighborhood", "kb", "plan.md", int64(1))
	w.call("graph.neighborhood", "kb", "plan.md", int64(1), map[string]any{})
	w.call("graph.unresolved", "kb")
	w.call("graph.unresolved", "kb", map[string]any{"kinds": "body"})
	w.call("graph.resolve", "kb", "plan.md", "target")
	w.call("graph.resolve", "kb", "plan.md", "nowhere")

	// doc
	w.call("doc.read", "kb", "plan.md")
	w.call("doc.read", "kb", "none.md")
	w.call("doc.read_raw", "kb", "plan.md")
	w.call("doc.outline", "kb", "plan.md")
	w.call("doc.validate", "kb", "plan.md", []byte("---\ntitle: x\n---\n# X\n"))
	w.call("doc.validate", "kb", "plan.md", "---\ntype: adr\n---\n")
	written := w.call("doc.write", "kb", "new.md", []byte("# New\n"), "")
	w.call("doc.write_raw", "kb", "new.md", []byte("# Newer\n"), written.(map[string]any)["version"])
	w.call("doc.rename", "kb", "new.md", "newer.md")
	read := call(t, cli, "doc.read", "kb", "newer.md").(map[string]any)
	w.call("doc.remove", "kb", "newer.md", "stale")
	w.call("doc.remove", "kb", "newer.md", read["version"])

	// file
	fv := w.call("file.write", "/tmp/notes/a.md", "# A\n", "")
	w.call("file.read", "/tmp/notes/a.md")
	w.call("file.read_raw", "/tmp/notes/a.md")
	w.call("file.write_raw", "/tmp/notes/a.md", []byte("# A2\n"), fv.(map[string]any)["version"])
	w.call("file.read", "notes/a.md")
	w.call("file.locate", "/roots/kb/plan.md")
	w.call("file.locate", "/elsewhere/plan.md")

	// embedding
	w.call("embedding.providers")
	w.call("embedding.add", map[string]any{"name": "cloud", "kind": "openai", "base_url": "https://api", "model": "te3", "context": int64(8192), "key": "sk"})
	w.call("embedding.add", "cloud")
	w.call("embedding.update", "cloud", map[string]any{"model": "te3-large"})
	w.call("embedding.remove", "cloud")
	w.call("embedding.use", "local")
	w.call("embedding.cancel_switch")
	w.call("embedding.models", "local")
	w.call("embedding.models", map[string]any{"kind": "ollama", "base_url": "http://h"})
	w.call("embedding.models", "local", map[string]any{"kind": "ollama"})
	w.call("embedding.models", map[string]any{"kind": "ollama"}, map[string]any{})
	w.call("embedding.model_context", "local", map[string]any{"kind": "ollama"})
	w.call("embedding.usage", "local", int64(7))
	w.call("embedding.log", "local", int64(5))

	// ranker
	w.call("ranker.list")
	w.call("ranker.add", map[string]any{"name": "c", "kind": "rerank-api", "base_url": "https://api", "model": "r3", "key": "sk"})
	w.call("ranker.update", "c", map[string]any{"model": "r4"})
	w.call("ranker.remove", "c")
	w.call("ranker.use", "tei")
	w.call("ranker.window", int64(30))
	w.call("ranker.models", "tei")
	w.call("ranker.models", map[string]any{"kind": "tei", "base_url": "http://h"})
	w.call("ranker.usage", "tei", int64(7))
	w.call("ranker.log", "tei", int64(5))

	// the events all of it logged
	w.call("sys.events", int64(0), int64(100))
	w.check()
}
