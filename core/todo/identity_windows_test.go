package todo_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// caseSensitive makes dir case-sensitive (fsutil setCaseSensitiveInfo), or skips where Windows can't.
func caseSensitive(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("fsutil.exe", "file", "setCaseSensitiveInfo", dir, "enable").CombinedOutput(); err != nil {
		t.Skipf("this Windows makes no case-sensitive folder here: %v %s", err, out)
	}
}

// shortName is p's 8.3 short name (GetShortPathName), "" when the volume keeps none for it.
func shortName(t *testing.T, p string) string {
	t.Helper()
	long, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		return ""
	}
	s := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(s, p) {
		return ""
	}
	return s
}

// longDir is a new directory under the test's temporary one, by its long name: the spelling the
// disk answers.
func longDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// workspaceFolder makes a workspace folder with a store and an ADR in it.
func workspaceFolder(t *testing.T, dir string) {
	t.Helper()
	for _, d := range []string{filepath.Join(dir, ".todo-list", "open"), filepath.Join(dir, "adrs")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "adrs", "x.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// In a case-sensitive folder kb and KB are two folders. With kb served, KB's store is no workspace's
// (not writable, not kb's), a file in KB belongs to no workspace, and a ref into KB is not written
// as kb's $KB_ROOT: identity is the disk's, never a match by case.
func TestACaseSensitiveFoldersSiblingIsNotTheServedRoot(t *testing.T) {
	base := longDir(t)
	caseSensitive(t, base)
	kb, other := filepath.Join(base, "kb"), filepath.Join(base, "KB")
	workspaceFolder(t, kb)
	workspaceFolder(t, other)
	m := todo.New(todotest.Roots{"kb": kb}, localDisk{}, todotest.NewJournal())
	t.Cleanup(func() { m.Close() }) // before the directory is removed: Windows removes no directory held open
	ws := todo.At{Workspace: "kb"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatal(err)
	}
	r, err := m.Resolve(ctx, todo.At{Root: other})
	if err != nil {
		t.Fatalf("resolve KB: %v", err)
	}
	if r.Writable || r.Root != paths.Slash(other) {
		t.Errorf("KB's store %+v: want not writable, at KB's own root %s", r, paths.Slash(other))
	}
	if name, root, err := m.Owner(ctx, filepath.Join(other, "adrs", "x.md")); err != nil || name != "" {
		t.Errorf("a file in KB belongs to %q (%s), %v: want no workspace", name, root, err)
	}
	ref := filepath.Join(other, "adrs", "x.md")
	task, err := m.Add(ctx, ws, todo.Spec{Title: "t", ADR: []string{ref}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.ADR) != 1 || task.ADR[0] != paths.Slash(ref) {
		t.Errorf("a ref into KB written %q: want it absolute, %s, never kb's $KB_ROOT", task.ADR, paths.Slash(ref))
	}
}

// A served root given in another spelling of the same folder (its drive letter in lower case; its
// 8.3 short name) is that folder: its store is the workspace's, writable, and a ref under it in the
// disk's spelling is written as $KB_ROOT. Each spelling has a folder and a Manager of its own.
func TestAServedRootSpelledAnotherWayIsTheSameFolder(t *testing.T) {
	t.Run("a lower-case drive letter", func(t *testing.T) {
		kb := filepath.Join(longDir(t), "knowledge-base-folder")
		workspaceFolder(t, kb)
		servedAs(t, kb, strings.ToLower(kb[:1])+kb[1:])
	})
	t.Run("its 8.3 short name", func(t *testing.T) {
		// the runner gives its temporary folder by its short name (C:\Users\RUNNER~1\…): that spelling
		// first, then the folder's own short name where the volume keeps one
		raw := t.TempDir()
		long, err := filepath.EvalSymlinks(raw)
		if err != nil {
			t.Fatal(err)
		}
		kb := filepath.Join(long, "knowledge-base-folder") // long enough to be given an 8.3 short name
		workspaceFolder(t, kb)
		root := filepath.Join(raw, "knowledge-base-folder")
		if !strings.Contains(root, "~") {
			root = shortName(t, kb) // the temporary folder is not given short: the folder's own short name
		}
		if root == "" {
			t.Skipf("this volume keeps no 8.3 short name: neither the temporary folder %s nor %s has one", raw, kb)
		}
		if !strings.Contains(root, "~") {
			t.Fatalf("the short spelling %s of %s holds no 8.3 name", root, kb)
		}
		t.Logf("served as %s, the folder %s", root, kb)
		servedAs(t, kb, root)
	})
}

// servedAs serves the folder kb as the workspace root root, another spelling of it, and checks the
// workspace's store is kb's, writable, and a ref under kb is written as $KB_ROOT.
func servedAs(t *testing.T, kb, root string) {
	t.Helper()
	m := todo.New(todotest.Roots{"kb": root}, localDisk{}, todotest.NewJournal())
	t.Cleanup(func() { m.Close() }) // before the directory is removed: Windows removes no directory held open
	ws := todo.At{Workspace: "kb"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatalf("claim with the root spelled %s: %v", root, err)
	}
	r, err := m.Resolve(ctx, ws)
	if err != nil || !r.Writable || r.Root != paths.Slash(kb) {
		t.Errorf("the store with the root spelled %s: %+v, %v; want writable at %s", root, r, err, paths.Slash(kb))
	}
	task, err := m.Add(ctx, ws, todo.Spec{Title: "t", ADR: []string{filepath.Join(kb, "adrs", "x.md")}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(task.ADR) != 1 || task.ADR[0] != "$KB_ROOT/adrs/x.md" {
		t.Errorf("with the root spelled %s, the adr %q: want $KB_ROOT/adrs/x.md", root, task.ADR)
	}
}
