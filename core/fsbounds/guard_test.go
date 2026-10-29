package fsbounds

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/vfs"
	"github.com/yongjohnlee80/golib/vfs/local"
)

// tree is a workspace "kb" at a fresh root, with home-like dotfiles in it, AutoDoc's own allowed
// ones, and a protected config folder below it; the guard serves it alone unless a test adds more.
type tree struct {
	root, outside, cfg string
	g                  *Guard
}

func newTree(t *testing.T) *tree {
	t.Helper()
	base := t.TempDir()
	tr := &tree{root: filepath.Join(base, "kb"), outside: filepath.Join(base, "outside"), cfg: filepath.Join(base, "kb", "visible", "cfg")}
	for _, d := range []string{"notes", ".ssh", ".config/autostart", ".git/hooks", ".autodoc", ".todo-list", "visible/cfg/lua", "bundle.md/inner"} {
		mustMkdir(t, filepath.Join(tr.root, d))
	}
	mustMkdir(t, tr.outside)
	for p, s := range map[string]string{
		"notes/a.md": "a", ".ssh/id_x": "key", ".bashrc": "rc", ".autodoc/schema.yaml": "s",
		".todo-list/t.md": "t", ".autodoc-vars.toml": "v", "visible/cfg/lua/x.lua": "lua", "bundle.md/inner/b.md": "b",
	} {
		mustWrite(t, filepath.Join(tr.root, p), s)
	}
	mustWrite(t, filepath.Join(tr.outside, "secret"), "s")
	tr.g = mustGuard(t, tr.cfg)
	tr.g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: tr.root}} })
	return tr
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

var (
	readAny = Bounds{Within: AnyWorkspace}
	writeKB = Bounds{Within: "kb"}
)

func (tr *tree) check(name string, b Bounds, o op) error {
	return tr.g.check(Root{Name: "kb", Dir: tr.root}, name, b, o)
}

// refusedFor fails unless err is a Refusal naming why.
func refusedFor(t *testing.T, what string, err error, why string) {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || !errors.Is(err, ErrRefused) || r.Why != why {
		t.Errorf("%s: %v, want refused as %q", what, err, why)
	}
}

// TestTheGuardsRules: each rule of the package doc refuses what it names, and lets its control
// through.
func TestTheGuardsRules(t *testing.T) {
	tr := newTree(t)
	if err := tr.check("notes/a.md", readAny, opRead); err != nil {
		t.Errorf("a document, read: %v", err)
	}
	if err := tr.check("notes/a.md", writeKB, opWrite); err != nil {
		t.Errorf("a document, written in its workspace: %v", err)
	}
	refusedFor(t, "a write to another workspace", tr.check("notes/a.md", Bounds{Within: "kb2"}, opWrite), whyOther)
	refusedFor(t, "a write that names no workspace", tr.check("notes/a.md", readAny, opWrite), whyAny)
	refusedFor(t, "a read bounded to another workspace", tr.check("notes/a.md", Bounds{Within: "kb2"}, opRead), whyOther)

	for _, p := range []string{".ssh/id_x", ".bashrc", ".config/autostart/x.desktop", ".git/hooks/pre-commit", "notes/.hidden.md", ".autodoc/.x"} {
		refusedFor(t, p+" read", tr.check(p, readAny, opRead), whyHidden)
		refusedFor(t, p+" written", tr.check(p, writeKB, opWrite), whyHidden)
	}
	for _, p := range []string{".autodoc/schema.yaml", ".todo-list/t.md", ".autodoc-vars.toml"} {
		if err := tr.check(p, writeKB, opWrite); err != nil {
			t.Errorf("AutoDoc's own %s: %v", p, err)
		}
	}

	// the protected folder, made, and anything under it; refused to reads too
	refusedFor(t, "the config folder's script", tr.check("visible/cfg/lua/x.lua", readAny, opRead), whyOwn)
	refusedFor(t, "a new file in the config folder", tr.check("visible/cfg/lua/new.lua", writeKB, opWrite), whyOwn)
	refusedFor(t, "the config folder itself", tr.check("visible/cfg", writeKB, opWrite), whyOwn)
	if err := tr.check("visible/other.md", writeKB, opWrite); err != nil {
		t.Errorf("beside the config folder: %v", err)
	}
}

