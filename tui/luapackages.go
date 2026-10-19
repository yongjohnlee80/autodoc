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

	"github.com/yongjohnlee80/autodoc/core/manifests"
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
type packageManifest = manifests.Package

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
// file there that links outside the folder is not the package's (os.Root refuses it). A staged
// clone's folder is not yet named for its package.
func readPackageIn(fsys fs.FS, dir string) (packageManifest, error) {
	folder := filepath.Base(dir)
	if strings.HasPrefix(folder, stagePrefix) {
		folder = ""
	}
	return manifests.ReadPackage(fsys, folder)
}

// validPackageName is a Lua package's name: letters, digits, - and _.
var validPackageName = manifests.ValidPackageName

// loadPackages loads every package of the lua folder, in name order: a folder that is not one, or
// fails, is one error and the others still load. A folder starting with "." (an install staged) is
// skipped.
func (h *Host) loadPackages() []error {
	h.userPackages = map[string]bool{}
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
		// the name is the user's from here, whether the package loads or fails: a plugin's package of
		// the same name never runs (ADR 1792300990 §4.4)
		h.userPackages[en.Name()] = true
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

// loadPluginPackages loads each installed plugin's Lua package, <plugin folder>/lua/ (package.toml
// naming the plugin, init.lua), gated under "plugin:<name>" (ADR 1792300990 §4.4). A name the user's
// own package holds (loaded or not) is a notice, and nothing of the plugin's package runs: no grant
// is asked, no init. The package's identity is recorded before its init runs, so autodoc.plugin.run
// works from the init too, and dropped again if the load fails.
func (h *Host) loadPluginPackages() []error {
	h.pluginPackages = map[string]string{}
	if h.plugins == nil {
		return nil
	}
	var errs []error
	for _, e := range h.plugins.pluginList {
		if e.reason != "" {
			continue
		}
		dir := filepath.Join(e.dir, "lua")
		root, err := os.OpenRoot(dir)
		if err != nil {
			continue // no lua/: the plugin has no package
		}
		if _, err := fs.Stat(root.FS(), "package.toml"); err != nil {
			root.Close()
			continue
		}
		h.luaRoots = append(h.luaRoots, root)
		m, err := readPluginPackage(root.FS(), e.m.Name)
		if err != nil {
			errs = append(errs, fmt.Errorf("the plugin %s's Lua package: %w", e.m.Name, err))
			continue
		}
		if h.userPackages[m.Name] {
			h.notify("the plugin " + m.Name + "'s Lua package is replaced by yours, lua/" + m.Name)
			continue
		}
		script := pluginScript(m.Name)
		h.pluginPackages[script] = e.key()
		grants := luaGrants{h}
		if len(m.Requires) > 0 {
			grants.Request(script, m.Requires)
		}
		if err := h.scripts.LoadPackage(script, root.FS(), grants); err != nil {
			delete(h.pluginPackages, script)
			errs = append(errs, err)
		}
	}
	return errs
}

// pluginScript is a plugin's package's script name: its grants' key, and its caller identity.
func pluginScript(name string) string { return "plugin:" + name }

// readPluginPackage reads a plugin's lua/package.toml: its name must be the plugin's.
var readPluginPackage = manifests.ReadPluginPackage
