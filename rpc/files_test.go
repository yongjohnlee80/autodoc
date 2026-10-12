package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/vfs/local"
	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// filesRig is a daemon serving the disk as the daemon does (app/serve.go), over network ("unix" or
// "tcp"), with the workspaces ws. Its client has said hello at protocol proto.
func filesRig(t *testing.T, network string, proto int64, ws ...Workspace) *golibrpc.Client {
	t.Helper()
	disk, err := local.New("/")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = disk.Close() })
	addr := shortSocket(t)
	if network == "tcp" {
		addr = "127.0.0.1:0"
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Fixed(ws...), "v-test", WithListener(ln), WithFiles(docs.New(disk, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop")
		}
	})
	cli, err := golibrpc.Dial(context.Background(), ln.Addr().String(), msgpackrpc.New(nil), golibrpc.ClientNetwork(network))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": proto, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	return cli
}

func callErr(cli *golibrpc.Client, method string, params ...any) error {
	_, err := cli.Call(context.Background(), method, params...)
	return err
}

// TestFileVerbsEditAFileOutsideEveryWorkspace: file.write creates a file in a folder that does not
// exist yet, file.read returns it with its version, a write at that version lands, and a stale
// write or a second create is a conflict that leaves the file as it is (ADR 1791430651 §5.1–2).
func TestFileVerbsEditAFileOutsideEveryWorkspace(t *testing.T) {
	cli := filesRig(t, "unix", Protocol)
	abs := filepath.Join(t.TempDir(), "notes", "deep", "a.md")

	v1 := call(t, cli, "file.write", abs, []byte("# one\n"), "").(map[string]any)["version"].(string)
	got := call(t, cli, "file.read", abs).(map[string]any)
	if string(got["content"].([]byte)) != "# one\n" || got["version"] != v1 {
		t.Fatalf("file.read after the create = %v, want # one at %s", got, v1)
	}
	v2 := call(t, cli, "file.write", abs, []byte("# two\n"), v1).(map[string]any)["version"].(string)
	if v2 == v1 {
		t.Errorf("a write kept the version %s", v1)
	}
	if err := callErr(cli, "file.write", abs, []byte("# stale\n"), v1); code(err) != CodeConflict {
		t.Errorf("a stale write: %v, want a conflict", err)
	}
	if err := callErr(cli, "file.write", abs, []byte("# again\n"), ""); code(err) != CodeConflict {
		t.Errorf("a create over a file: %v, want a conflict", err)
	}
	if b, _ := os.ReadFile(abs); string(b) != "# two\n" {
		t.Errorf("on disk: %q, want the second write", b)
	}
	if err := callErr(cli, "file.read", filepath.Join(filepath.Dir(abs), "none.md")); code(err) != CodeNotFound {
		t.Errorf("a missing file: %v, want not found", err)
	}
}

// TestFileVerbsRefuseWhatIsNotAnAbsoluteTextFile: a relative, unclean or root path is invalid, and
// so is a file the editor cannot hold — binary bytes, read or written — whatever its extension.
func TestFileVerbsRefuseWhatIsNotAnAbsoluteTextFile(t *testing.T) {
	cli := filesRig(t, "unix", Protocol)
	dir := t.TempDir()
	bin := filepath.Join(dir, "blob.md")
	if err := os.WriteFile(bin, []byte("PK\x03\x04\x00\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"notes/a.md", dir + "/x/../a.md", dir + "/", "/", bin} {
		if err := callErr(cli, "file.read", p); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("file.read %q: %v, want invalid params", p, err)
		}
	}
	for _, p := range []string{"notes/a.md", dir + "/x/../a.md", dir + "/", "/"} {
		if err := callErr(cli, "file.locate", p); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("file.locate %q: %v, want invalid params", p, err)
		}
	}
	if err := callErr(cli, "file.write", filepath.Join(dir, "new.md"), []byte("a\x00b"), ""); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("writing a NUL: %v, want invalid params", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused write left a file: %v", err)
	}
}

// TestFileVerbsAreForLocalPeers: over TCP — a token's reach — every file.* verb is access denied
// and touches nothing (ADR 1791430651 §5.5, rationale §R2).
func TestFileVerbsAreForLocalPeers(t *testing.T) {
	cli := filesRig(t, "tcp", Protocol)
	abs := filepath.Join(t.TempDir(), "a.md")
	for _, c := range [][]any{{"file.read", abs}, {"file.write", abs, []byte("x"), ""}, {"file.locate", abs}} {
		if err := callErr(cli, c[0].(string), c[1:]...); code(err) != golibrpc.CodeAccessDenied {
			t.Errorf("%s over tcp: %v, want access denied", c[0], err)
		}
	}
	if _, err := os.Stat(abs); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file.write over tcp wrote: %v", err)
	}
}

