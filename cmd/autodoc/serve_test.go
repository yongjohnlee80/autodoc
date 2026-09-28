package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// syncBuf is a daemon's output, read while it writes.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

type daemon struct {
	t      *testing.T
	sock   string
	out    *syncBuf
	cancel context.CancelFunc
	done   chan error
}

// short is a temporary directory with a path short enough for a unix socket.
func short(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "ad")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(d) })
	return d
}

// writeConfig writes a configuration: a socket, a state directory and the workspaces (name=root),
// plus extra TOML.
func writeConfig(t *testing.T, sock, state, extra string, workspaces ...string) string {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "[server]\nsocket = %q\nstate_dir = %q\n[follow]\npoll_interval = \"50ms\"\n%s\n", sock, state, extra)
	for _, w := range workspaces {
		name, root, _ := strings.Cut(w, "=")
		fmt.Fprintf(&b, "[[workspace]]\nname = %q\nroot = %q\n", name, root)
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// start runs runServe on cfg and waits until it answers, or until it returns (wait false: return
// at once with the handle).
func start(t *testing.T, cfg, sock string) *daemon {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	d := &daemon{t: t, sock: sock, out: &syncBuf{}, cancel: cancel, done: make(chan error, 1)}
	go func() { d.done <- runServe(ctx, cfg, d.out) }()
	t.Cleanup(func() { d.stop() })
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := rpc.ProbeOn(context.Background(), "unix", sock); err == nil {
			return d
		}
		select {
		case err := <-d.done:
			t.Fatalf("runServe returned: %v\n%s", err, d.out)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon did not answer\n%s", d.out)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (d *daemon) stop() {
	d.cancel()
	select {
	case err := <-d.done:
		if err != nil && !errors.Is(err, context.Canceled) {
			d.t.Errorf("runServe: %v", err)
		}
		d.done <- nil
	case <-time.After(10 * time.Second):
		d.t.Error("the daemon did not stop")
	}
}

func dial(t *testing.T, sock string) *golibrpc.Client {
	t.Helper()
	cli, err := golibrpc.Dial(context.Background(), sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": rpc.Protocol}); err != nil {
		t.Fatal(err)
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

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestServesAndCleansUp: the socket is 0600 while it serves, and gone after.
func TestServesAndCleansUp(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	d := start(t, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+t.TempDir()), sock)
	fi, err := os.Stat(sock)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket %v, %v", fi.Mode(), err)
	}
	if got := call(t, dial(t, sock), "workspace.list").([]any); len(got) != 1 || got[0].(map[string]any)["state"] != "ready" {
		t.Errorf("workspaces %v", got)
	}
	d.stop()
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket is still there: %v", err)
	}
}

// TestSecondDaemonIsRefused: a second --serve on a served endpoint says so and serves nothing; the
// first keeps serving on its socket.
func TestSecondDaemonIsRefused(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	cfg := writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+t.TempDir())
	start(t, cfg, sock)
	err := runServe(context.Background(), cfg, io.Discard)
	if !errors.Is(err, errAlreadyServing) {
		t.Fatalf("a second daemon: %v", err)
	}
	if _, err := rpc.ProbeOn(context.Background(), "unix", sock); err != nil {
		t.Errorf("the first daemon stopped answering: %v", err)
	}
}

// TestStaleSocketIsReplaced: a socket file nothing answers on (a crashed daemon's) is taken over.
func TestStaleSocketIsReplaced(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close() // the file stays, and nothing listens
	start(t, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+t.TempDir()), sock)
}

// TestSuccessorsSocketIsLeft: a daemon shutting down removes the socket only while it is its own.
func TestSuccessorsSocketIsLeft(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	d := start(t, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+t.TempDir()), sock)
	// a successor took the path over (as after a stale-socket takeover)
	if err := os.Remove(sock); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d.stop()
	if _, err := os.Stat(sock); err != nil {
		t.Errorf("the successor's socket was removed: %v", err)
	}
}

