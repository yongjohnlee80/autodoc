// Package usertheme is AutoDoc's config-adjacent theme catalog. Golib owns the theme vocabulary,
// parser and validator; this package owns user-file discovery, naming and export policy.
package usertheme

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
	"github.com/yongjohnlee80/golib/tui/style"
)

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Directory resolves beside the selected config, without loading it or contacting a daemon.
func Directory(configPath string) (string, error) {
	if configPath == "" {
		var err error
		configPath, err = config.DefaultPath()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(filepath.Join(filepath.Dir(configPath), "themes"))
}

// CheckName refuses path components that cannot be QML module segments and built-in shadowing.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("theme name %q must match [a-z][a-z0-9_]*", name)
	}
	if slices.Contains(themes.Names(), name) {
		return fmt.Errorf("theme %q is built in and cannot be shadowed", name)
	}
	return nil
}

// Check validates file with the same shared validator used when a theme is loaded.
func Check(file string) (map[string]string, error) {
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	v, ds := themes.Validate(file, src)
	var errs []error
	for _, d := range ds {
		errs = append(errs, d)
	}
	return v, errors.Join(errs...)
}

// Create copies one built-in as a complete starting point; it never overwrites an existing file.
func Create(dir, name, from string) (string, error) {
	if err := CheckName(name); err != nil {
		return "", err
	}
	if !slices.Contains(themes.Names(), from) {
		return "", fmt.Errorf("no built-in theme %q", from)
	}
	src, err := fs.ReadFile(themes.FS(), from+".qml")
	if err != nil {
		return "", err
	}
	if _, ds := themes.Validate(from+".qml", src); len(ds) != 0 {
		return "", fmt.Errorf("invalid built-in theme: %v", ds)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(dir, name+".qml")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", err
	}
	_, err = f.Write(src)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		_ = os.Remove(file)
		return "", err
	}
	return file, nil
}

// Catalog captures module names at startup; edits, deletion and repair of those files are live.
// Add a new name and restart to register its module. It has no mutable global state.
type Catalog struct {
	dir      string
	users    []string
	entries  []fs.DirEntry
	Problems []error
}

func Open(dir string) *Catalog {
	c := &Catalog{dir: dir}
	if dir == "" {
		return c
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return c
	}
	if err != nil {
		c.Problems = append(c.Problems, err)
		return c
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".qml" {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".qml")
		if err := CheckName(name); err != nil {
			c.Problems = append(c.Problems, fmt.Errorf("%s:1:1: name: %w", filepath.Join(dir, e.Name()), err))
			continue
		}
		c.users, c.entries = append(c.users, name), append(c.entries, e)
		if _, err := c.Values(name); err != nil {
			c.Problems = append(c.Problems, err)
		}
	}
	return c
}

func (c *Catalog) Values(name string) (map[string]string, error) {
	if slices.Contains(themes.Names(), name) {
		return themes.Values(name)
	}
	if !slices.Contains(c.users, name) {
		return nil, fmt.Errorf("no theme %q", name)
	}
	return Check(filepath.Join(c.dir, name+".qml"))
}

func (c *Catalog) Names() []string {
	return append(themes.Names(), c.users...)
}

func (c *Catalog) Labels() []string {
	names := c.Names()
	for i, n := range names {
		if slices.Contains(c.users, n) {
			names[i] = n + " (user)"
		}
	}
	return names
}

// Files is the same io/fs path used by module loading and HotReload. An invalid file is an error
// before QML evaluation; the UI owns fallback. Invalid startup names remain registered for repair.
func (c *Catalog) Files() fs.FS { return checkedFS{c} }

type checkedFS struct{ c *Catalog }

func (f checkedFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return &themeDir{entries: slices.Clone(f.c.entries)}, nil
	}
	n := strings.TrimSuffix(name, ".qml")
	if name != n+".qml" || !slices.Contains(f.c.users, n) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	// Validate exactly the bytes handed to the loader, so a save between check and open cannot
	// install unchecked content. fs.ReadFile uses ReadFile below directly.
	src, err := f.ReadFile(name)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(filepath.Join(f.c.dir, name))
	if err != nil {
		return nil, err
	}
	return &themeFile{Reader: strings.NewReader(string(src)), info: info}, nil
}
func (f checkedFS) ReadFile(name string) ([]byte, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrInvalid}
	}
	n := strings.TrimSuffix(name, ".qml")
	if name != n+".qml" || !slices.Contains(f.c.users, n) {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	file := filepath.Join(f.c.dir, name)
	src, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	_, ds := themes.Validate(file, src)
	var errs []error
	for _, d := range ds {
		errs = append(errs, d)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return src, nil
}
func (f checkedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	if name != "." {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	return slices.Clone(f.c.entries), nil
}

// ExportStyle maps document RGB foreground/background to the nearest built-in RGB palette.
// Terminal slots/defaults have no portable RGB values; those use mono. Ties keep built-in order.
func (c *Catalog) ExportStyle(name string) string {
	if slices.Contains(themes.Names(), name) {
		return name
	}
	v, err := c.Values(name)
	if err != nil {
		return "light"
	}
	base, bok := rgb(v["document.base"])
	text, tok := rgb(v["document.text"])
	if !bok || !tok {
		return "mono"
	}
	best, score := "light", int(^uint(0)>>1)
	for _, n := range themes.Names() {
		bv, _ := themes.Values(n)
		bb, ok := rgb(bv["document.base"])
		bt, ok2 := rgb(bv["document.text"])
		if !ok || !ok2 {
			continue
		}
		d := distance(base, bb) + distance(text, bt)
		if d < score {
			best, score = n, d
		}
	}
	return best
}
func rgb(s string) ([3]int, bool) {
	c, err := style.ParseColor(s)
	if err != nil {
		return [3]int{}, false
	}
	r, g, b, ok := c.RGBValues()
	return [3]int{int(r), int(g), int(b)}, ok
}
func distance(a, b [3]int) int {
	d := 0
	for i := range a {
		x := a[i] - b[i]
		d += x * x
	}
	return d
}