// TestACreateIsCheckedWhole: a create's missing tail is part of what is checked, so a hidden
// folder a create would make is refused before anything is made; a nested visible create is not.
func TestACreateIsCheckedWhole(t *testing.T) {
	tr := newTree(t)
	refusedFor(t, "visible/.ssh/new.txt, .ssh not made yet", tr.check("visible/.ssh/new.txt", writeKB, opWrite), whyHidden)
	if _, err := os.Stat(filepath.Join(tr.root, "visible", ".ssh")); !os.IsNotExist(err) {
		t.Errorf("the check made something: %v", err)
	}
	if err := tr.check("visible/new/sub.md", writeKB, opWrite); err != nil {
		t.Errorf("a nested visible create: %v", err)
	}
}

// TestLinksAreFollowedNotTrusted: a link inside the root that leads out, or to a hidden folder in
// it, is refused; one that stays in it and visible is checked as its target; a link that leads
// nowhere refuses rather than being taken for a missing name.
func TestLinksAreFollowedNotTrusted(t *testing.T) {
	tr := newTree(t)
	for link, target := range map[string]string{"notes/out": tr.outside, "notes/keys": filepath.Join(tr.root, ".ssh"), "notes/in": filepath.Join(tr.root, "visible"), "notes/dangling": filepath.Join(tr.root, "nowhere")} {
		if err := os.Symlink(target, filepath.Join(tr.root, link)); err != nil {
			t.Fatal(err)
		}
	}
	refusedFor(t, "a link out", tr.check("notes/out/secret", readAny, opRead), whyOutside)
	refusedFor(t, "a link to .ssh", tr.check("notes/keys/id_x", readAny, opRead), whyHidden)
	refusedFor(t, "a dangling link", tr.check("notes/dangling/x.md", writeKB, opWrite), whyDangling)
	if err := tr.check("notes/in/new.md", writeKB, opWrite); err != nil {
		t.Errorf("a link that stays in the root: %v", err)
	}
	refusedFor(t, "a link to the config folder", tr.check("notes/in/cfg/lua/x.lua", readAny, opRead), whyOwn)
}

// TestAFolderIsNeitherMovedNorRemoved: a tree operation's source that is a folder is refused,
// whatever lies in it; a file's is not, nor a link to a folder (it moves as the link).
func TestAFolderIsNeitherMovedNorRemoved(t *testing.T) {
	tr := newTree(t)
	refusedFor(t, "a folder renamed", tr.check("bundle.md", writeKB, opTree), whyFolder)
	refusedFor(t, "a folder copied", tr.check("bundle.md", readAny, opCopy), whyFolder)
	if err := tr.check("notes/a.md", writeKB, opTree); err != nil {
		t.Errorf("a file renamed: %v", err)
	}
	if err := os.Symlink(filepath.Join(tr.root, "bundle.md"), filepath.Join(tr.root, "notes", "link.md")); err != nil {
		t.Fatal(err)
	}
	if err := tr.check("notes/link.md", writeKB, opTree); err != nil {
		t.Errorf("a link to a folder, moved as the link: %v", err)
	}
}

// TestASecondNameIsRefused: a regular file with another hard link is refused, read and write.
func TestASecondNameIsRefused(t *testing.T) {
	tr := newTree(t)
	if err := os.Link(filepath.Join(tr.outside, "secret"), filepath.Join(tr.root, "notes", "secret.md")); err != nil {
		t.Skip("no hard links here: ", err)
	}
	refusedFor(t, "a hard link read", tr.check("notes/secret.md", readAny, opRead), whyLinked)
	refusedFor(t, "a hard link written", tr.check("notes/secret.md", writeKB, opWrite), whyLinked)
}

