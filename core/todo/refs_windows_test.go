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
	lower := strings.ToLower(x[:1]) + x[1:] // a drive letter in lower case, as some shells give it
	task, err := m.Add(ctx, ws, todo.Spec{Title: "t", ADR: []string{x, lower}, Review: []string{share}}, "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if len(task.ADR) != 2 || task.ADR[0] != "$KB_ROOT/adrs/x.md" || task.ADR[1] != "$KB_ROOT/adrs/x.md" {
		t.Errorf("the adrs %q: want both $KB_ROOT/adrs/x.md, the lower-case drive's too", task.ADR)
	}
	if len(task.Review) != 1 || task.Review[0] != paths.Slash(share) || !strings.HasPrefix(strings.ToLower(task.Review[0]), "//localhost/") {
		t.Errorf("the review %q: want %q, the share kept", task.Review, paths.Slash(share))
	}
	for _, e := range task.Errors {
		if strings.HasPrefix(e.Field, "adr[") {
			t.Errorf("the adr under the KB was not found: %+v", e)
		}
	}
	updated, err := m.Update(ctx, ws, task.ID, task.Rev, todo.Patch{"adr": filepath.Join(kb, "adrs", "y.md")})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.ADR) != 1 || updated.ADR[0] != "$KB_ROOT/adrs/y.md" {
		t.Errorf("the updated adr %q: want $KB_ROOT/adrs/y.md", updated.ADR)
	}
}
