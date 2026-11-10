package todo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// Refs given in Windows' own form, through the verbs: an adr under the KB (`C:\…\kb\adrs\x.md`) is
// written as $KB_ROOT/adrs/x.md and found on the disk; a review on a share outside every root
// (`\\localhost\C$\…\r.md`) is kept in the slash form, the share its volume; an update's adr is
// read the same way.
func TestARefInWindowsOwnFormIsStoredInTheSlashForm(t *testing.T) {
	// the directory as a user names it: its long form (the temporary directory may be given in its
	// 8.3 short form, RUNNER~1, which core/todo does not resolve)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	kb, outside := filepath.Join(dir, "kb"), filepath.Join(dir, "outside")
	for _, d := range []string{filepath.Join(kb, ".todo-list", "open"), filepath.Join(kb, "adrs"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{filepath.Join(kb, "adrs", "x.md"), filepath.Join(kb, "adrs", "y.md"), filepath.Join(outside, "r.md")} {
		if err := os.WriteFile(f, []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	vol := filepath.VolumeName(dir)
	if len(vol) != 2 || vol[1] != ':' {
		t.Skipf("the temporary directory %s is not on a drive", dir)
	}
	share := `\\localhost\` + vol[:1] + `$` + outside[len(vol):] + `\r.md`
	if _, err := os.Stat(share); err != nil {
		t.Skipf("the administrative share is not reachable here: %v", err)
	}
	m := todo.New(todotest.Roots{"kb": kb}, localDisk{}, todotest.NewJournal())
	t.Cleanup(func() { m.Close() }) // before the directory is removed: Windows removes no directory held open
	ws := todo.At{Workspace: "kb"}
	if err := m.Claim(ctx, ws); err != nil {
		t.Fatal(err)
	}
	x := filepath.Join(kb, "adrs", "x.md")
	lower := strings.ToLower(x[:1]) + x[1:] // another spelling of the drive: no proof of identity
	task, err := m.Add(ctx, ws, todo.Spec{Title: "t", ADR: []string{x, lower}, Review: []string{share}}, "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(task.ADR) != 2 || task.ADR[0] != "$KB_ROOT/adrs/x.md" || task.ADR[1] != paths.Slash(lower) {
		t.Errorf("the adrs %q: want $KB_ROOT/adrs/x.md, then the lower-case drive's kept absolute (%s)", task.ADR, paths.Slash(lower))
	}
	if len(task.Review) != 1 || task.Review[0] != paths.Slash(share) || !strings.HasPrefix(strings.ToLower(task.Review[0]), "//localhost/") {
		t.Errorf("the review %q: want %q, the share kept", task.Review, paths.Slash(share))
	}
	// each ref resolves and is found: the adr under the KB, the lower-case drive's spelling of it, and
	// the review on the share
	refErrors(t, "added", task)
	updated, err := m.Update(ctx, ws, task.ID, task.Rev, todo.Patch{"adr": filepath.Join(kb, "adrs", "y.md")})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.ADR) != 1 || updated.ADR[0] != "$KB_ROOT/adrs/y.md" {
		t.Errorf("the updated adr %q: want $KB_ROOT/adrs/y.md", updated.ADR)
	}
	refErrors(t, "updated", updated)
	got, err := m.Get(ctx, ws, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	refErrors(t, "read back", got)
}

// refErrors fails for each adr or review error the task carries: a ref that did not resolve, or
// resolved to a path the disk does not have.
func refErrors(t *testing.T, when string, task todo.Task) {
	t.Helper()
	for _, e := range task.Errors {
		if strings.HasPrefix(e.Field, "adr[") || strings.HasPrefix(e.Field, "review[") {
			t.Errorf("%s: %s %s: %s", when, e.Field, e.Code, e.Message)
		}
	}
}
