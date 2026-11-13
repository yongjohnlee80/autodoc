package explorer

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// The explorer, a feature of its own: it lists a workspace's files the first time its row opens,
// and from then on takes the host's listings of the workspace in use, replacing the rows only when
// the files changed; the workspaces changing forget what it listed.
func TestTheExplorerTakesTheHostsListings(t *testing.T) {
	c := newScripted("kb")
	c.Answers["index.list"] = func(params []any) (any, error) {
		return map[string]any{"docs": []any{map[string]any{"path": "a.md"}, map[string]any{"path": "dir/b.md"}}}, nil
	}
	e := NewExplorer(c)
	c.Spaces = []feature.Workspace{{Name: "kb", State: "ready"}, {Name: "notes", State: "ready"}}
	e.Listed()
	if n := e.Model.RowCount(nil); n != 2 {
		t.Fatalf("top level: %d rows, want the 2 workspaces", n)
	}
	kb := tuidecl.Index{Row: 0}
	e.fetch(kb)
	dir := tuidecl.Index{Row: 0, Parent: &kb}
	if e.Model.RowCount(&kb) != 2 || e.Model.Key(dir) != dirKey("kb", "dir") {
		t.Fatalf("kb opened: %d rows, the first %q", e.Model.RowCount(&kb), e.Model.Key(dir))
	}
	e.fetch(dir)
	open := func() bool { return e.Model.RowCount(&dir) == 1 }
	if !open() {
		t.Fatal("dir/ opened without its file")
	}

	// the host lists the same files: nothing replaced, the folder stays open
	c.Listing = feature.FileList{Paths: []string{"a.md", "dir/b.md"}, Loaded: true, Gen: 1}
	e.Listed()
	if !open() {
		t.Fatal("the same files listed again closed dir/")
	}
	// a file added: the rows under kb are the new list
	c.Listing = feature.FileList{Paths: []string{"a.md", "c.md", "dir/b.md"}, Loaded: true, Gen: 2}
	e.Listed()
	if n := e.Model.RowCount(&kb); n != 3 {
		t.Fatalf("after c.md was listed kb has %d rows, want 3", n)
	}
	// a workspace the explorer never opened: nothing listed under it
	c.WS, c.Listing = "notes", feature.FileList{Paths: []string{"n.md"}, Loaded: true, Gen: 3}
	e.Listed()
	if _, listed := e.Paths["notes"]; listed {
		t.Fatal("a listing of notes was taken before its row opened")
	}
	// the workspaces changing: the top level again, nothing listed under it
	c.Spaces = []feature.Workspace{{Name: "kb", State: "ready"}, {Name: "notes", State: "indexing"}}
	e.Listed()
	if len(e.Paths) != 0 || e.Model.RowCount(&kb) != 0 {
		t.Fatalf("the workspaces changed and the explorer kept %v", e.Paths)
	}
}

// Enter on a file opens it and closes the drawer; on a workspace or a folder, it opens or closes
// the row. SPC e opens or closes the drawer.
func TestTheExplorerOpensWhatIsActivated(t *testing.T) {
	c := newScripted("kb")
	c.Answers["index.list"] = func([]any) (any, error) {
		return map[string]any{"docs": []any{map[string]any{"path": "a.md"}}}, nil
	}
	e := NewExplorer(c)
	c.Spaces = []feature.Workspace{{Name: "kb", State: "ready"}}
	e.Listed()
	kb := tuidecl.Index{Row: 0}
	e.fetch(kb)
	if err := e.activated(kb); err != nil {
		t.Fatal(err)
	}
	if err := e.activated(tuidecl.Index{Row: 0, Parent: &kb}); err != nil {
		t.Fatal(err)
	}
	e.toggle()
	if want := []string{"explorerTree.toggleExpanded", "explorer.close", "explorer.toggle(0)"}; !slices.Equal(c.Views, want) {
		t.Errorf("views called: %v, want %v", c.Views, want)
	}
	if !slices.Equal(c.OpenedFiles, []string{"kb:a.md"}) {
		t.Errorf("opened %v, want kb:a.md", c.OpenedFiles)
	}
}
