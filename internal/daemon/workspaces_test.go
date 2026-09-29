package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// open is a daemon over a fresh store, stopped with the test.
func open(t *testing.T) (*Workspaces, *store.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(ctx, db, Options{Poll: 20 * time.Millisecond, BatchDelay: 5 * time.Millisecond})
	t.Cleanup(func() {
		m.StopAll()
		cancel()
		_ = db.Close()
	})
	return m, db
}

// indexed waits until the workspace's index holds docs notes and nothing pending.
func indexed(t *testing.T, m *Workspaces, name string, docs int64) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		w, ok := m.Get(name)
		if !ok || w.Index == nil {
			t.Fatalf("%s is not served", name)
		}
		if st, err := w.Index.Status(context.Background()); err == nil && st.Docs == docs && st.PendingJobs == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not index %d notes", name, docs)
		}
	}
}

func stored(t *testing.T, db *store.Store) int {
	t.Helper()
	ws, err := db.Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(ws)
}

// TestRemoveThatFailsLeavesTheWorkspace: a remove whose delete fails (here, the request is
// cancelled) leaves the workspace stored, listed and served, so the remove can be asked again;
// asked again, it goes, and a third time there is nothing to remove.
func TestRemoveThatFailsLeavesTheWorkspace(t *testing.T) {
	m, db := open(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 1)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Remove(cancelled, "kb"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Remove on a cancelled request = %v, want context.Canceled", err)
	}
	if n := stored(t, db); n != 1 {
		t.Fatalf("the store has %d workspaces after the failed remove, want 1", n)
	}
	if got := m.List(); len(got) != 1 || got[0].Name != "kb" || got[0].Err != nil {
		t.Fatalf("after the failed remove the daemon lists %+v, want kb, served", got)
	}
	// served again, not only listed: a note written now is followed and indexed
	if err := os.WriteFile(filepath.Join(root, "b.md"), []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 2)

	if err := m.Remove(context.Background(), "kb"); err != nil {
		t.Fatalf("the retried remove: %v", err)
	}
	if n, l := stored(t, db), len(m.List()); n != 0 || l != 0 {
		t.Fatalf("after the remove: %d stored, %d listed; want none", n, l)
	}
	if err := m.Remove(context.Background(), "kb"); !errors.Is(err, store.ErrNoWorkspace) {
		t.Fatalf("a remove of a removed workspace = %v, want ErrNoWorkspace", err)
	}
}

// TestRemoveOfAWorkspaceTheStoreLost: when the store no longer has the row (it went behind the
// daemon's back), the remove says so, and the daemon drops it too.
func TestRemoveOfAWorkspaceTheStoreLost(t *testing.T) {
	m, db := open(t)
	w, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := db.Workspaces(context.Background())
	if err != nil || len(ws) != 1 || ws[0].Name != w.Name {
		t.Fatalf("stored %+v, %v", ws, err)
	}
	if err := db.RemoveWorkspace(context.Background(), ws[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(context.Background(), "kb"); !errors.Is(err, store.ErrNoWorkspace) {
		t.Fatalf("Remove of a row the store lost = %v, want ErrNoWorkspace", err)
	}
	if got := m.List(); len(got) != 0 {
		t.Fatalf("the daemon still lists %+v", got)
	}
}
