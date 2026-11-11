package tui

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAPackageFolderIsHeldByNoRead: a package's folder read through packageFS (its files whole,
// their information, a file opened) is held by nothing between reads: it can be renamed and
// removed, on every OS (Windows refuses either while a root holds the folder), and on Linux no
// descriptor of the process names it (the mutant: the root kept open).
func TestAPackageFolderIsHeldByNoRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pkg")
	writeFiles(t, dir, map[string]string{"init.lua": "return 1", "lib/util.lua": "return 2"})
	pkg := packageFS(dir)
	if b, err := pkg.ReadFile("init.lua"); err != nil || string(b) != "return 1" {
		t.Fatalf("ReadFile: %q, %v", b, err)
	}
	if fi, err := pkg.Stat("lib/util.lua"); err != nil || fi.Size() != int64(len("return 2")) {
		t.Fatalf("Stat: %v, %v", fi, err)
	}
	f, err := pkg.Open("lib/util.lua")
	if err != nil {
		t.Fatal(err)
	}
	if b, err := io.ReadAll(f); err != nil || string(b) != "return 2" {
		t.Fatalf("the file Open answered: %q, %v", b, err)
	}
	if b, err := fs.ReadFile(pkg, "lib/util.lua"); err != nil || string(b) != "return 2" { // as the engine's require reads
		t.Fatalf("fs.ReadFile: %q, %v", b, err)
	}
	if held := descriptorsNaming(t, dir); len(held) != 0 {
		t.Errorf("between reads, the process holds %v", held)
	}
	moved := dir + "-moved"
	if err := os.Rename(dir, moved); err != nil {
		t.Fatalf("the folder can't be renamed after its reads: %v", err)
	}
	if err := os.RemoveAll(moved); err != nil {
		t.Fatalf("the folder can't be removed after its reads: %v", err)
	}
}

// descriptorsNaming is the descriptors of this process whose target is dir or under it, where the
// OS lists them (Linux's /proc/self/fd); nil elsewhere, where the rename and the removal say it.
func descriptorsNaming(t *testing.T, dir string) []string {
	t.Helper()
	if runtime.GOOS != "linux" {
		return nil
	}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name()))
		if err == nil && (target == dir || strings.HasPrefix(target, dir+string(filepath.Separator))) {
			out = append(out, fd.Name()+" → "+target)
		}
	}
	return out
}

// TestAPackageReadAfterItsFolderMovedSaysSo: once the folder has moved or gone, a read through the
// package fails as a missing file; a folder put back at the same path is read anew, its new files.
func TestAPackageReadAfterItsFolderMovedSaysSo(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pkg")
	writeFiles(t, dir, map[string]string{"lib.lua": "return 'first'"})
	pkg := packageFS(dir)
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	if _, err := pkg.ReadFile("lib.lua"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a read of the moved folder: %v, want it missing", err)
	}
	writeFiles(t, dir, map[string]string{"lib.lua": "return 'second'"})
	if b, err := pkg.ReadFile("lib.lua"); err != nil || string(b) != "return 'second'" {
		t.Fatalf("the folder replaced at its path: %q, %v; want its new file", b, err)
	}
}

// TestAPackageReadsNothingOutsideItsFolder: through packageFS, a path out of the folder, an absolute
// one, a link that leads outside and, on Windows, a junction that does are refused; a folder is not
// a file to read.
func TestAPackageReadsNothingOutsideItsFolder(t *testing.T) {
	root := t.TempDir()
	dir, outside := filepath.Join(root, "pkg"), filepath.Join(root, "outside")
	writeFiles(t, dir, map[string]string{"init.lua": "return 1", "sub/x.lua": "return 2"})
	writeFiles(t, outside, map[string]string{"secret.lua": "return 'outside'"})
	pkg := packageFS(dir)
	refused := []string{"../outside/secret.lua", filepath.ToSlash(filepath.Join(outside, "secret.lua"))}
	if err := os.Symlink(filepath.Join(outside, "secret.lua"), filepath.Join(dir, "link.lua")); err == nil {
		refused = append(refused, "link.lua")
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(dir, "junction"), outside).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v: %s", err, out)
		}
		refused = append(refused, "junction/secret.lua")
	}
	for _, name := range refused {
		if b, err := pkg.ReadFile(name); err == nil {
			t.Errorf("%s read %q, want it refused", name, b)
		}
		if f, err := pkg.Open(name); err == nil {
			_ = f.Close()
			t.Errorf("%s opened, want it refused", name)
		}
	}
	if _, err := pkg.Open("sub"); err == nil {
		t.Error("the folder sub opened as a file")
	}
}

// TestARunningPackagesFolderCanBeMovedAndRemoved: while the program runs, a package's folder, its
// init run and one module required, can be renamed and removed (on every OS: on Windows nothing may
// hold it). The module already required stays the engine's; a module required first after the
// move fails cleanly, its require saying the file is missing, and nothing else of the program does.
func TestARunningPackagesFolderCanBeMovedAndRemoved(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"pkg/package.toml": "name = \"pkg\"\n",
		"pkg/early.lua":    `return "early"`,
		"pkg/late.lua":     `return "late"`,
		"pkg/init.lua": `local early = require("early")
autodoc.command{ name = "pkgmods", run = function(ed)
  local ok, v = pcall(require, "late")
  ed:insert(require("early") .. " " .. tostring(ok) .. " " .. tostring(v))
end }`,
	})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.connected(t)
	r.waitNamespaces(t) // the scripts started, the package's init run (its early module required)
	pkg, moved := filepath.Join(dir, "pkg"), filepath.Join(t.TempDir(), "pkg")
	if err := os.Rename(pkg, moved); err != nil {
		t.Fatalf("the running package's folder can't be renamed: %v", err)
	}
	if err := os.RemoveAll(moved); err != nil {
		t.Fatalf("the running package's folder can't be removed: %v", err)
	}
	r.page("", 1, 1)
	r.mustRun(t, "pkgmods")
	r.s.WaitFor(t, "the cached module, and the late one refused", func(sc string) bool {
		text := r.editorText()
		return strings.HasPrefix(text, "early false ") && strings.Contains(text, `require "late"`)
	})
	t.Logf("after the move: %q", r.editorText())
}
