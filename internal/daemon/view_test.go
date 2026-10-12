package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// TestARenameLeavesAnEarlierViewItsName: rpc reads a served workspace through a view; a rename
// gives it a new one, so a request that already held the workspace keeps the name it began with,
// while the workspace is served under the new name with the same indexer.
func TestARenameLeavesAnEarlierViewItsName(t *testing.T) {
	m, _ := open(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "a", Root: root}); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Get("a")
	if err := m.Rename(context.Background(), "a", "b"); err != nil {
		t.Fatal(err)
	}
	after, ok := m.Get("b")
	if !ok || after.Name() != "b" || before.Name() != "a" || after.Index() != before.Index() {
		t.Fatalf("after the rename: %v %q, the earlier view %q, one indexer %v", ok, after.Name(), before.Name(), after.Index() == before.Index())
	}
	if _, _, ok := after.EmbeddingQueue(); !ok {
		t.Error("a served workspace has no place in the queue")
	}
	if _, ok := after.Following(); !ok {
		t.Error("a served workspace has no follower")
	}
	if _, ok := after.Databases(); !ok {
		t.Error("a served workspace keeps no databases")
	}
	if _, _, ok := after.FrontmatterSchema(); !ok {
		t.Error("a served workspace keeps no schema")
	}
	if after.SectionTokens() != store.SectionTokensDefault || after.EmbeddingPolicy() == "" {
		t.Errorf("settings %d %q", after.SectionTokens(), after.EmbeddingPolicy())
	}
	after.Searched()
}

// TestAWorkspaceNotOpenedAnswersOnlyWhatItIs: one whose root could not be opened answers its name,
// root, patterns and error, and keeps nothing else.
func TestAWorkspaceNotOpenedAnswersOnlyWhatItIs(t *testing.T) {
	m, _ := open(t)
	s := m.serve(1, workspace.Spec{Name: "gone", Root: filepath.Join(t.TempDir(), "missing"), Include: []string{"**/*.md"}})
	w := s.view
	if w.Name() != "gone" || w.Err() == nil || w.Index() != nil || w.Docs() != nil || len(w.Include()) != 1 || w.Exclude() != nil {
		t.Fatalf("not opened: %q %v %v %v %v %v", w.Name(), w.Err(), w.Index(), w.Docs(), w.Include(), w.Exclude())
	}
	_, following := w.Following()
	_, _, queued := w.EmbeddingQueue()
	_, _, schema := w.FrontmatterSchema()
	_, dbs := w.Databases()
	if following || queued || schema || dbs || w.Warming() != nil || w.TextExtensions() != nil || w.TextCollisions() != nil ||
		w.SectionTokens() != 0 || w.EmbeddingPolicy() != "" || w.Provider().Override != "" {
		t.Errorf("a workspace not opened answered more than what it is")
	}
	w.Searched() // a no-op
	if w.Root() == "" {
		t.Error("no root")
	}
}
