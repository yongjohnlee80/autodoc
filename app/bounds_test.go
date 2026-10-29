package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// A gated script's bounds, as the TUI's gate sends them (ADR 1792004705 §4.2a.1): one parameter
// past the verb's own. These cells send them by hand, so each guard of the daemon's is seen from
// the wire, with the disk read back after.

// boundsFixture is a daemon serving kb (a home-like root: dotfiles, a protected config stand-in,
// a folder named like a document holding one) and kb2.
type boundsFixture struct {
	t                *testing.T
	root, root2, cfg string
	outside, sock    string
	cli              *golibrpc.Client
}

func newBoundsFixture(t *testing.T) *boundsFixture {
	t.Helper()
	dir := short(t)
	f := &boundsFixture{t: t, root: filepath.Join(dir, "kb"), root2: filepath.Join(dir, "kb2"), outside: filepath.Join(dir, "out")}
	f.cfg = filepath.Join(f.root, "visible", "cfg")
	for p, s := range map[string]string{
		"notes/a.md": "# A\n", ".hidden/n.md": "# N\n", ".ssh/id_x": "key", ".bashrc": "rc\n", ".git/hooks/pre-commit": "#!/bin/sh\n",
		"visible/cfg/lua/x.lua": "-- x\n", "bundle.md/cfg/lua/y.lua": "-- y\n", "data.csv": "a,b\n", ".autodoc/schema.yaml": "fields: {}\n",
	} {
		writeAt(t, filepath.Join(f.root, p), s)
	}
	writeAt(t, filepath.Join(f.root2, "b.md"), "# B\n")
	writeAt(t, filepath.Join(f.outside, "secret"), "s")
	f.sock = filepath.Join(dir, "a.sock")
	start(t, writeConfig(t, f.sock, filepath.Join(dir, "state"), "", "kb="+f.root, "kb2="+f.root2), f.sock)
	f.cli = dial(t, f.sock)
	eventually(t, "kb indexed", func() bool {
		st := call(t, f.cli, "index.status", "kb").(map[string]any)
		return st["pending_jobs"] == int64(0) && st["docs"] != int64(0)
	})
	return f
}

