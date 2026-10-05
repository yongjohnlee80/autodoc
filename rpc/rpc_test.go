package rpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

func md(p string) bool { return strings.HasSuffix(p, ".md") }

// committing is memfs whose conditional writes land, then report a failure after the commit point.
type committing struct{ *memfs.FS }

func (c committing) WriteFileIf(ctx context.Context, name string, r io.Reader, want vfs.Version, opts ...vfs.WriteOption) (vfs.FileInfo, error) {
	fi, err := c.FS.WriteFileIf(ctx, name, r, want, opts...)
	if err != nil {
		return fi, err
	}
	return vfs.FileInfo{}, &vfs.CommitError{Path: name, Info: fi, Err: errors.New("fsync /secret/path: input/output error")}
}

type rig struct {
	t      *testing.T
	sock   string
	srv    *Server
	fsys   *memfs.FS
	notes  atomic.Int64 // notifications any client received
	cancel context.CancelFunc
	done   chan error
	cores  sync.WaitGroup // the indexers and followers, stopped before the store closes
}

// serve runs a daemon's worth of core over memfs, in one store: workspace kb (followed and
// indexed), flaky (its writes land and then fail), and broken (its root could not be opened).
func serve(t *testing.T) *rig {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r := &rig{t: t, fsys: memfs.New(), cancel: cancel, done: make(chan error, 1)}
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	open := func(name string, fsys vfs.FS) *Workspace {
		row, err := db.AddWorkspace(ctx, name, "/roots/"+name, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		ix := index.NewIndexer(index.Open(db, row.ID), fsys, index.Options{Match: md, BatchDelay: 5 * time.Millisecond})
		f := follow.New(fsys, ix, ix, follow.Options{Match: md, PollInterval: 20 * time.Millisecond})
		ix.SetRescanner(f)
		r.cores.Go(func() { _ = ix.Run(ctx) })
		r.cores.Go(func() { _ = f.Run(ctx) })
		return &Workspace{Name: name, Root: "/roots/" + name, Index: ix, Docs: docs.New(fsys, md), Following: f.Status}
	}
	ws := []*Workspace{open("kb", r.fsys), open("flaky", committing{memfs.New()}),
		{Name: "broken", Root: "/roots/broken", Err: fmt.Errorf("workspace %q: %w", "broken", workspace.ErrNotADirectory)}}
	r.sock = shortSocket(t) // macOS bounds a socket's path at 104 bytes; a test's TempDir can pass it
	ln, err := net.Listen("unix", r.sock)
	if err != nil {
		t.Fatal(err)
	}
	r.srv = New(Fixed(ws...), "v-test", WithListener(ln))
	go func() { r.done <- r.srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
		r.cores.Wait() // before the store closes (its cleanup runs after this one)
	})
	return r
}

// dial connects, request/response only, as auto-core's client is; hello says hello unless it is
// false.
func (r *rig) dial(hello bool) *golibrpc.Client {
	r.t.Helper()
	cli, err := golibrpc.Dial(context.Background(), r.sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"),
		golibrpc.OnNotification(func(string, []any) { r.notes.Add(1) }))
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { _ = cli.Close() })
	if hello {
		if _, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": Protocol, "name": "test"}); err != nil {
			r.t.Fatal(err)
		}
	}
	return cli
}

func call(t *testing.T, cli *golibrpc.Client, method string, params ...any) any {
	t.Helper()
	res, err := cli.Call(context.Background(), method, params...)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	return res
}

