package kbtype

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// The limits a type is read within (docs/decisions/contract.md#kb-types-are-git-repos-of-files).
// They bound what is read and applied, not the clone that staged it: that is the git helper's
// timeout's (rationale §R2).
const (
	MaxManifestSize = 64 << 10  // kbtype.toml
	MaxFileSize     = 256 << 10 // one file of tree/
	MaxTreeSize     = 4 << 20   // tree/'s files, together
	MaxFiles        = 1000      // tree/'s files
	MaxEntries      = 2000      // what the walk visits, folders included
)

// Format is the kbtype.toml format this build reads.
const Format = 1

// Manifest and Tree are a type repo's two parts; Operations is the file a type never holds.
const (
	Manifest   = "kbtype.toml"
	Tree       = "tree"
	Operations = "KB_OPERATIONS.md"
)

// ErrInvalid is a type that breaks a rule; the error's text names which.
var ErrInvalid = errors.New("kbtype: invalid")

// Type is a parsed, validated KB type.
type Type struct {
	Format      int      `toml:"format"`
	Name        string   `toml:"name"`
	Title       string   `toml:"title"`
	Description string   `toml:"description"`
	AutoDoc     string   `toml:"autodoc"`
	Workspace   Settings `toml:"workspace"`
	Folders     []Folder `toml:"folder"`
	// Files are tree/'s files by slash path, read by Read.
	Files map[string][]byte `toml:"-"`
}

// Settings are the workspace settings a type proposes; the Add-workspace dialog shows each, and the
// user may change it.
type Settings struct {
	Include         []string `toml:"include"`
	EmbeddingPolicy string   `toml:"embedding_policy"`
	Schema          string   `toml:"schema"` // relative to the KB root; one of Files when set
}

// Folder is one folder of the KB, in the order the dialog lists it.
type Folder struct {
	Path      string `toml:"path"`
	Purpose   string `toml:"purpose"`
	Immutable bool   `toml:"immutable"` // the user's untouched material: only its descriptor, only when new or empty
}

// Descriptor is folder d's descriptor: _ABOUT.md in a folder whose name starts with "_", where
// ABOUT.md would be the same file as _templates/about.md on a case-insensitive filesystem
// (scaffold.lua's about_name), and ABOUT.md elsewhere.
func Descriptor(d string) string {
	if strings.HasPrefix(path.Base(d), "_") {
		return path.Join(d, "_ABOUT.md")
	}
	return path.Join(d, "ABOUT.md")
}