func writeAt(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// bounds is the gate's parameter: within, and the TUI's config folder protected.
func (f *boundsFixture) bounds(within string) map[string]any {
	return map[string]any{"within": within, "protect": []any{f.cfg}}
}

// try is method with params, its error as the wire has it.
func (f *boundsFixture) try(method string, params ...any) (any, error) {
	return f.cli.Call(context.Background(), method, params...)
}

// denied fails unless err is access denied naming why.
func (f *boundsFixture) denied(what string, err error, why string) {
	f.t.Helper()
	var re *golibrpc.Error
	if !errors.As(err, &re) || re.Code != golibrpc.CodeAccessDenied || !strings.Contains(re.Message, why) {
		f.t.Errorf("%s: %v, want access denied (%s)", what, err, why)
	}
}

// file is the root's file p now, "" when it is not there.
func (f *boundsFixture) file(p string) string {
	b, _ := os.ReadFile(filepath.Join(f.root, p))
	return string(b)
}

// TestABoundedCallIsHeldToItsWorkspace: reads reach any served workspace, writes only within's,
// and a write naming no workspace, or another one, is refused; the user's call is not bounded.
func TestABoundedCallIsHeldToItsWorkspace(t *testing.T) {
	f := newBoundsFixture(t)
	if _, err := f.try("doc.read", "kb2", "b.md", f.bounds("*")); err != nil {
		t.Errorf("a read of another served workspace: %v", err)
	}
	if _, err := f.try("doc.write", "kb", "notes/new.md", "# new\n", "", f.bounds("kb")); err != nil || f.file("notes/new.md") != "# new\n" {
		t.Errorf("a write in the current workspace: %v", err)
	}
	_, err := f.try("doc.write", "kb2", "c.md", "x", "", f.bounds("kb"))
	f.denied("a write to another workspace", err, "another workspace")
	_, err = f.try("doc.write", "kb", "notes/any.md", "x", "", f.bounds("*"))
	f.denied("a write that names no workspace", err, "names no workspace")
	_, err = f.try("file.read", filepath.Join(f.outside, "secret"), f.bounds("*"))
	f.denied("file.read outside every root", err, "outside the workspace")
	_, err = f.try("file.write", filepath.Join(f.outside, "secret"), "x", "", f.bounds("kb"))
	f.denied("file.write outside every root", err, "outside the workspace")
	if b, _ := os.ReadFile(filepath.Join(f.outside, "secret")); string(b) != "s" {
		t.Errorf("the file outside was written: %q", b)
	}
	if _, err := f.try("file.write", filepath.Join(f.root, "notes", "f.txt"), "x\n", "", f.bounds("kb")); err != nil || f.file("notes/f.txt") != "x\n" {
		t.Errorf("file.write in the current workspace: %v", err)
	}
	// .bashrc: the script's write refused and the file unchanged; the user's own lands, as before
	rc := filepath.Join(f.root, ".bashrc")
	read, err := f.try("file.read", rc)
	if err != nil {
		t.Fatal(err)
	}
	version := read.(map[string]any)["version"]
	_, err = f.try("file.write", rc, "evil\n", version, f.bounds("kb"))
	f.denied("a script's write of .bashrc", err, "is hidden")
	if f.file(".bashrc") != "rc\n" {
		t.Errorf("the refused write changed .bashrc: %q", f.file(".bashrc"))
	}
	if _, err := f.try("file.write", rc, "mine\n", version); err != nil || f.file(".bashrc") != "mine\n" {
		t.Errorf("the user's own write of .bashrc: %v", err)
	}
}

// TestHiddenAndOwnPathsAreRefused: through file.*, doc.* and workspace.scaffold alike, a hidden
// path is refused, read or write, AutoDoc's own allowed; the TUI's config folder (protect) too; a
// create whose hidden folder does not exist yet is refused before anything is made.
func TestHiddenAndOwnPathsAreRefused(t *testing.T) {
	f := newBoundsFixture(t)
	_, err := f.try("file.read", filepath.Join(f.root, ".ssh", "id_x"), f.bounds("*"))
	f.denied("file.read of .ssh/id_x", err, "is hidden")
	_, err = f.try("doc.read", "kb", ".hidden/n.md", f.bounds("*"))
	f.denied("doc.read of a hidden document", err, "is hidden")
	_, err = f.try("doc.read_raw", "kb", ".hidden/n.md", f.bounds("*"))
	f.denied("doc.read_raw of a hidden document", err, "is hidden")
	_, err = f.try("doc.outline", "kb", ".hidden/n.md", f.bounds("*"))
	f.denied("doc.outline of a hidden document", err, "is hidden")
	if _, err := f.try("doc.read", "kb", ".hidden/n.md"); err != nil {
		t.Errorf("the user's read of it: %v", err)
	}
	_, err = f.try("file.write", filepath.Join(f.root, ".git", "hooks", "pre-commit"), "x", "", f.bounds("kb"))
	f.denied("a hook written", err, "is hidden")
	_, err = f.try("doc.write", "kb", "visible/.ssh/authorized_keys.md", "x", "", f.bounds("kb"))
	f.denied("a create under a hidden folder not made yet", err, "is hidden")
	if _, err := os.Stat(filepath.Join(f.root, "visible", ".ssh")); !os.IsNotExist(err) {
		t.Errorf("the refused create made its folder: %v", err)
	}
	if _, err := f.try("file.read", filepath.Join(f.root, ".autodoc", "schema.yaml"), f.bounds("*")); err != nil {
		t.Errorf("AutoDoc's own .autodoc/: %v", err)
	}
	_, err = f.try("file.write", filepath.Join(f.cfg, "lua", "x.lua"), "evil", "", f.bounds("kb"))
	f.denied("the TUI's config folder", err, "AutoDoc's own")
	if f.file("visible/cfg/lua/x.lua") != "-- x\n" {
		t.Errorf("the config script was written")
	}

	res, err := f.try("workspace.scaffold", "kb", []any{
		map[string]any{"path": "ok.md", "content": "# ok\n"},
		map[string]any{"path": ".config/autostart/x.desktop", "content": "[Desktop Entry]\n"},
		map[string]any{"path": "ok2.md", "content": "# ok2\n", "immutable": ".git"},
	}, f.bounds("kb"))
	if err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	got := map[string]string{}
	for _, r := range res.([]any) {
		m := r.(map[string]any)
		got[m["path"].(string)] = m["outcome"].(string)
	}
	if got["ok.md"] != rpc.ScaffoldCreated || got[".config/autostart/x.desktop"] != rpc.ScaffoldRefused || got["ok2.md"] != rpc.ScaffoldRefused {
		t.Errorf("a mixed scaffold batch: %v, want ok.md created, the others refused", got)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".config")); !os.IsNotExist(err) {
		t.Errorf("the refused descriptor made .config: %v", err)
	}
}

// TestARenameIsCheckedAtBothEnds: a hidden or AutoDoc's own destination, and a hidden source, are
// refused with both files unchanged; a folder named like a document is moved by no gated call, so
// the protected folder inside it stays; the user's own rename of it lands.
func TestARenameIsCheckedAtBothEnds(t *testing.T) {
	f := newBoundsFixture(t)
	_, err := f.try("doc.rename", "kb", "notes/a.md", ".hidden/a.md", f.bounds("kb"))
	f.denied("to a hidden destination", err, "is hidden")
	_, err = f.try("doc.rename", "kb", "notes/a.md", "visible/cfg/a.md", f.bounds("kb"))
	f.denied("into the config folder", err, "AutoDoc's own")
	_, err = f.try("doc.rename", "kb", ".hidden/n.md", "notes/n.md", f.bounds("kb"))
	f.denied("from a hidden source", err, "is hidden")
	if f.file("notes/a.md") != "# A\n" || f.file(".hidden/n.md") != "# N\n" {
		t.Errorf("a refused rename moved a file")
	}
	_, err = f.try("doc.rename", "kb", "bundle.md", "moved.md", f.bounds("kb"))
	f.denied("a folder renamed", err, "is a folder")
	if f.file("bundle.md/cfg/lua/y.lua") != "-- y\n" {
		t.Errorf("the folder's contents moved")
	}
	if _, err := f.try("doc.rename", "kb", "notes/a.md", "notes/b.md", f.bounds("kb")); err != nil {
		t.Errorf("a gated rename of a document: %v", err)
	}
	if _, err := f.try("doc.rename", "kb", "bundle.md", "moved.md"); err != nil || f.file("moved.md/cfg/lua/y.lua") != "-- y\n" {
		t.Errorf("the user's own rename of the folder: %v", err)
	}
}