// TestAMissingProtectedFolderIsReservedByName: a protected folder not made yet has no identity, so
// a create of it, or of it spelled in another case, below the folder it will be made in is refused.
// Once made, identity matches it.
func TestAMissingProtectedFolderIsReservedByName(t *testing.T) {
	tr := newTree(t)
	missing := filepath.Join(tr.root, "visible", "newcfg")
	tr.g = mustGuard(t, missing)
	tr.g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: tr.root}} })
	refusedFor(t, "the missing folder's script", tr.check("visible/newcfg/lua/x.lua", writeKB, opWrite), whyOwn)
	refusedFor(t, "it in another case", tr.check("visible/NewCfg/lua/x.lua", writeKB, opWrite), whyOwn)
	if err := tr.check("visible/newcfgx/a.md", writeKB, opWrite); err != nil {
		t.Errorf("a name that only starts like it: %v", err)
	}
	mustMkdir(t, missing)
	refusedFor(t, "once made", tr.check("visible/newcfg/x.lua", writeKB, opWrite), whyOwn)
}

// TestTheInnermostRootAnchorsTheHiddenRule: with a home-like root and its .ssh both served, a file
// of .ssh is the .ssh workspace's and not hidden there; written from the outer one, it is another
// workspace's.
func TestTheInnermostRootAnchorsTheHiddenRule(t *testing.T) {
	tr := newTree(t)
	inner := filepath.Join(tr.root, ".ssh")
	tr.g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: tr.root}, {Name: "keys", Dir: inner}} })
	if err := tr.g.check(Root{Name: "keys", Dir: inner}, "id_x", readAny, opRead); err != nil {
		t.Errorf("the served .ssh's own file: %v", err)
	}
	refusedFor(t, "written from the outer workspace", tr.check(".ssh/id_x", writeKB, opWrite), whyOther)
	ws, name, err := tr.g.Locate(filepath.Join(inner, "id_x"))
	if err != nil || ws.Name != "keys" || name != "id_x" {
		t.Errorf("Locate: %v %q %v, want keys id_x", ws, name, err)
	}
}

// TestTheCallsProtectedFoldersAreTheCallers: protect, the TUI's config folder, is refused as the
// guard's own are.
func TestTheCallsProtectedFoldersAreTheCallers(t *testing.T) {
	tr := newTree(t)
	tr.g = mustGuard(t)
	tr.g.SetServed(func() []Root { return []Root{{Name: "kb", Dir: tr.root}} })
	if err := tr.check("visible/cfg/lua/x.lua", writeKB, opWrite); err != nil {
		t.Fatalf("unprotected: %v", err)
	}
	refusedFor(t, "protected by the call", tr.check("visible/cfg/lua/x.lua", Bounds{Within: "kb", Protect: []string{tr.cfg}}, opWrite), whyOwn)
}

// TestLocateFindsTheWorkspace: a bounded file.* path is routed to the innermost served root, and
// one outside every root is refused.
func TestLocateFindsTheWorkspace(t *testing.T) {
	tr := newTree(t)
	ws, name, err := tr.g.Locate(filepath.Join(tr.root, "notes", "a.md"))
	if err != nil || ws.Name != "kb" || name != "notes/a.md" {
		t.Errorf("Locate: %v %q %v", ws, name, err)
	}
	_, _, err = tr.g.Locate(filepath.Join(tr.outside, "secret"))
	refusedFor(t, "outside every root", err, whyOutside)
	var none *Guard
	_, _, err = none.Locate(filepath.Join(tr.root, "notes", "a.md"))
	refusedFor(t, "no guard", err, whyOutside)
}