func code(err error) int64 {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// eventually polls cond for up to 10 s.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestHandshakeGate: before a compatible sys.hello every verb is HandshakeRequired; a probe hello
// admits nothing; a mismatched hello refuses the session for good.
func TestHandshakeGate(t *testing.T) {
	r := serve(t)
	cli := r.dial(false)
	for _, v := range r.srv.Verbs() {
		if v == "sys.hello" {
			continue
		}
		if _, err := cli.Call(context.Background(), v); code(err) != CodeHandshakeRequired {
			t.Errorf("%s before hello: %v", v, err)
		}
	}
	res := call(t, cli, "sys.hello", map[string]any{})
	if m := res.(map[string]any); m["server"] != ServerName || m["protocol"] != Protocol || m["version"] != "v-test" {
		t.Errorf("probe answer %v", m)
	}
	if _, err := cli.Call(context.Background(), "workspace.list"); code(err) != CodeHandshakeRequired {
		t.Errorf("after a probe hello: %v", err)
	}
	bad := r.dial(false)
	if _, err := bad.Call(context.Background(), "sys.hello", map[string]any{"protocol": Protocol + 1}); code(err) != CodeProtocolMismatch {
		t.Fatalf("a mismatched hello: %v", err)
	}
	for _, v := range []string{"sys.hello", "workspace.list"} {
		var p []any
		if v == "sys.hello" {
			p = []any{map[string]any{"protocol": Protocol}}
		}
		if _, err := bad.Call(context.Background(), v, p...); code(err) != CodeProtocolMismatch {
			t.Errorf("%s on a refused session: %v", v, err)
		}
	}
	if _, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": "one"}); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("a non-integer protocol: %v", err)
	}
}

func TestDuplicateVerbPanics(t *testing.T) {
	s := New(nil, "v")
	defer func() {
		if recover() == nil {
			t.Error("a second registration of a verb did not panic")
		}
	}()
	s.handle("sys.hello", s.hello)
}

// TestVerbsArePinned: the verb surface is Protocol 13's (12's, with index.documents); 12's was 11's, with search.query's stages; 10 had no ranker verbs; 10's was 9's; 8 had no workspace.configure or sys.capabilities; 7 had no workspace.set_schema, workspace.set_text_extensions, workspace.set_provider, doc.validate, doc.outline or sys.events; 6 had no workspace.set_patterns; 5 had no workspace.focus; 4 had no workspace.section_size or embedding.model_context; 3 had no embedding.cancel_switch or
// index.models; 2 had no preference or embedding verbs; 1 had no workspace.add, rename or remove).
// Changing it means bumping Protocol and this list together.
func TestVerbsArePinned(t *testing.T) {
	want := []string{"doc.outline", "doc.read", "doc.remove", "doc.rename", "doc.validate", "doc.write",
		"embedding.add", "embedding.cancel_switch", "embedding.log", "embedding.model_context", "embedding.models", "embedding.providers", "embedding.remove",
		"embedding.update", "embedding.usage", "embedding.use",
		"graph.backlinks", "graph.links", "graph.neighborhood", "graph.unresolved",
		"index.changes", "index.documents", "index.list", "index.models", "index.purge_model", "index.reindex", "index.status",
		"preference.list", "preference.set",
		"ranker.add", "ranker.list", "ranker.log", "ranker.models", "ranker.remove", "ranker.update", "ranker.usage", "ranker.use", "ranker.window",
		"search.query", "sys.capabilities", "sys.events", "sys.hello", "sys.shutdown", "workspace.add", "workspace.configure", "workspace.embedding_policy", "workspace.focus", "workspace.list", "workspace.remove", "workspace.rename", "workspace.section_size", "workspace.set_patterns", "workspace.set_provider", "workspace.set_schema", "workspace.set_text_extensions"}
	if got := New(Fixed(), "v").Verbs(); !reflect.DeepEqual(got, want) || Protocol != 13 || MinProtocol != 12 {
		t.Errorf("verbs %q at protocol %d: bump Protocol with the list", got, Protocol)
	}
}

