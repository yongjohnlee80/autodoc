package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

// A ROOT HOLDING AUTODOC'S OWN FOLDERS (docs/decisions/contract.md#a-root-holding-autodocs-own-folders).
// AutoDoc's config folder (its Lua, plugins, themes, keymap and config), its state folder and its
// data folder (the store) are AutoDoc's alone. A workspace whose root holds one of them, or lies
// inside one, is still added and served, but it is named: a gated script with the workspace's grants
// could reach those files but for the protected-path list.

// OwnDir is one of AutoDoc's own folders: Name is "config", "state" or "data".
type OwnDir struct{ Name, Path string }

// Overlap is a root's overlap with one of AutoDoc's own folders: Dir is the folder's name,
// Relation "holds" (the folder is the root or under it) or "inside" (the root is under the
// folder), Path the folder where it leads.
type Overlap struct{ Dir, Relation, Path string }

// Overlaps is root's overlaps with own, in own's order. The root is compared where it leads now (a
// symlink can change after the root was added); own is as given, canonical already
// (CanonicalDirs). Containment is by whole path elements: a root that only shares a prefix with a
// folder overlaps nothing.
func Overlaps(root string, own []OwnDir) []Overlap {
	r := canonical(root)
	var out []Overlap
	for _, d := range own {
		switch {
		case d.Path == "":
		case within(d.Path, r):
			out = append(out, Overlap{Dir: d.Name, Relation: "holds", Path: d.Path})
		case within(r, d.Path):
			out = append(out, Overlap{Dir: d.Name, Relation: "inside", Path: d.Path})
		}
	}
	return out
}

// CanonicalDirs is own, each path where it leads (canonical).
func CanonicalDirs(own []OwnDir) []OwnDir {
	out := make([]OwnDir, 0, len(own))
	for _, d := range own {
		if d.Path != "" {
			d.Path = canonical(d.Path)
		}
		out = append(out, d)
	}
	return out
}

// canonical is p absolute and cleaned, its symlinks resolved as far as it exists: a folder not
// made yet (the state folder before the daemon's first write there) keeps its missing tail as
// written, under its nearest existing parent resolved.
func canonical(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	var tail []string
	for cur := abs; ; {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(append([]string{real}, tail...)...)
		} else if !os.IsNotExist(err) {
			return abs
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		tail = append([]string{filepath.Base(cur)}, tail...)
		cur = parent
	}
}

// within reports whether p is dir or under it, by whole path elements.
func within(p, dir string) bool {
	if p == dir {
		return true
	}
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
