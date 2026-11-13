package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// TestAnInvalidNameIsRefusedAndNothingChanges: a name no workspace may have ("a/b") is refused with
// ErrInvalidSpec by Rename and by Configure, each on a workspace of its own. The workspace stays
// served and stored under the name and root it had, with the indexer it had, and no workspace of
// the refused name exists, served or stored.
func TestAnInvalidNameIsRefusedAndNothingChanges(t *testing.T) {
	const bad = "a/b"
	ctx := context.Background()
	for name, change := range map[string]func(m *Workspaces) error{
		"Rename":    func(m *Workspaces) error { return m.Rename(ctx, "kb", bad) },
		"Configure": func(m *Workspaces) error { return m.Configure(ctx, "kb", store.Changes{Name: ptr(bad)}) },
	} {
		t.Run(name, func(t *testing.T) {
			m, db := open(t)
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# A\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Add(ctx, workspace.Spec{Name: "kb", Root: root}); err != nil {
				t.Fatal(err)
			}
			before, _ := m.Get("kb")

			if err := change(m); !errors.Is(err, workspace.ErrInvalidSpec) {
				t.Errorf("%s to %q: %v; want ErrInvalidSpec", name, bad, err)
			}

			after, ok := m.Get("kb")
			if !ok {
				t.Fatalf("kb is no longer served")
			}
			if after.Name() != "kb" || after.Root() != root || after.Err() != nil || after.Index() != before.Index() {
				t.Errorf("kb after the refusal: name %q, root %q, err %v, the same indexer %v; want it as it was",
					after.Name(), after.Root(), after.Err(), after.Index() == before.Index())
			}
			if _, ok := m.Get(bad); ok {
				t.Errorf("a workspace named %q is served", bad)
			}
			if got := len(m.List()); got != 1 {
				t.Errorf("%d workspaces served; want kb alone", got)
			}
			stored, err := db.Workspaces(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(stored) != 1 || stored[0].Name != "kb" || stored[0].Root != root {
				t.Errorf("stored: %+v; want kb alone, at %s", stored, root)
			}
		})
	}
}