// TestFileVerbsNeedProtocol17: a session below 17 does not have them.
func TestFileVerbsNeedProtocol17(t *testing.T) {
	cli := filesRig(t, "unix", 16)
	for _, v := range []string{"file.read", "file.write", "file.locate"} {
		err := callErr(cli, v, "/tmp/a.md")
		if code(err) != golibrpc.CodeMethodNotFound || !strings.Contains(err.Error(), "needs protocol 17") {
			t.Errorf("%s at 16: %v, want an unknown method naming protocol 17", v, err)
		}
	}
}

// TestFileVerbsOnAServerWithoutFiles: a server given no WithFiles says so; file.locate, which
// needs none, still answers.
func TestFileVerbsOnAServerWithoutFiles(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	abs := filepath.Join(t.TempDir(), "a.md")
	for _, c := range [][]any{{"file.read", abs}, {"file.write", abs, []byte("x"), ""}} {
		if err := callErr(cli, c[0].(string), c[1:]...); code(err) != CodeUnsupported || !strings.Contains(err.Error(), "no files outside") {
			t.Errorf("%s without files: %v, want unsupported", c[0], err)
		}
	}
	if got := call(t, cli, "file.locate", "/roots/kb/a.md"); !reflect.DeepEqual(got, map[string]any{"workspace": "kb", "path": "a.md"}) {
		t.Errorf("file.locate without files = %v", got)
	}
}

// TestFileLocateFindsTheWorkspaceThatServesAPath: the most specific root holding the path wins; a
// path outside every root, a root itself, a sibling sharing a root's prefix, a file the workspace
// excludes, and a workspace that could not open (Err, whatever else it has) are in none (ADR 1791430651 §5.4).
func TestFileLocateFindsTheWorkspaceThatServesAPath(t *testing.T) {
	base := t.TempDir()
	ws := func(name, root string) Workspace {
		return Static(WorkspaceInfo{Name: name, Root: root, Docs: docs.New(memfs.New(), md)})
	}
	cli := filesRig(t, "unix", Protocol,
		ws("kb", filepath.Join(base, "kb")),
		ws("inner", filepath.Join(base, "kb", "sub")),
		Static(WorkspaceInfo{Name: "all", Root: filepath.Join(base, "all"), Docs: docs.New(memfs.New(), nil)}), // every path
		Static(WorkspaceInfo{Name: "broken", Root: filepath.Join(base, "gone"), Docs: docs.New(memfs.New(), md),
			Err: fmt.Errorf("x: %w", workspace.ErrNotADirectory)}))
	for _, tc := range []struct {
		abs  string
		want any
	}{
		{filepath.Join(base, "kb", "a.md"), map[string]any{"workspace": "kb", "path": "a.md"}},
		{filepath.Join(base, "kb", "d", "e.md"), map[string]any{"workspace": "kb", "path": "d/e.md"}},
		{filepath.Join(base, "kb", "sub", "b.md"), map[string]any{"workspace": "inner", "path": "b.md"}},
		{filepath.Join(base, "kb", "c.txt"), nil}, // the workspace does not index it
		{filepath.Join(base, "kb2", "a.md"), nil}, // a sibling, not under kb
		{filepath.Join(base, "kb"), nil},          // the root itself is no file
		{filepath.Join(base, "all", "x.txt"), map[string]any{"workspace": "all", "path": "x.txt"}},
		{filepath.Join(base, "all"), nil},           // nor for a workspace that indexes every path
		{filepath.Join(base, "gone", "a.md"), nil},  // a workspace that could not open
		{filepath.Join(base, "other", "a.md"), nil}, // in no root
	} {
		got := call(t, cli, "file.locate", tc.abs)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("file.locate %s = %v, want %v", tc.abs, got, tc.want)
		}
	}
}

// diskWorkspace is workspace kb over root on the disk, Markdown only, its indexer running.
func diskWorkspace(t *testing.T, root string) (Workspace, *index.Indexer) {
	t.Helper()
	return diskWorkspaceOf(t, root, md, nil)
}

// diskWorkspaceOf is workspace kb over root on the disk, indexing what match admits (nil: every
// path), with the build's registrations reg (nil: none); its indexer running and no follower, so
// the index hears of a file only through the API's touch.
func diskWorkspaceOf(t *testing.T, root string, match func(string) bool, reg *registrations.Table) (Workspace, *index.Indexer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	row, err := db.AddWorkspace(ctx, "kb", root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fsys, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	ix := index.New(index.NewStore(db, row.ID), fsys, index.WithMatch(match), index.WithBatching(0, 5*time.Millisecond),
		index.WithRegistrations(reg))
	done := make(chan struct{})
	go func() { _ = ix.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done; _ = fsys.Close(); _ = db.Close() })
	var opts []docs.Option
	if reg != nil {
		opts = []docs.Option{docs.WithRegistrations(reg.Kinds()), docs.WithDeriver(reg)}
	}
	return Static(WorkspaceInfo{Name: "kb", Root: root, Index: ix, Docs: docs.New(fsys, match, opts...)}), ix
}

