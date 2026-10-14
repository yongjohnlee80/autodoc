package tui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// LUA PACKAGES (M8): a package is a folder of the config's lua/, lua/<name>/, holding init.lua and
// a package.toml:
//
//	name = "wordcount"          # the folder's name
//	version = "1.0.0"
//	description = "word counts"
//	requires = ["workspace.read"]
//
// A package loads gated (ADR 1792004705 §4.2), under its name: its requires, and any requires{}
// its code says, are asked once (luagate.go); its require reaches its own folder only (package
// script). The user's own lua/*.lua are not packages and stay ungated. :lua-install <git-url>
// clones a package and asks before it goes into lua/, as Plugins › Add from a git URL… does.

// packageManifest is a package's package.toml.
type packageManifest struct {
	Name        string   `toml:"name"`
	Version     string   `toml:"version"`
	Description string   `toml:"description"`
	Requires    []string `toml:"requires"`
}

// readPackage reads dir's manifest: its name must be the folder's, and its init.lua there.
func readPackage(dir string) (packageManifest, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return packageManifest{}, fmt.Errorf("package.toml: %s", firstLine(err.Error()))
	}
	defer root.Close()
	return readPackageIn(root.FS(), dir)
}

// readPackageIn reads the manifest of the package whose folder is dir, through fsys, its root: a
// file there that links outside the folder is not the package's (os.Root refuses it).
func readPackageIn(fsys fs.FS, dir string) (packageManifest, error) {
	var m packageManifest
	src, err := fs.ReadFile(fsys, "package.toml")
	if err == nil {
		_, err = toml.Decode(string(src), &m)
	}
	if err != nil {
		return m, fmt.Errorf("package.toml: %s", firstLine(err.Error()))
	}
	switch {
	case !validPackageName(m.Name):
		return m, fmt.Errorf("package.toml: a name of letters, digits, - and _, not %q", m.Name)
	case m.Name != filepath.Base(dir) && !strings.HasPrefix(filepath.Base(dir), stagePrefix):
		return m, fmt.Errorf("package.toml names %s, but its folder is %s", m.Name, filepath.Base(dir))
	}
	if _, err := fs.Stat(fsys, "init.lua"); err != nil {
		return m, errors.New("no init.lua in the package's folder")
	}
	return m, nil
}

func validPackageName(n string) bool {
	return n != "" && !strings.ContainsFunc(n, func(r rune) bool {
		return !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
}

// loadPackages loads every package of the lua folder, in name order: a folder that is not one, or
// fails, is one error and the others still load. A folder starting with "." (an install staged) is
// skipped.
func (h *Host) loadPackages() []error {
	if h.luaDir == "" {
		return nil
	}
	entries, err := os.ReadDir(h.luaDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return []error{err}
	}
	var errs []error
	for _, en := range entries {
		if !en.IsDir() || strings.HasPrefix(en.Name(), ".") {
			continue
		}
		dir := filepath.Join(h.luaDir, en.Name())
		// the package reads its own folder only, links included: os.Root refuses a path, or a
		// link, that leads outside it. It stays open while the engine may require from it.
		root, err := os.OpenRoot(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("the Lua package %s: %w", en.Name(), err))
			continue
		}
		h.luaRoots = append(h.luaRoots, root)
		m, err := readPackageIn(root.FS(), dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("the Lua package %s: %w", en.Name(), err))
			continue
		}
		grants := luaGrants{h}
		if len(m.Requires) > 0 {
			grants.Request(m.Name, m.Requires)
		}
		if err := h.scripts.LoadPackage(m.Name, root.FS(), grants); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// installPackage is :lua-install <git-url>: a shallow clone, staged in the lua folder, asked about
// (its name, version, source, commit and what it requires); Install moves it into place and reloads
// the scripts, and anything else throws the clone away.
func (h *Host) installPackage(url string) error {
	url = strings.TrimSpace(url)
	switch {
	case url == "":
		return errors.New("lua-install <git-url>")
	case h.luaDir == "":
		return errors.New("lua-install: no Lua folder")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("lua-install needs git")
	}
	dir := h.luaDir
	h.notify("cloning " + url + "…")
	type staged struct {
		dir, commit string
		m           packageManifest
		err         error
	}
	do(h, func(ctx context.Context) staged {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return staged{err: err}
		}
		stage, err := os.MkdirTemp(dir, stagePrefix)
		if err != nil {
			return staged{err: err}
		}
		fail := func(err error) staged { _ = os.RemoveAll(stage); return staged{err: err} }
		if _, err := git(ctx, pluginCloneTimeout, "", "clone", "--depth", "1", "--", url, stage); err != nil {
			return fail(err)
		}
		m, err := readPackage(stage)
		if err != nil {
			return fail(err)
		}
		if _, err := os.Stat(filepath.Join(dir, m.Name)); err == nil {
			return fail(fmt.Errorf("%s is installed already: remove lua/%s first", m.Name, m.Name))
		}
		commit, err := git(ctx, pluginCloneTimeout, stage, "rev-parse", "--short", "HEAD")
		if err != nil {
			return fail(err)
		}
		return staged{dir: stage, commit: commit, m: m}
	}, func(s staged) {
		if s.err != nil {
			h.notify("lua-install: not installed: " + s.err.Error())
			return
		}
		h.askPackage(url, s.dir, s.commit, s.m)
	})
	return nil
}

// askPackage asks before a staged package goes into the lua folder.
func (h *Host) askPackage(url, stage, commit string, m packageManifest) {
	requires := "nothing it must ask for"
	if len(m.Requires) > 0 {
		requires = strings.Join(slices.Sorted(slices.Values(m.Requires)), ", ") + " (asked again, each, once it loads)"
	}
	text := fmt.Sprintf("%s %s: %s\nfrom %s at %s\nrequires %s\n\nA package's Lua runs in AutoDoc, held to what you grant it.",
		m.Name, m.Version, m.Description, url, commit, requires)
	h.ask(Question{
		Title: "install the Lua package " + m.Name + "?",
		Text:  text,
		Yes:   "&Install", No: "&Cancel",
		OnYes: func() {
			to := filepath.Join(h.luaDir, m.Name)
			if err := os.Rename(stage, to); err != nil {
				_ = os.RemoveAll(stage)
				h.notify("lua-install: not installed: " + err.Error())
				return
			}
			h.notify("installed the Lua package " + m.Name)
			if err := h.reloadScripts(); err != nil {
				h.notify(err.Error())
			}
		},
		OnCancel: func() { _ = os.RemoveAll(stage) },
	})
}