// TestEveryMethodIsChecked: each method of the wrapped filesystem, and each capability, refuses a
// hidden name under bounds and passes the user's call through; a nil guard refuses every bounded
// one.
func TestEveryMethodIsChecked(t *testing.T) {
	tr := newTree(t)
	disk, err := local.New(tr.root)
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	b := With(context.Background(), writeKB)
	user := context.Background()
	// golib's local driver has no Copier: a driver with one is wrapped too, so Copy is checked
	for _, driver := range []vfs.FS{disk, copying{disk}} {
		fsys := tr.g.Wrap(func() string { return "kb" }, tr.root, driver)
		calls := methodCalls(fsys)
		for name, call := range calls {
			refusedFor(t, name+" of a hidden name", call(b, ".bashrc", "notes/z.md"), whyHidden)
			refusedFor(t, name+" to a hidden name", call(b, "notes/a.md", ".ssh/new"), whyHidden)
		}
		if err := calls["Stat"](user, ".bashrc", ""); err != nil {
			t.Errorf("the user's Stat: %v", err)
		}
	}
	if _, ok := tr.g.Wrap(func() string { return "kb" }, tr.root, copying{disk}).(vfs.Copier); !ok {
		t.Fatal("a driver with a Copier lost it")
	}
	var none *Guard
	nfs := none.Wrap(func() string { return "kb" }, tr.root, disk)
	if _, err := nfs.Stat(b, "notes/a.md"); !errors.Is(err, ErrRefused) {
		t.Errorf("no guard, bounded: %v", err)
	}
	if _, err := nfs.Stat(user, "notes/a.md"); err != nil {
		t.Errorf("no guard, the user's: %v", err)
	}
}

// methodCalls is each method of fsys and of its capabilities as one call shape: (ctx, a name, a
// second name). A method that takes one name is given the hidden one of the two.
func methodCalls(fsys vfs.FS) map[string]func(ctx context.Context, a, b string) error {
	x := strings.NewReader
	m := map[string]func(ctx context.Context, a, b string) error{
		"Stat":      func(ctx context.Context, a, _ string) error { _, err := fsys.Stat(ctx, a); return err },
		"ReadDir":   func(ctx context.Context, a, _ string) error { _, err := fsys.ReadDir(ctx, a); return err },
		"Open":      func(ctx context.Context, a, _ string) error { return closed(fsys.Open(ctx, a, 0)) },
		"WriteFile": func(ctx context.Context, a, _ string) error { _, err := fsys.WriteFile(ctx, a, x("x")); return err },
		"MkdirAll":  func(ctx context.Context, a, _ string) error { return fsys.MkdirAll(ctx, a) },
		"Remove":    func(ctx context.Context, a, _ string) error { return fsys.Remove(ctx, a) },
		"RemoveAll": func(ctx context.Context, a, _ string) error { return fsys.RemoveAll(ctx, a) },
		"Rename":    func(ctx context.Context, a, b string) error { return fsys.Rename(ctx, a, b) },
	}
	if c, ok := fsys.(vfs.ConditionalWriter); ok {
		m["WriteFileIf"] = func(ctx context.Context, a, _ string) error { _, err := c.WriteFileIf(ctx, a, x("x"), ""); return err }
		m["RemoveIf"] = func(ctx context.Context, a, _ string) error { return c.RemoveIf(ctx, a, "") }
	}
	if c, ok := fsys.(vfs.ExclusiveCreator); ok {
		m["CreateExclusive"] = func(ctx context.Context, a, _ string) error { _, err := c.CreateExclusive(ctx, a, x("x")); return err }
	}
	if c, ok := fsys.(vfs.NoReplaceRenamer); ok {
		m["RenameNoReplace"] = func(ctx context.Context, a, b string) error { return c.RenameNoReplace(ctx, a, b) }
	}
	if c, ok := fsys.(vfs.Copier); ok {
		m["Copy"] = func(ctx context.Context, a, b string) error { _, err := c.Copy(ctx, a, b); return err }
	}
	if c, ok := fsys.(vfs.Watcher); ok {
		m["Watch"] = func(ctx context.Context, a, _ string) error { _, err := c.Watch(ctx, a); return err }
	}
	for name, call := range m {
		if !twoNames[name] {
			call := call
			m[name] = func(ctx context.Context, a, b string) error {
				if strings.HasPrefix(b, ".") {
					return call(ctx, b, "")
				}
				return call(ctx, a, "")
			}
		}
	}
	return m
}