// TestABoundedLocateAnswersMembership: under bounds, file.locate answers a file the script may
// read, indexed or not (a gated open of data.csv), and none for a hidden one; unbounded, it keeps
// answering only what the workspace indexes.
func TestABoundedLocateAnswersMembership(t *testing.T) {
	f := newBoundsFixture(t)
	csv := filepath.Join(f.root, "data.csv")
	if res, err := f.try("file.locate", csv); err != nil || res != nil {
		t.Errorf("unbounded locate of an unindexed file: %v, %v; want none", res, err)
	}
	res, err := f.try("file.locate", csv, f.bounds("*"))
	if m, _ := res.(map[string]any); err != nil || m["workspace"] != "kb" || m["path"] != "data.csv" {
		t.Errorf("bounded locate of data.csv: %v, %v; want kb data.csv", res, err)
	}
	if res, err := f.try("file.locate", filepath.Join(f.root, ".ssh", "id_x"), f.bounds("*")); err != nil || res != nil {
		t.Errorf("bounded locate of a hidden file: %v, %v; want none", res, err)
	}
}

// TestASecondHardLinkIsRefused: a file in the root that is a second name of one outside it is
// refused to a bounded read.
func TestASecondHardLinkIsRefused(t *testing.T) {
	f := newBoundsFixture(t)
	link := filepath.Join(f.root, "notes", "secret.md")
	if err := os.Link(filepath.Join(f.outside, "secret"), link); err != nil {
		t.Skip("no hard links here: ", err)
	}
	_, err := f.try("file.read", link, f.bounds("*"))
	f.denied("a second hard link", err, "another name")
}

// TestBoundsArriveFromProtocol34: a session below 34 sends no bounds, so a parameter past a verb's
// own is refused as one too many, never taken for bounds.
func TestBoundsArriveFromProtocol34(t *testing.T) {
	f := newBoundsFixture(t)
	old, err := golibrpc.Dial(context.Background(), f.sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := old.Call(context.Background(), "sys.hello", map[string]any{"protocol": rpc.BoundsProtocol - 1}); err != nil {
		t.Fatal(err)
	}
	_, err = old.Call(context.Background(), "doc.read", "kb", ".hidden/n.md", f.bounds("*"))
	var re *golibrpc.Error
	if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams {
		t.Errorf("a protocol-33 session's extra parameter: %v, want invalid params", err)
	}
}

// TestTheDaemonsOwnPathsAreRefused: the daemon's STATE (its store, its log) and its config with the
// folder beside it are refused to a bounded call, read and write, when a workspace's root holds
// them; the workspace's other files are reached.
func TestTheDaemonsOwnPathsAreRefused(t *testing.T) {
	dir := short(t)
	root := filepath.Join(dir, "kb")
	writeAt(t, filepath.Join(root, "notes", "a.md"), "# A\n")
	state := filepath.Join(root, "state")
	sock := filepath.Join(dir, "a.sock")
	made := writeConfig(t, sock, state, "", "kb="+root)
	body, err := os.ReadFile(made)
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "conf", "config.toml") // the daemon's own config, inside the root
	writeAt(t, cfg, string(body))
	start(t, cfg, sock)
	f := &boundsFixture{t: t, root: root, sock: sock, cli: dial(t, sock)}
	f.cfg = filepath.Join(dir, "elsewhere") // the TUI's config is not in the root: only the daemon's own
	for _, p := range []string{filepath.Join(state, "autodoc.db"), cfg} {
		_, err := f.try("file.read", p, f.bounds("*"))
		f.denied("a read of "+p, err, "AutoDoc's own")
	}
	_, err = f.try("file.write", filepath.Join(state, "new.md"), "x", "", f.bounds("kb"))
	f.denied("a write into STATE", err, "AutoDoc's own")
	_, err = f.try("file.write", filepath.Join(root, "conf", "lua.md"), "x", "", f.bounds("kb"))
	f.denied("a write beside the daemon's config", err, "AutoDoc's own")
	if _, err := f.try("file.read", filepath.Join(root, "notes", "a.md"), f.bounds("*")); err != nil {
		t.Errorf("the workspace's own file: %v", err)
	}
}