// TestSession runs what an AutoVim-shaped client does: list the workspaces, write a file, follow
// the change log to it, search, read the graph and the status. The server sends nothing unasked.
func TestSession(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	ws := call(t, cli, "workspace.list").([]any)
	var states []string
	for _, w := range ws {
		m := w.(map[string]any)
		states = append(states, m["name"].(string)+"="+m["state"].(string))
	}
	if !reflect.DeepEqual(states, []string{"broken=error", "flaky=ready", "kb=ready"}) {
		t.Errorf("workspaces %q", states)
	}
	c0 := call(t, cli, "index.status", "kb").(map[string]any)["cursor"].(int64)
	v := call(t, cli, "doc.write", "kb", "plan.md", []byte("# Plan\n\nkestrel [[target]]\n"), "").(map[string]any)["version"].(string)
	if v == "" {
		t.Fatal("no version")
	}
	if _, err := r.fsys.WriteFile(context.Background(), "target.md", strings.NewReader("# Target\n")); err != nil {
		t.Fatal(err)
	}
	var seen []string
	eventually(t, "both notes in the change log", func() bool {
		res := call(t, cli, "index.changes", "kb", c0, int64(100)).(map[string]any)
		seen = seen[:0]
		for _, c := range res["changes"].([]any) {
			m := c.(map[string]any)
			seen = append(seen, m["op"].(string)+" "+m["path"].(string))
		}
		return len(seen) == 2
	})
	res := call(t, cli, "search.query", "kb", "kestrel", map[string]any{"limit": int64(5)}).(map[string]any)
	hits := res["hits"].([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["path"] != "plan.md" || res["semantic"] != "off" || res["mode_used"] != "lexical" {
		t.Errorf("search %v", res)
	}
	// "target" is in both files: the limit is the client's
	for limit, want := range map[int64]int{1: 1, 0: 2} {
		opts := map[string]any{}
		if limit > 0 {
			opts["limit"] = limit
		}
		if got := len(call(t, cli, "search.query", "kb", "target", opts).(map[string]any)["hits"].([]any)); got != want {
			t.Errorf("limit %d: %d hits, want %d", limit, got, want)
		}
	}
	links := call(t, cli, "graph.links", "kb", "plan.md").([]any)
	if len(links) != 1 || links[0].(map[string]any)["path"] != "target.md" || links[0].(map[string]any)["resolved"] != true {
		t.Errorf("links %v", links)
	}
	back := call(t, cli, "graph.backlinks", "kb", "target.md").([]any)
	if len(back) != 1 || back[0].(map[string]any)["path"] != "plan.md" {
		t.Errorf("backlinks %v", back)
	}
	nb := call(t, cli, "graph.neighborhood", "kb", "plan.md", int64(1)).(map[string]any)
	if !reflect.DeepEqual(nb["nodes"], []any{"plan.md", "target.md"}) {
		t.Errorf("neighborhood %v", nb)
	}
	st := call(t, cli, "index.status", "kb").(map[string]any)
	if st["docs"] != int64(2) || st["following"].(map[string]any)["mode"] != follow.Watching {
		t.Errorf("status %v", st)
	}
	if _, ok := st["embeddings"]; ok {
		t.Error("embeddings in the status of a workspace with no provider")
	}
	doc := call(t, cli, "doc.read", "kb", "plan.md").(map[string]any)
	if string(doc["content"].([]byte)) != "# Plan\n\nkestrel [[target]]\n" || doc["version"] != v {
		t.Errorf("read %v", doc)
	}
	if n := r.notes.Load(); n != 0 {
		t.Errorf("the server sent %d notifications", n)
	}
}

// TestErrorCodes: each failure a client acts on arrives with its code and a constant message, and
// no error's own text (a path, a driver's detail) crosses the wire.
func TestErrorCodes(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	v := call(t, cli, "doc.write", "kb", "a.md", []byte("one"), "").(map[string]any)["version"].(string)
	call(t, cli, "doc.write", "kb", "a.md", []byte("two"), v)
	fv := call(t, cli, "doc.write", "flaky", "a.md", []byte("one"), "").(map[string]any)["version"].(string)
	for _, c := range []struct {
		name   string
		method string
		params []any
		code   int64
	}{
		{"no such workspace", "search.query", []any{"nope", "x"}, CodeNoSuchWorkspace},
		{"a workspace whose root could not be opened", "index.status", []any{"broken"}, golibrpc.CodeInvalidParams},
		{"adding to a fixed set", "workspace.add", []any{"new", "/roots/new"}, CodeUnsupported},
		{"renaming in a fixed set", "workspace.rename", []any{"kb", "k2"}, CodeUnsupported},
		{"removing from a fixed set", "workspace.remove", []any{"kb"}, CodeUnsupported},
		{"section size on a fixed set", "workspace.section_size", []any{"kb", int64(256)}, CodeUnsupported},
		{"focus on a fixed set", "workspace.focus", []any{"kb"}, CodeUnsupported},
		{"policy on a fixed set", "workspace.embedding_policy", []any{"kb", "never"}, CodeUnsupported},
		{"patterns on a fixed set", "workspace.set_patterns", []any{"kb", []any{"**/*.md"}, []any{}}, CodeUnsupported},
		{"patterns need lists", "workspace.set_patterns", []any{"kb", "**/*.md", []any{}}, golibrpc.CodeInvalidParams},
		{"focus needs a name", "workspace.focus", []any{int64(1)}, golibrpc.CodeInvalidParams},
		{"section size needs a number", "workspace.section_size", []any{"kb", "bad"}, golibrpc.CodeInvalidParams},
		{"an add without a root", "workspace.add", []any{"new"}, golibrpc.CodeInvalidParams},
		{"an add with a non-list include", "workspace.add", []any{"new", "/r", "x"}, golibrpc.CodeInvalidParams},
		{"stale write", "doc.write", []any{"kb", "a.md", []byte("x"), v}, CodeConflict},
		{"create over a note", "doc.write", []any{"kb", "a.md", []byte("x"), ""}, CodeConflict},
		{"stale remove", "doc.remove", []any{"kb", "a.md", v}, CodeConflict},
		{"read a missing note", "doc.read", []any{"kb", "none.md"}, CodeNotFound},
		{"links of a missing note", "graph.links", []any{"kb", "none.md"}, CodeNotFound},
		{"an expired cursor", "index.changes", []any{"kb", int64(-5), int64(10)}, CodeCursorExpired},
		{"a cancel with no embeddings", "embedding.cancel_switch", []any{}, CodeUnsupported},
		{"model context without embeddings", "embedding.model_context", []any{"", map[string]any{}}, CodeUnsupported},
		{"semantic with no provider", "search.query", []any{"kb", "x", map[string]any{"mode": "semantic"}}, CodeUnsupported},
		{"an unknown mode", "search.query", []any{"kb", "x", map[string]any{"mode": "fuzzy"}}, golibrpc.CodeInvalidParams},
		{"an unknown option", "search.query", []any{"kb", "x", map[string]any{"fuzz": true}}, golibrpc.CodeInvalidParams},
		{"not a note", "doc.write", []any{"kb", "run.sh", []byte("x"), ""}, golibrpc.CodeInvalidParams},
		{"an escaping path", "doc.read", []any{"kb", "../etc/passwd.md"}, golibrpc.CodeInvalidParams},
		{"too many parameters", "workspace.list", []any{"x"}, golibrpc.CodeInvalidParams},
		{"a wrong type", "index.list", []any{"kb", int64(1), int64(10)}, golibrpc.CodeInvalidParams},
		{"committed", "doc.write", []any{"flaky", "a.md", []byte("two"), fv}, CodeCommitted},
	} {
		_, err := cli.Call(context.Background(), c.method, c.params...)
		if code(err) != c.code {
			t.Errorf("%s: %v, want code %d", c.name, err, c.code)
			continue
		}
		if c.name == "semantic with no provider" && !strings.Contains(err.Error(), "semantic search is not ready") {
			t.Errorf("%s: the message %q does not say semantic search is not ready", c.name, err.Error())
		}
		for _, leak := range []string{"/secret", "a.md", "none.md", "passwd", "fsync", "vfs:", "index:", "docs:"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: the message %q carries %q", c.name, err.Error(), leak)
			}
		}
	}
}

func TestWireErrMapsEverySentinel(t *testing.T) {
	for _, pe := range publicErrs {
		err := wireErr(fmt.Errorf("context: %w", pe.err))
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != pe.code || re.Message != pe.message {
			t.Errorf("%v: %v", pe.err, err)
		}
	}
	plain := errors.New("disk full at /home/x")
	if wireErr(plain) != plain {
		t.Error("an unmapped error is not left to the transport, which withholds its text")
	}
}

// TestChangeRecovery: the documented re-list (status cursor, then list, then changes from it) loses
// nothing that changed during the listing.
func TestChangeRecovery(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	for i := range 5 {
		call(t, cli, "doc.write", "kb", fmt.Sprintf("n%d.md", i), []byte("x"), "")
	}
	eventually(t, "five notes", func() bool { return call(t, cli, "index.status", "kb").(map[string]any)["docs"] == int64(5) })
	c0 := call(t, cli, "index.status", "kb").(map[string]any)["cursor"].(int64)
	var listed []string
	after := ""
	for {
		page := call(t, cli, "index.list", "kb", after, int64(2)).(map[string]any)
		for _, d := range page["docs"].([]any) {
			after = d.(map[string]any)["path"].(string)
			listed = append(listed, after)
		}
		if len(listed) == 2 { // a change lands during the listing
			call(t, cli, "doc.write", "kb", "late.md", []byte("x"), "")
		}
		if page["more"] != true {
			break
		}
	}
	eventually(t, "late.md replayed from c0", func() bool {
		res := call(t, cli, "index.changes", "kb", c0, int64(100)).(map[string]any)
		for _, c := range res["changes"].([]any) {
			if c.(map[string]any)["path"] == "late.md" {
				return true
			}
		}
		return false
	})
	if len(listed) < 5 {
		t.Errorf("listed %q", listed)
	}
	oldest := call(t, cli, "index.status", "kb").(map[string]any)["oldest_retained"].(int64)
	call(t, cli, "index.changes", "kb", oldest-1, int64(10))
}

func TestShutdownStopsTheServer(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	call(t, cli, "sys.shutdown")
	select {
	case err := <-r.done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Run: %v", err)
		}
		r.done <- err // for the cleanup
	case <-time.After(5 * time.Second):
		t.Fatal("sys.shutdown did not stop the server")
	}
}