// TestWorkspaceBusy: a second daemon on another endpoint that names a served workspace serves that
// one as busy, and its others.
func TestWorkspaceBusy(t *testing.T) {
	dir := short(t)
	state := filepath.Join(dir, "state")
	shared, own := t.TempDir(), t.TempDir()
	a := filepath.Join(dir, "a.sock")
	start(t, writeConfig(t, a, state, "", "kb="+shared), a)
	b := filepath.Join(dir, "b.sock")
	start(t, writeConfig(t, b, state, "", "kb="+shared, "mine="+own), b)
	cli := dial(t, b)
	var states []string
	for _, w := range call(t, cli, "workspace.list").([]any) {
		m := w.(map[string]any)
		states = append(states, m["name"].(string)+"="+m["state"].(string))
	}
	if !reflect.DeepEqual(states, []string{"kb=busy", "mine=ready"}) {
		t.Errorf("workspaces %q", states)
	}
	var re *golibrpc.Error
	if _, err := cli.Call(context.Background(), "index.status", "kb"); !errors.As(err, &re) || re.Code != rpc.CodeWorkspaceBusy {
		t.Errorf("the busy workspace: %v", err)
	}
	call(t, cli, "index.status", "mine")
}

// changed polls index.changes from since until want (op path, in any order) have all been seen.
func changed(t *testing.T, cli *golibrpc.Client, since int64, want ...string) int64 {
	t.Helper()
	cursor := since
	seen := map[string]bool{}
	eventually(t, fmt.Sprintf("changes %q", want), func() bool {
		res := call(t, cli, "index.changes", "kb", cursor, int64(100)).(map[string]any)
		cursor = res["cursor"].(int64)
		for _, c := range res["changes"].([]any) {
			m := c.(map[string]any)
			seen[m["op"].(string)+" "+m["path"].(string)] = true
		}
		for _, w := range want {
			if !seen[w] {
				return false
			}
		}
		return true
	})
	return cursor
}

