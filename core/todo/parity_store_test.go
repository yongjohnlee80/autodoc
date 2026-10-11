package todo_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// TestReadingACopyNeverWrites is opt-in, with TestParityWithAStoreCopy: AUTODOC_TODO_PARITY_DIR
// is a COPY of a real <root>/.todo-list, its root a copy of the workspace. Listing it, its archive
// months and every task leaves every file's bytes and mtime as they were. With
// AUTODOC_TODO_PARITY_LEGACY_WORKSPACE it is claimed with that legacy $WORKSPACE, twice: the counts
// are what the cutover rewrites, and the second claim rewrites nothing. Then it is claimed and
// refreshed twice: the first refresh's summary is what the daemon would change on its first run,
// and the second changes nothing.
func TestReadingACopyNeverWrites(t *testing.T) {
	dir := os.Getenv("AUTODOC_TODO_PARITY_DIR")
	if dir == "" || filepath.Base(dir) != ".todo-list" {
		t.Skip("AUTODOC_TODO_PARITY_DIR is not set to a copy of a .todo-list")
	}
	home, _ := os.UserHomeDir()
	j := todotest.NewJournal()
	m := todo.New(todotest.Roots{"copy": filepath.Dir(dir)}, localDisk{}, j, todo.WithHome(home), todo.WithEnv(os.Getenv))
	at := todo.At{Workspace: "copy"}
	before := snapshotDir(t, dir)
	l, err := m.List(ctx, at, todo.ListOpts{})
	if err != nil {
		t.Fatal(err)
	}
	months, err := m.List(ctx, at, todo.ListOpts{Buckets: []string{"archived"}})
	if err != nil {
		t.Fatal(err)
	}
	archived := 0
	for _, p := range months.Partitions {
		ml, err := m.List(ctx, at, todo.ListOpts{Buckets: []string{"archived/" + p.Month}})
		if err != nil {
			t.Fatal(err)
		}
		archived += len(ml.Tasks)
		for _, task := range ml.Tasks {
			if _, err := m.Get(ctx, at, task.ID); err != nil {
				t.Errorf("get %s: %v", task.ID, err)
			}
		}
	}
	for _, task := range l.Tasks {
		if _, err := m.Get(ctx, at, task.ID); err != nil {
			t.Errorf("get %s: %v", task.ID, err)
		}
	}
	t.Logf("active tasks %d, malformed %d; archived tasks %d in %d months", len(l.Tasks), len(l.Malformed), archived, len(months.Partitions))
	for _, b := range l.Malformed {
		t.Logf("MALFORMED %s: %s", b.Path, b.Reason)
	}
	if after := snapshotDir(t, dir); !sameDir(t, before, after) {
		t.Fatal("reading the copy changed it")
	}
	if legacy := os.Getenv("AUTODOC_TODO_PARITY_LEGACY_WORKSPACE"); legacy != "" {
		cutover(t, m, j, at, dir, legacy)
	}
	if os.Getenv("AUTODOC_TODO_PARITY_REFRESH") == "" {
		return
	}
	if err := m.Claim(ctx, at); err != nil {
		t.Fatal(err)
	}
	first, err := m.Refresh(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("first refresh %+v", first)
	settled := snapshotDir(t, dir)
	second, err := m.Refresh(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if second.Moved+second.Rewritten+second.Archived+second.ErrorsSet != 0 || !sameDir(t, settled, snapshotDir(t, dir)) {
		t.Errorf("a second refresh changed the copy: %+v", second)
	}
}

type fileState struct {
	data  string
	mtime time.Time
}

func snapshotDir(t *testing.T, dir string) map[string]fileState {
	out := map[string]fileState{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(p)
		fi, _ := d.Info()
		out[p] = fileState{string(b), fi.ModTime()}
		return nil
	})
	return out
}

func sameDir(t *testing.T, a, b map[string]fileState) bool {
	same := len(a) == len(b)
	for p, s := range a {
		if b[p] != s {
			t.Logf("changed: %s", p)
			same = false
		}
	}
	return same
}

// cutover claims the copy with a legacy $WORKSPACE and reports what it rewrote, active and archived;
// a second claim must rewrite nothing.
func cutover(t *testing.T, m *todo.Manager, j *todotest.Journal, at todo.At, dir, legacy string) {
	r, err := m.Resolve(ctx, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Claim(ctx, at, todo.ClaimOpts{LegacyWorkspace: legacy}); err != nil {
		t.Fatal(err)
	}
	active, archived := 0, 0
	for _, c := range j.Rows(r.StoreID) {
		if c.Reason != "cutover: $WORKSPACE was "+legacy {
			continue
		}
		if strings.HasPrefix(c.ToPath, "archived/") {
			archived++
		} else {
			active++
		}
		t.Logf("CUTOVER %s %v", c.ToPath, c.Fields)
	}
	t.Logf("cutover rewrote %d tasks: %d active, %d archived", active+archived, active, archived)
	rows, settled := len(j.Rows(r.StoreID)), snapshotDir(t, dir)
	if err := m.Claim(ctx, at, todo.ClaimOpts{LegacyWorkspace: legacy}); err != nil {
		t.Fatal(err)
	}
	if len(j.Rows(r.StoreID)) != rows || !sameDir(t, settled, snapshotDir(t, dir)) {
		t.Error("a second cutover changed the copy")
	}
}