// TestFileWriteInAWorkspaceIsItsDocWrite: file.write on a file a workspace indexes is that
// workspace's doc.write. The write is indexed at once. The version file.read gave through "/" is
// the workspace's, so a write at it lands and a stale one conflicts. A path reached through a
// symlink into the root is the workspace's file. A path the workspace excludes is written through
// "/", as one outside every workspace is, and is not indexed.
func TestFileWriteInAWorkspaceIsItsDocWrite(t *testing.T) {
	root := t.TempDir()
	w, ix := diskWorkspace(t, root)
	cli := filesRig(t, "unix", Protocol, w)
	ctx := context.Background()
	indexed := func(p string) bool {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, ok, err := ix.Store().Version(ctx, p); err == nil && ok {
				return true
			}
			time.Sleep(10 * time.Millisecond)
		}
		return false
	}

	abs := filepath.Join(root, "notes", "a.md")
	call(t, cli, "file.write", abs, []byte("# A\n\nkestrel\n"), "")
	if !indexed("notes/a.md") {
		t.Fatal("a file written by file.write in the workspace was not indexed")
	}
	v := call(t, cli, "file.read", abs).(map[string]any)["version"].(string)
	call(t, cli, "file.write", abs, []byte("# A\n\nosprey\n"), v)
	if err := callErr(cli, "file.write", abs, []byte("# A\n\nstale\n"), v); code(err) != CodeConflict {
		t.Errorf("a write at a stale version: %v; want a conflict", err)
	}

	link := filepath.Join(t.TempDir(), "to-notes")
	if err := os.Symlink(filepath.Join(root, "notes"), link); err != nil {
		t.Fatal(err)
	}
	if got := call(t, cli, "file.locate", filepath.Join(link, "b.md")); !reflect.DeepEqual(got, map[string]any{"workspace": "kb", "path": "notes/b.md"}) {
		t.Errorf("file.locate through a symlink = %v", got)
	}
	call(t, cli, "file.write", filepath.Join(link, "b.md"), []byte("# B\n"), "")
	if !indexed("notes/b.md") {
		t.Error("a file written through a symlink into the workspace was not indexed")
	}

	excluded := filepath.Join(root, "plain.txt")
	call(t, cli, "file.write", excluded, []byte("plain\n"), "")
	if b, err := os.ReadFile(excluded); err != nil || string(b) != "plain\n" {
		t.Fatalf("an excluded path was not written through /: %q, %v", b, err)
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok, _ := ix.Store().Version(ctx, "plain.txt"); ok {
		t.Error("a path the workspace excludes was indexed")
	}
}

// TestFileWriteRawInAWorkspaceIsItsDocWriteRaw: file.write_raw on an HTML file a workspace indexes
// is the workspace's doc.write_raw. With no follower, only the routed write's touch tells the
// indexer, so the page being indexed shows the write went through the workspace. The plain
// file.write of the same page is refused, as doc.write refuses a derived document.
func TestFileWriteRawInAWorkspaceIsItsDocWriteRaw(t *testing.T) {
	reg, err := registrations.New(nil, registrations.Documents(derived.MaxContainer, derived.MaxText))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	page := filepath.Join(root, "page.html")
	if err := os.WriteFile(page, []byte("<h1>Title</h1><p>body</p>\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, ix := diskWorkspaceOf(t, root, nil, reg)
	cli := filesRig(t, "unix", Protocol, w)
	raw := call(t, cli, "doc.read_raw", "kb", "page.html").(map[string]any) // the version a read through "/" gives too
	if err := callErr(cli, "file.write", page, []byte("<p>x</p>"), raw["version"]); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("file.write of an HTML page in a workspace: %v; want refused, as doc.write refuses it", err)
	}
	call(t, cli, "file.write_raw", page, []byte("<h1>Edited</h1>\n"), raw["version"])
	if b, err := os.ReadFile(page); err != nil || string(b) != "<h1>Edited</h1>\n" {
		t.Fatalf("the page after file.write_raw: %q, %v", b, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok, err := ix.Store().Version(context.Background(), "page.html"); err == nil && ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the page written by file.write_raw was not indexed: the write did not go through the workspace")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