// TestFollowsTheFiles: on a real root, an external write, an editor's atomic save (a temporary file
// renamed over the note), a delete, a move within the root and a populated directory moved in each
// reach the index, and index.changes reports them (ADR 0203 §5.1).
func TestFollowsTheFiles(t *testing.T) {
	dir := short(t)
	root := t.TempDir()
	sock := filepath.Join(dir, "a.sock")
	start(t, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+root), sock)
	cli := dial(t, sock)
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	c := call(t, cli, "index.status", "kb").(map[string]any)["cursor"].(int64)
	write("a.md", "one")
	c = changed(t, cli, c, "upsert a.md")
	write(".a.md.swp", "two")
	if err := os.Rename(filepath.Join(root, ".a.md.swp"), filepath.Join(root, "a.md")); err != nil {
		t.Fatal(err)
	}
	c = changed(t, cli, c, "upsert a.md")
	if err := os.Rename(filepath.Join(root, "a.md"), filepath.Join(root, "b.md")); err != nil {
		t.Fatal(err)
	}
	c = changed(t, cli, c, "delete a.md", "upsert b.md")
	if err := os.Remove(filepath.Join(root, "b.md")); err != nil {
		t.Fatal(err)
	}
	c = changed(t, cli, c, "delete b.md")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "d", "e"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"d/x.md", "d/e/y.md"} {
		if err := os.WriteFile(filepath.Join(outside, p), []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(filepath.Join(outside, "d"), filepath.Join(root, "d")); err != nil {
		t.Fatal(err)
	}
	changed(t, cli, c, "upsert d/x.md", "upsert d/e/y.md")
}

// TestWrittenLikeAnExternalEdit: a note written through doc.write and the same note written from
// outside index the same (ADR 0203 §5.5): the one engine path.
func TestWrittenLikeAnExternalEdit(t *testing.T) {
	dir := short(t)
	root := t.TempDir()
	sock := filepath.Join(dir, "a.sock")
	start(t, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+root), sock)
	cli := dial(t, sock)
	body := "# Heron\n\ngrey heron #wader [[egret]]\n\n## Range\n\nmarshes\n"
	call(t, cli, "doc.write", "kb", "a/n.md", []byte(body), "")
	if err := os.MkdirAll(filepath.Join(root, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b", "n.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	shape := func(path string) string {
		var parts []string
		for _, h := range call(t, cli, "search.query", "kb", "heron marshes grey", map[string]any{"paths": []any{filepath.Dir(path)}}).(map[string]any)["hits"].([]any) {
			m := h.(map[string]any)
			parts = append(parts, fmt.Sprintf("%v %v-%v %q", m["breadcrumb"], m["byte_start"], m["byte_end"], m["snippet"]))
		}
		for _, l := range call(t, cli, "graph.links", "kb", path).([]any) {
			m := l.(map[string]any)
			parts = append(parts, fmt.Sprintf("%v %v", m["raw"], m["kind"]))
		}
		return strings.Join(parts, " | ")
	}
	eventually(t, "both notes indexed", func() bool { return call(t, cli, "index.status", "kb").(map[string]any)["docs"] == int64(2) })
	if a, b := shape("a/n.md"), shape("b/n.md"); a != b || a == "" {
		t.Errorf("written:  %s\nexternal: %s", a, b)
	}
}

// ollama answers like an Ollama server with one embedding model: hashed-word vectors.
func ollama(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "tiny:latest", "model": "tiny:latest", "digest": "sha256:t"}}})
	})
	mux.HandleFunc("POST /api/embed", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Input []string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		var out [][]float32
		for _, s := range req.Input {
			v := make([]float32, 32)
			for _, word := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) }) {
				h := fnv.New32a()
				h.Write([]byte(word))
				v[h.Sum32()%32]++
			}
			out = append(out, v)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": out})
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// TestEmbeddingFromConfig: [embedding] names the provider, the daemon embeds with it, and search
// goes hybrid; a provider that cannot be set up leaves search lexical and says so in the log.
func TestEmbeddingFromConfig(t *testing.T) {
	dir := short(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "z.md"), []byte("zebra giraffe\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := ollama(t)
	sock := filepath.Join(dir, "a.sock")
	start(t, writeConfig(t, sock, filepath.Join(dir, "state"), fmt.Sprintf("[embedding]\nprovider = \"ollama\"\nmodel = \"tiny\"\nbase_url = %q\n", srv.URL), "kb="+root), sock)
	cli := dial(t, sock)
	// with no document yet, none is unready: wait for the note first
	eventually(t, "the note indexed and semantic-ready", func() bool {
		st := call(t, cli, "index.status", "kb").(map[string]any)
		e, ok := st["embeddings"].(map[string]any)
		return st["docs"] == int64(1) && ok && e["semantic"] == "ready" && e["pending"] == int64(0) &&
			e["model"] == "ollama|tiny|sha256:t|32"
	})
	res := call(t, cli, "search.query", "kb", "zebra lion", nil).(map[string]any)
	if res["mode_used"] != "hybrid" || len(res["hits"].([]any)) != 1 {
		t.Errorf("search %v", res)
	}

	dir2 := short(t)
	sock2 := filepath.Join(dir2, "a.sock")
	d := start(t, writeConfig(t, sock2, filepath.Join(dir2, "state"), "[embedding]\nprovider = \"ollama\"\nmodel = \"tiny\"\nbase_url = \"http://127.0.0.1:1\"\n", "kb="+t.TempDir()), sock2)
	res = call(t, dial(t, sock2), "search.query", "kb", "zebra", nil).(map[string]any)
	if res["semantic"] != "off" || !strings.Contains(d.out.String(), "embedding off") {
		t.Errorf("an unreachable provider: %v\n%s", res, d.out)
	}
}
