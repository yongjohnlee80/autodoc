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

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
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

// installPackage is :lua-install <git-url>, or a registry package's name: a candidate staged in the
// lua folder, its card asked (askPackage).
func (h *Host) installPackage(url string) error {
	url = strings.TrimSpace(url)
	if isRegistryName(url) && h.luaDir != "" { // a name: the registry's package of that name
		h.notify(h.trf("autodoc.notice.luapackages.looking_up_in_the_registry", url))
		resolveRegistry(core{h}, regindex.KindLua, url, h.registryLoaded, func(repo string, err error) {
			if err != nil {
				h.notify(h.trf("autodoc.notice.luapackages.lua_install_not_installed", err.Error()))
				return
			}
			if err := h.installPackage(repo); err != nil {
				h.notify(err.Error())
			}
		})
		return nil
	}
	switch {
	case url == "":
		return errors.New("lua-install <name or git-url>")
	case h.luaDir == "":
		return errors.New("lua-install: no Lua folder")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errors.New("lua-install needs git")
	}
	dir, own := h.luaDir, h.cands.allocs
	h.notify(h.trf("autodoc.notice.luapackages.cloning", url))
	do(h, func(ctx context.Context) answerOf[*candidate] { return answered(stagePackage(ctx, dir, url, own)) }, func(r answerOf[*candidate]) {
		if r.err == nil {
			r.err = h.cands.begin(r.v, h.askPackage, h.hidePackage)
		}
		if r.err != nil {
			h.notify(h.trf("autodoc.notice.luapackages.lua_install_not_installed", r.err.Error()))
			return
		}
		h.cands.show(r.v)
	})
	return nil
}

// stagePackage stages url as a Lua package candidate in dir, refused when that package is
// installed already.
func stagePackage(ctx context.Context, dir, url string, own *allocs) (*candidate, error) {
	stage, commit, err := cloneStage(ctx, dir, url, own)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*candidate, error) { own.remove(stage); return nil, err }
	m, err := readPackage(stage)
	if err != nil {
		return fail(err)
	}
	if _, err := os.Stat(filepath.Join(dir, m.Name)); err == nil {
		return fail(fmt.Errorf("%s is installed already: remove lua/%s first", m.Name, m.Name))
	}
	return &candidate{kind: regindex.KindLua, name: m.Name, repo: url, commit: commit, stage: stage,
		entry: cachedEntry(regindex.KindLua, url), pkg: m}, nil
}

// packageRisk is the sentence every package card carries, in English (the card's is in the UI's
// language).
const packageRisk = "A package's Lua runs in AutoDoc, held to what you grant it."

// askPackage is a Lua package candidate's card: its name, version, source and commit, what it
// requires, its audit above the risk, and what Audit… does. Install moves the pristine stage into
// the lua folder and reloads the scripts; Cancel, or another question in its place, ends it.
func (h *Host) askPackage(c *candidate) {
	m := c.pkg
	requires := h.tr("autodoc.question.luapackages.nothing_it_must_ask_for")
	if len(m.Requires) > 0 {
		requires = h.trf("autodoc.question.luapackages.asked_again_each_once_it_loads", strings.Join(slices.Sorted(slices.Values(m.Requires)), ", "))
	}
	audit := ""
	if line := auditLineIn(h.trf, c.entry, c.commit); line != "" {
		audit = line + "\n\n"
	}
	text := fmt.Sprintf("%s %s: %s\n%s\n%s\n\n%s%s\n\n%s",
		m.Name, m.Version, m.Description, h.trf("autodoc.question.luapackages.from_at", c.repo, regindex.Short(c.commit)),
		h.trf("autodoc.question.luapackages.requires", requires), audit,
		h.tr("autodoc.question.luapackages.a_package_s_lua_runs_in_autodoc_held_to_what_you"),
		h.tr("autodoc.text.candidate.audit_asks_your_agent_to_read_this_code_autodoc"))
	h.ask(Question{
		Title: h.trf("autodoc.question.luapackages.install_the_lua_package", m.Name),
		Text:  text,
		Yes:   h.tr("autodoc.question.luapackages.install"), No: h.tr("autodoc.question.luapackages.cancel"), Other: h.tr("autodoc.question.luapackages.audit"),
		OnYes: func() {
			if h.cands.claim(c) {
				h.installPackageCandidate(c)
			}
		},
		OnOther:  func() { h.cands.audit(c) },
		OnCancel: func() { h.cands.cancel(c, "") },
	})
}

// hidePackage takes c's card down when another candidate's replaces it: the question is closed, and
// its OnCancel finds c suspended and ends nothing.
func (h *Host) hidePackage(*candidate) { h.closeDialog("confirm") }

// installPackageCandidate moves c's pristine stage into the lua folder, then reloads the scripts.
func (h *Host) installPackageCandidate(c *candidate) {
	to := filepath.Join(h.luaDir, c.name)
	do(h, func(ctx context.Context) error {
		if err := pristine(ctx, c); err != nil {
			return err
		}
		return os.Rename(c.stage, to)
	}, func(err error) {
		if err != nil {
			msg := h.trf("autodoc.notice.luapackages.lua_install_not_installed", err.Error())
			h.notify(msg)
			h.cands.end(c, msg)
			return
		}
		msg := h.trf("autodoc.notice.luapackages.installed_the_lua_package", c.name)
		h.notify(msg)
		h.cands.end(c, msg)
		if err := h.reloadScripts(); err != nil {
			h.notify(err.Error())
		}
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
			h.notify(h.trf("autodoc.notice.luapackages.the_plugin_s_lua_package_is_replaced_by_yours_lu", m.Name, m.Name))
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
