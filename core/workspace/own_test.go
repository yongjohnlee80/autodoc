package workspace_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// ownDirs is AutoDoc's own folders under base, as a daemon is told them: config and data made,
// state not made yet (a daemon before its first write there).
func ownDirs(t *testing.T, base string) []workspace.OwnDir {
	t.Helper()
	for _, d := range []string{"cfg/autodoc", "share/autodoc"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return []workspace.OwnDir{
		{Name: "config", Path: filepath.Join(base, "cfg/autodoc")},
		{Name: "state", Path: filepath.Join(base, "state/autodoc")},
		{Name: "data", Path: filepath.Join(base, "share/autodoc")},
	}
}

// A root that holds AutoDoc's own folders, or lies inside one, is named with each: which folder,
// which way, and where. A sibling root, or one that only shares a prefix with a folder, is not. A
// folder not made yet is still matched by its path, and a root reached through a symlink is
// compared where it leads.
func TestARootHoldingOrInsideAutoDocsOwnFoldersIsNamed(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	own := ownDirs(t, base)
	cfg, state, data := own[0].Path, own[1].Path, own[2].Path
	for _, d := range []string{"cfg/autodoc-notes", "notes", "share/autodoc/kb"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "home-link")
	if err := os.Symlink(base, link); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, root string
		want       []workspace.Overlap
	}{
		{"a root holding all three", base, []workspace.Overlap{
			{Dir: "config", Relation: "holds", Path: cfg}, {Dir: "state", Relation: "holds", Path: state}, {Dir: "data", Relation: "holds", Path: data}}},
		{"the config folder's parent", filepath.Join(base, "cfg"), []workspace.Overlap{{Dir: "config", Relation: "holds", Path: cfg}}},
		{"the state folder's parent, the folder not made yet", filepath.Join(base, "state"), []workspace.Overlap{{Dir: "state", Relation: "holds", Path: state}}},
		{"the data folder itself", data, []workspace.Overlap{{Dir: "data", Relation: "holds", Path: data}}},
		{"a root inside the data folder", filepath.Join(data, "kb"), []workspace.Overlap{{Dir: "data", Relation: "inside", Path: data}}},
		{"a root through a symlink to one holding them", link, []workspace.Overlap{
			{Dir: "config", Relation: "holds", Path: cfg}, {Dir: "state", Relation: "holds", Path: state}, {Dir: "data", Relation: "holds", Path: data}}},
		{"a sibling root", filepath.Join(base, "notes"), nil},
		{"a root sharing the config folder's prefix", filepath.Join(base, "cfg/autodoc-notes"), nil},
	} {
		if got := workspace.Overlaps(c.root, own); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s (%s): %+v, want %+v", c.name, c.root, got, c.want)
		}
	}
}

// The folders a daemon is told are compared where they lead too: a config folder named through a
// symlink is found under the root that holds its target.
func TestAutoDocsOwnFoldersAreComparedWhereTheyLead(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(base, "real", "autodoc")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "cfg-link")
	if err := os.Symlink(filepath.Join(base, "real"), link); err != nil {
		t.Fatal(err)
	}
	own := workspace.CanonicalDirs([]workspace.OwnDir{{Name: "config", Path: filepath.Join(link, "autodoc")}})
	want := []workspace.Overlap{{Dir: "config", Relation: "holds", Path: real}}
	if got := workspace.Overlaps(filepath.Join(base, "real"), own); !reflect.DeepEqual(got, want) {
		t.Errorf("%+v, want %+v", got, want)
	}
}