func TestProbe(t *testing.T) {
	r := serve(t)
	ctx := context.Background()
	if v, err := ProbeOn(ctx, "unix", r.sock); err != nil || v != "v-test" {
		t.Errorf("probing autodoc: %q, %v", v, err)
	}
	// the probe admitted nothing, and a probe leaves no session behind that another client could use
	other := filepath.Join(t.TempDir(), "o.sock")
	ln, err := net.Listen("unix", other)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			_ = c.Close()
		}
	}()
	if _, err := ProbeOn(ctx, "unix", other); !errors.Is(err, ErrNotAutodoc) {
		t.Errorf("probing a foreign occupant: %v", err)
	}
	if _, err := ProbeOn(ctx, "unix", filepath.Join(t.TempDir(), "none.sock")); err == nil || errors.Is(err, ErrNotAutodoc) {
		t.Errorf("probing nothing: %v", err)
	}
}

// memPrefs is a Preferences in memory.
type memPrefs struct {
	mu sync.Mutex
	m  map[string]string
}

func (p *memPrefs) Preferences(context.Context) (map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := map[string]string{}
	for k, v := range p.m {
		out[k] = v
	}
	return out, nil
}

func (p *memPrefs) SetPreference(_ context.Context, name, value string) error {
	if name == "" {
		return store.ErrNoPreferenceName
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[name] = value
	return nil
}

// TestPreferenceVerbs: a preference set reads back in the listing; a nameless one is invalid; a
// server given no Preferences says it keeps none.
func TestPreferenceVerbs(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "p.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := New(Fixed(), "v-test", WithListener(ln), WithPreferences(&memPrefs{m: map[string]string{}}))
	go func() { _ = srv.Run(ctx) }()
	r := &rig{t: t, sock: sock}
	cli := r.dial(true)
	call(t, cli, "preference.set", "tui.theme", "light")
	call(t, cli, "preference.set", "tui.theme", "retro")
	if got := fmt.Sprint(call(t, cli, "preference.list")); got != "map[tui.theme:retro]" {
		t.Fatalf("preference.list = %s", got)
	}
	if _, err := cli.Call(ctx, "preference.set", "", "x"); code(err) != golibrpc.CodeInvalidParams {
		t.Fatalf("a nameless preference: %v, want invalid params", err)
	}
	// the preferences a verb of its own sets are not written around it: under a build's ranker, a
	// write of ranker.in_use would open the selection it protects to change
	for _, owned := range []string{store.PrefProvider, store.PrefRanker, store.PrefRankerWindow} {
		if _, err := cli.Call(ctx, "preference.set", owned, "other"); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("preference.set %s: %v, want invalid params", owned, err)
		}
	}
	if got := fmt.Sprint(call(t, cli, "preference.list")); got != "map[tui.theme:retro]" {
		t.Fatalf("after the refused writes, preference.list = %s", got)
	}
	none := serve(t).dial(true)
	if _, err := none.Call(ctx, "preference.list"); code(err) != CodeUnsupported {
		t.Fatalf("preference.list without Preferences: %v, want unsupported", err)
	}
}
