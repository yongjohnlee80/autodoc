package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// indexed waits until the workspace's index holds docs files and nothing pending.
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
	// served again, not only listed: a file written now is followed and indexed
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

func TestWorkspaceSectionSizeReindexesOnlyTheNamedWorkspace(t *testing.T) {
	m, db := open(t)
	for _, name := range []string{"a", "b"} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "n.md"), []byte("# Note\n\n"+strings.Repeat("many words and details.\n\n", 100)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Add(context.Background(), config.Workspace{Name: name, Root: root}); err != nil {
			t.Fatal(err)
		}
		indexed(t, m, name, 1)
	}
	if err := m.SetSectionTokens(context.Background(), "absent", 256); !errors.Is(err, store.ErrNoWorkspace) {
		t.Fatalf("missing workspace: %v", err)
	}
	if err := m.SetSectionTokens(context.Background(), "a", 12); err == nil {
		t.Fatal("out-of-range section size accepted")
	}
	if err := m.SetSectionTokens(context.Background(), "a", 256); err != nil {
		t.Fatal(err)
	}
	ws, err := db.Workspaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range ws {
		size, err := db.SectionTokens(context.Background(), w.ID)
		want := 512
		if w.Name == "a" {
			want = 256
		}
		if err != nil || size != want {
			t.Errorf("%s section size = %d, %v; want %d", w.Name, size, err, want)
		}
		for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			var version string
			err := db.Read(context.Background(), func(tx *store.Tx) error {
				d, err := db.Workspace(w.ID).Documents(tx).With(store.DocPath, "n.md").Get(store.DocIndexer)
				if err == nil {
					version = d.Indexer
				}
				return err
			})
			if err == nil && strings.HasSuffix(version, fmt.Sprintf(".t%d", want)) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s did not index under section size %d: %q, %v", w.Name, want, version, err)
			}
		}
	}
}

func TestSetPatternsReconcilesAndRejectsMalformedEdits(t *testing.T) {
	manager, db := open(t)
	root := t.TempDir()
	for path, content := range map[string]string{"note.md": "# Note\n", "readme.txt": "plain text\n"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.Add(context.Background(), config.Workspace{Name: "kb", Root: root, Include: []string{"**/*.md"}}); err != nil {
		t.Fatal(err)
	}
	indexed(t, manager, "kb", 1)
	if err := manager.SetPatterns(context.Background(), "kb", []string{"**/*.{md,txt"}, nil); err == nil {
		t.Fatal("accepted malformed brace pattern")
	}
	if got := manager.List()[0].Include; len(got) != 1 || got[0] != "**/*.md" {
		t.Fatalf("invalid pattern changed the running matcher: %v", got)
	}
	if err := manager.SetPatterns(context.Background(), "kb", []string{"**/*.{md,txt}"}, nil); err != nil {
		t.Fatal(err)
	}
	indexed(t, manager, "kb", 2)
	if err := manager.SetPatterns(context.Background(), "kb", []string{"**/*.txt"}, nil); err != nil {
		t.Fatal(err)
	}
	indexed(t, manager, "kb", 1)
	if err := manager.SetPatterns(context.Background(), "kb", []string{}, nil); err != nil {
		t.Fatal(err)
	}
	indexed(t, manager, "kb", 0)
	if got := manager.List()[0].Include; got == nil || len(got) != 0 {
		t.Fatalf("running blank include = %v", got)
	}
	workspaces, err := db.Workspaces(context.Background())
	if err != nil || len(workspaces) != 1 || workspaces[0].Include == nil || len(workspaces[0].Include) != 0 {
		t.Fatalf("stored patterns = %+v, %v", workspaces, err)
	}
}
