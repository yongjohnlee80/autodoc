package tui

import (
	"slices"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// The explorer, a feature of its own: it lists a workspace's files the first time its row opens,
// and from then on takes the host's listings of the workspace in use, replacing the rows only when
// the files changed; the workspaces changing forget what it listed.
func TestTheExplorerTakesTheHostsListings(t *testing.T) {
	c := newScripted("kb")
	c.answers["index.list"] = func(params []any) (any, error) {
		return map[string]any{"docs": []any{map[string]any{"path": "a.md"}, map[string]any{"path": "dir/b.md"}}}, nil
	}
	e := newExplorer(c)
	c.spaces = []Workspace{{Name: "kb", State: "ready"}, {Name: "notes", State: "ready"}}
	e.Listed()
	if n := e.model.RowCount(nil); n != 2 {
		t.Fatalf("top level: %d rows, want the 2 workspaces", n)
	}
	kb := tuidecl.Index{Row: 0}
	e.fetch(kb)
	dir := tuidecl.Index{Row: 0, Parent: &kb}
	if e.model.RowCount(&kb) != 2 || e.model.Key(dir) != dirKey("kb", "dir") {
		t.Fatalf("kb opened: %d rows, the first %q", e.model.RowCount(&kb), e.model.Key(dir))
	}
	e.fetch(dir)
	open := func() bool { return e.model.RowCount(&dir) == 1 }
	if !open() {
		t.Fatal("dir/ opened without its file")
	}

	// the host lists the same files: nothing replaced, the folder stays open
	c.listing = FileList{Paths: []string{"a.md", "dir/b.md"}, Loaded: true, Gen: 1}
	e.Listed()
	if !open() {
		t.Fatal("the same files listed again closed dir/")
	}
	// a file added: the rows under kb are the new list
	c.listing = FileList{Paths: []string{"a.md", "c.md", "dir/b.md"}, Loaded: true, Gen: 2}
	e.Listed()
	if n := e.model.RowCount(&kb); n != 3 {
		t.Fatalf("after c.md was listed kb has %d rows, want 3", n)
	}
	// a workspace the explorer never opened: nothing listed under it
	c.ws, c.listing = "notes", FileList{Paths: []string{"n.md"}, Loaded: true, Gen: 3}
	e.Listed()
	if _, listed := e.paths["notes"]; listed {
		t.Fatal("a listing of notes was taken before its row opened")
	}
	// the workspaces changing: the top level again, nothing listed under it
	c.spaces = []Workspace{{Name: "kb", State: "ready"}, {Name: "notes", State: "indexing"}}
	e.Listed()
	if len(e.paths) != 0 || e.model.RowCount(&kb) != 0 {
		t.Fatalf("the workspaces changed and the explorer kept %v", e.paths)
	}
}

// Enter on a file opens it and closes the drawer; on a workspace or a folder, it opens or closes
// the row.
func TestTheExplorerOpensWhatIsActivated(t *testing.T) {
	c := newScripted("kb")
	c.answers["index.list"] = func([]any) (any, error) {
		return map[string]any{"docs": []any{map[string]any{"path": "a.md"}}}, nil
	}
	e := newExplorer(c)
	c.spaces = []Workspace{{Name: "kb", State: "ready"}}
	e.Listed()
	kb := tuidecl.Index{Row: 0}
	e.fetch(kb)
	if err := e.activated(kb); err != nil {
		t.Fatal(err)
	}
	if err := e.activated(tuidecl.Index{Row: 0, Parent: &kb}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"explorerTree.toggleExpanded", "explorer.close"}; !slices.Equal(c.views, want) {
		t.Errorf("views called: %v, want %v", c.views, want)
	}
	if !slices.Equal(c.files, []string{"kb:a.md"}) {
		t.Errorf("opened %v, want kb:a.md", c.files)
	}
}