var twoNames = map[string]bool{"Rename": true, "RenameNoReplace": true, "Copy": true}

func closed(r io.ReadCloser, err error) error {
	if r != nil {
		_ = r.Close()
	}
	return err
}

// allCaps has every capability, for compose to be given or not.
type allCaps struct{}

func (allCaps) WriteFileIf(context.Context, string, io.Reader, vfs.Version, ...vfs.WriteOption) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, nil
}
func (allCaps) RemoveIf(context.Context, string, vfs.Version) error { return nil }
func (allCaps) CreateExclusive(context.Context, string, io.Reader, ...vfs.WriteOption) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, nil
}
func (allCaps) RenameNoReplace(context.Context, string, string) error { return nil }
func (allCaps) Copy(context.Context, string, string, ...vfs.WriteOption) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, nil
}
func (allCaps) Watch(context.Context, string, ...vfs.WatchOption) (<-chan vfs.Event, error) {
	return nil, nil
}

// TestWrapKeepsTheDriversCapabilities: for each of the 32 sets of capabilities a driver may have,
// the wrapped filesystem has exactly that set, so a caller's type assertion answers as the driver's.
func TestWrapKeepsTheDriversCapabilities(t *testing.T) {
	var c allCaps
	for mask := 0; mask < 32; mask++ {
		var (
			cw vfs.ConditionalWriter
			ec vfs.ExclusiveCreator
			nr vfs.NoReplaceRenamer
			cp vfs.Copier
			wa vfs.Watcher
		)
		if mask&1 != 0 {
			cw = c
		}
		if mask&2 != 0 {
			ec = c
		}
		if mask&4 != 0 {
			nr = c
		}
		if mask&8 != 0 {
			cp = c
		}
		if mask&16 != 0 {
			wa = c
		}
		got := compose(&boundedFS{}, cw, ec, nr, cp, wa)
		_, hasCW := got.(vfs.ConditionalWriter)
		_, hasEC := got.(vfs.ExclusiveCreator)
		_, hasNR := got.(vfs.NoReplaceRenamer)
		_, hasCP := got.(vfs.Copier)
		_, hasWA := got.(vfs.Watcher)
		if hasCW != (cw != nil) || hasEC != (ec != nil) || hasNR != (nr != nil) || hasCP != (cp != nil) || hasWA != (wa != nil) {
			t.Errorf("mask %05b: has %v %v %v %v %v", mask, hasCW, hasEC, hasNR, hasCP, hasWA)
		}
	}
}

// copying is a driver with a Copier, which copies nothing: what matters is whether Copy is reached.
type copying struct{ vfs.FS }

func (copying) Copy(context.Context, string, string, ...vfs.WriteOption) (vfs.FileInfo, error) {
	return vfs.FileInfo{}, nil
}

func mustGuard(t *testing.T, protected ...string) *Guard {
	t.Helper()
	g, err := NewGuard(protected...)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestARelativeProtectedPathIsNeverDropped: a relative path given to the guard is an error, and a
// call whose protect is relative is refused, so neither protects nothing in silence.
func TestARelativeProtectedPathIsNeverDropped(t *testing.T) {
	if _, err := NewGuard("cfg"); err == nil {
		t.Error("a relative protected path was taken")
	}
	tr := newTree(t)
	refusedFor(t, "a call protecting a relative folder", tr.check("notes/a.md", Bounds{Within: "kb", Protect: []string{"cfg"}}, opWrite), whyProtect)
}