var nameRule = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,63}$`) // the repo's name: autovim.kb

// policies are the embedding policies a type may propose (core/store/workspaces.go's).
var policies = []string{"always", "when opened", "never"}

// Read parses dir's kbtype.toml, reads dir/tree within the limits, and validates them. An error
// wraps ErrInvalid and names the rule broken.
func Read(dir string) (*Type, error) {
	src, err := readLimited(filepath.Join(dir, Manifest), MaxManifestSize)
	if err != nil {
		return nil, err
	}
	var t Type
	md, err := toml.Decode(string(src), &t)
	if err != nil {
		return nil, invalid("%s: %s", Manifest, firstLine(err.Error()))
	}
	if und := md.Undecoded(); len(und) > 0 {
		return nil, invalid("%s: unknown key %s", Manifest, und[0])
	}
	if t.Files, err = readTree(filepath.Join(dir, Tree)); err != nil {
		return nil, err
	}
	if err := t.validate(); err != nil {
		return nil, err
	}
	return &t, nil
}

// readLimited reads at most limit bytes of p; one more is a refusal, not a truncated read.
func readLimited(p string, limit int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, invalid("no %s at the repository's top", filepath.Base(p))
		}
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, invalid("%s is over %d bytes", filepath.Base(p), limit)
	}
	return b, nil
}

// readTree reads root's files by slash path. Anything but a regular file or a folder, a read
// error, or a limit passed fails the type closed.
func readTree(root string) (map[string][]byte, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, invalid("no %s/ folder at the repository's top", Tree)
	}
	files := map[string][]byte{}
	var entries, total int
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entries++; entries > MaxEntries {
			return invalid("%s/ has over %d entries", Tree, MaxEntries)
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		switch {
		case d.IsDir():
			return nil
		case !d.Type().IsRegular(): // a symlink, a device, a pipe: a "copy" could read outside the repo
			return invalid("%s/%s is not a regular file", Tree, rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > MaxFileSize {
			return invalid("%s/%s is over %d bytes", Tree, rel, MaxFileSize)
		}
		if total += int(info.Size()); total > MaxTreeSize {
			return invalid("%s/ is over %d bytes", Tree, MaxTreeSize)
		}
		if len(files) >= MaxFiles {
			return invalid("%s/ has over %d files", Tree, MaxFiles)
		}
		b, err := readLimited(p, MaxFileSize)
		if err != nil {
			return err
		}
		if !kind.Textual(b) {
			return invalid("%s/%s is not UTF-8 text", Tree, rel)
		}
		files[rel] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// validate checks t against the ADR's rules (§4.1), once its files are read.
func (t *Type) validate() error {
	if t.Format != Format {
		return invalid("%s: format %d; this AutoDoc reads format %d", Manifest, t.Format, Format)
	}
	if !nameRule.MatchString(t.Name) {
		return invalid("%s: name %q: lowercase letters, digits, . and -", Manifest, t.Name)
	}
	if strings.TrimSpace(t.Title) == "" {
		return invalid("%s: no title", Manifest)
	}
	if p := t.Workspace.EmbeddingPolicy; p != "" && !slices.Contains(policies, p) {
		return invalid("%s: embedding_policy %q: one of %s", Manifest, p, strings.Join(policies, ", "))
	}
	for rel := range t.Files {
		if !clean(rel) {
			return invalid("%s/%s: not a clean relative path", Tree, rel)
		}
	}
	if _, ok := t.Files[Operations]; ok {
		return invalid("%s/%s: that file is AutoDoc's own", Tree, Operations)
	}
	for _, f := range []string{"AGENTS.md", "ABOUT.md"} {
		if _, ok := t.Files[f]; !ok {
			return invalid("%s/%s is missing", Tree, f)
		}
	}
	if s := t.Workspace.Schema; s != "" {
		if _, ok := t.Files[s]; !ok || !clean(s) {
			return invalid("%s: schema %q is not a file of %s/", Manifest, s, Tree)
		}
	}
	listed := map[string]Folder{}
	for _, f := range t.Folders {
		if !clean(f.Path) {
			return invalid("%s: folder %q: not a clean relative path", Manifest, f.Path)
		}
		if _, dup := listed[f.Path]; dup {
			return invalid("%s: folder %q is listed twice", Manifest, f.Path)
		}
		if strings.TrimSpace(f.Purpose) == "" {
			return invalid("%s: folder %q has no purpose", Manifest, f.Path)
		}
		listed[f.Path] = f
		if _, ok := t.Files[Descriptor(f.Path)]; !ok {
			return invalid("%s/%s is missing: folder %q's descriptor", Tree, Descriptor(f.Path), f.Path)
		}
	}
	// an immutable folder has no subfolders (checked first, so the refusal names the folder)
	for _, f := range t.Folders {
		for d := path.Dir(f.Path); d != "."; d = path.Dir(d) {
			if listed[d].Immutable {
				return invalid("%s: folder %q is inside the immutable %q: an immutable folder has no subfolders", Manifest, f.Path, d)
			}
		}
	}
	// every folder under tree/ is listed, and an immutable folder's whole subtree holds only its
	// own descriptor: nothing a type seeds may land inside the user's untouched material, however deep
	for rel := range t.Files {
		for d := path.Dir(rel); d != "."; d = path.Dir(d) {
			if _, ok := listed[d]; !ok {
				return invalid("%s/%s: folder %q is not listed in %s", Tree, rel, d, Manifest)
			}
			if listed[d].Immutable && rel != Descriptor(d) {
				return invalid("%s/%s: folder %q is immutable: it holds only its descriptor", Tree, rel, d)
			}
		}
	}
	return nil
}

// clean reports whether p is a clean, relative slash path inside its root.
func clean(p string) bool {
	return p != "" && p != "." && !strings.HasPrefix(p, "/") && !strings.Contains(p, `\`) &&
		path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// sortedFiles is t's file paths in tree order: by folder, each folder's descriptor first.
func (t *Type) sortedFiles() []string {
	out := make([]string, 0, len(t.Files))
	for rel := range t.Files {
		out = append(out, rel)
	}
	slices.SortFunc(out, func(a, b string) int {
		da, db := path.Dir(a), path.Dir(b)
		if da != db {
			return strings.Compare(da, db)
		}
		if a == Descriptor(da) {
			return -1
		}
		if b == Descriptor(db) {
			return 1
		}
		return strings.Compare(a, b)
	})
	return out
}
