package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"maps"
	"slices"

	"github.com/BurntSushi/toml"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// ADDING AND MANAGING PLUGINS — Plugins › Add from a git URL… clones a repository whose top holds a
// plugin.toml, and Plugins › Manage plugins… updates or removes what the folder holds. There is no
// hub: the URL is the source.
//
// Nothing a clone holds runs before PluginConfirm is answered: the question names the plugin, its
// source and commit, the build its manifest asks for ([install] build, run in its directory) and the
// command it starts, and says it is the user's risk. Only a Yes builds it and moves it into the
// folder; a No throws the clone away. An update asks the same question of the commit it would move
// to.

// pluginRisk is the warning every add and update carries.
const pluginRisk = "A plugin is a program that runs as you, with your files. AutoDoc does " +
	"not check what a plugin does: add only plugins you trust. Adding one is at your own risk."

// The deadlines of a clone, a fetch and a build.
var (
	pluginCloneTimeout = 2 * time.Minute
	pluginBuildTimeout = 10 * time.Minute
)

// stagePrefix marks a clone not yet added: discoverPlugins skips a directory starting with ".".
const stagePrefix = ".adding-"

// pluginChange is the add or update PluginConfirm asks about.
type pluginChange struct {
	update bool
	dir    string // add: the staged clone; update: the plugin's own directory
	m      manifest
	url    string
	commit string // the commit it would be at
}

// managedPlugin is the manager's row: an installed plugin, and where it came from.
type managedPlugin struct {
	e      pluginEntry
	source string // the clone's origin, "" for a plugin put in the folder by hand
	commit string
}

// startAddPlugin is Plugins › Add from a git URL….
func (h *Host) startAddPlugin() {
	if h.pluginOpt.Dir == "" {
		h.notify("no plugins folder: AutoDoc was started without one")
		return
	}
	h.setField("App.pluginUrl", "")
	h.open("pluginAdd")
}

// addPlugin clones url into a staged directory and asks PluginConfirm about what it holds.
func (h *Host) addPlugin(url string) {
	url = strings.TrimSpace(url)
	if url == "" {
		h.notify("add a plugin: no URL")
		h.open("pluginAdd")
		return
	}
	h.closeDialog("pluginAdd")
	h.notifyOngoing(toastPlugin, "cloning "+url+"…")
	dir := h.pluginOpt.Dir
	type cloned struct {
		c   pluginChange
		err error
	}
	do(h, func(ctx context.Context) cloned {
		c, err := stageClone(ctx, dir, url)
		return cloned{c, err}
	}, func(r cloned) {
		if r.err != nil {
			h.notifyDone(toastPlugin, "not added: "+r.err.Error())
			return
		}
		h.notifyDone(toastPlugin, "cloned "+r.c.m.Name+" at "+r.c.commit)
		h.askPlugin(r.c)
	})
}

// stageClone clones url into a staged directory of dir and reads its manifest; a clone that is not
// a runnable plugin, or names one already installed, is thrown away.
func stageClone(ctx context.Context, dir, url string) (pluginChange, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return pluginChange{}, errors.New("adding a plugin needs git")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return pluginChange{}, err
	}
	stage, err := os.MkdirTemp(dir, stagePrefix)
	if err != nil {
		return pluginChange{}, err
	}
	fail := func(err error) (pluginChange, error) {
		_ = os.RemoveAll(stage)
		return pluginChange{}, err
	}
	if _, err := git(ctx, pluginCloneTimeout, "", "clone", "--depth", "1", "--", url, stage); err != nil {
		return fail(err)
	}
	e := readPlugin(stage)
	switch {
	case e == nil:
		return fail(errors.New("the repository has no plugin.toml at its top"))
	case e.reason != "":
		return fail(errors.New(e.reason))
	}
	if _, err := os.Stat(filepath.Join(dir, e.m.Name)); err == nil {
		return fail(fmt.Errorf("%s is installed already: Manage plugins… updates it", e.m.Name))
	}
	commit, err := git(ctx, pluginCloneTimeout, stage, "rev-parse", "--short", "HEAD")
	if err != nil {
		return fail(err)
	}
	return pluginChange{dir: stage, m: e.m, url: url, commit: commit}, nil
}

// askPlugin puts c to PluginConfirm.
func (h *Host) askPlugin(c pluginChange) {
	h.pendingPlugin = &c
	verb := "add"
	if c.update {
		verb = "update"
	}
	title := c.m.Title
	if title == "" {
		title = c.m.Name
	}
	h.set("App.pluginConfirmTitle", verb+" the plugin "+title+"?")
	h.set("App.pluginQuestion", pluginQuestion(c, title))
	h.open("pluginConfirm")
}

// pluginQuestion is PluginConfirm's text: what the plugin is, where it came from, what its build
// runs and what it starts, the risk, and then what its manifest declares it does in AutoDoc.
func pluginQuestion(c pluginChange, title string) string {
	build := "nothing: it has no build step"
	if len(c.m.Install.Build) > 0 {
		build = strings.Join(c.m.Install.Build, " ")
	}
	q := fmt.Sprintf("%s (%s)\nfrom  %s\nat    %s\n\nIts build runs:  %s\nIt then starts:  %s\n\n%s",
		title, c.m.Name, c.url, c.commit, build, strings.Join(c.m.Command, " "), pluginRisk)
	if d := pluginDeclares(&c.m); d != "" {
		q += "\n\nDeclares:  " + d
	}
	return q
}

// pluginDeclares is what a manifest's protocol-2 keys say the plugin does, for the install
// confirmation (ADR 1791268009 §2.1): "" for a 0209 dialog.
func pluginDeclares(m *manifest) string {
	var parts []string
	switch {
	case m.service() && m.Start == "launch":
		parts = append(parts, "a service, with no surface · starts with AutoDoc")
	case m.service():
		parts = append(parts, "a service, with no surface · starts on first use")
	case m.card():
		parts = append(parts, "a card beside the page")
	}
	if m.Feed.Document {
		parts = append(parts, "reads the open note's text (feed)")
	}
	if len(m.Commands) > 0 {
		cmds := make([]string, len(m.Commands))
		for i, c := range m.Commands {
			cmds[i] = c.Title
			if c.Key != "" {
				cmds[i] += " (SPC p " + c.Key + ")"
			}
		}
		parts = append(parts, "commands: "+strings.Join(cmds, ", "))
	}
	return strings.Join(parts, " · ")
}

// pluginConfirmed is PluginConfirm's Yes: the build, then the plugin in the folder.
func (h *Host) pluginConfirmed() {
	c := h.pendingPlugin
	h.pendingPlugin = nil
	if c == nil {
		return
	}
	if c.update {
		if r := h.running[c.m.Name]; r != nil {
			r.close("") // it is replaced under itself otherwise
		}
	}
	h.notifyOngoing(toastPlugin, "building "+c.m.Name+"…")
	dir, logs := h.pluginOpt.Dir, h.pluginOpt.LogDir
	do(h, func(ctx context.Context) error { return installChange(ctx, dir, logs, *c) }, func(err error) {
		if err != nil {
			h.notifyDone(toastPlugin, c.m.Name+" not installed: "+err.Error())
			return
		}
		h.loadPlugins()
		h.loadManaged()
		verb := "added"
		if c.update {
			verb = "updated"
		}
		h.notifyDone(toastPlugin, fmt.Sprintf("%s %s at %s: Plugins › %s", verb, c.m.Name, c.commit, c.m.Title))
	})
}

// pluginDeclined is PluginConfirm's No: an add's clone is thrown away; an update is left undone.
func (h *Host) pluginDeclined() {
	c := h.pendingPlugin
	h.pendingPlugin = nil
	if c == nil {
		return
	}
	if !c.update {
		_ = os.RemoveAll(c.dir)
		h.notify(c.m.Name + " not added")
		return
	}
	h.notify(c.m.Name + " not updated")
}

// installChange builds c and puts it in the folder: an add's clone moves in, an update's directory
// is moved to its fetched commit first. A failed build leaves an add's clone gone and an update at
// the commit it built from.
func installChange(ctx context.Context, dir, logs string, c pluginChange) error {
	if c.update {
		if _, err := git(ctx, pluginCloneTimeout, c.dir, "reset", "--hard", "FETCH_HEAD"); err != nil {
			return err
		}
	}
	if err := buildPlugin(ctx, logs, c); err != nil {
		if !c.update {
			_ = os.RemoveAll(c.dir)
		}
		return err
	}
	if c.update {
		return nil
	}
	return os.Rename(c.dir, filepath.Join(dir, c.m.Name))
}

// buildPlugin runs c's [install] build in its directory, bounded, its output in the plugin's
// install log.
func buildPlugin(ctx context.Context, logs string, c pluginChange) error {
	if len(c.m.Install.Build) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, pluginBuildTimeout)
	defer cancel()
	log := openPluginLog(logs, c.m.Name+"-install")
	defer log.Close()
	cmd := exec.CommandContext(ctx, c.m.Install.Build[0], c.m.Install.Build[1:]...)
	cmd.Dir = c.dir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "AUTODOC_PLUGIN_DIR="+c.dir)
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("its build did not finish in %s", pluginBuildTimeout)
		}
		msg := "its build failed (" + err.Error() + ")"
		if last := log.last(); last != "" {
			msg += ": " + last
		}
		return errors.New(msg)
	}
	return nil
}

// git runs git, never asking for credentials or anything else, and says what it said on a failure.
func git(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", fmt.Errorf("git %s did not finish in %s", args[0], timeout)
		}
		msg := strings.TrimSpace(errOut.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", args[0], msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// ---------------------------------------------------------------- the manager

// managePlugins is Plugins › Manage plugins….
func (h *Host) managePlugins() {
	if h.pluginOpt.Dir == "" {
		h.notify("no plugins folder: AutoDoc was started without one")
		return
	}
	h.set("App.pluginsHelp", pluginsHelp)
	h.open("pluginManager")
	h.loadManaged()
}

const pluginsHelp = "Add… from a git URL · Update… or Remove… the one under the cursor · the folder: "

// loadManaged lists the installed plugins in the manager, each with its source, read off the loop.
func (h *Host) loadManaged() {
	h.set("App.pluginsHelp", pluginsHelp+h.pluginOpt.Dir)
	entries := discoverPlugins(h.pluginOpt.Dir)
	do(h, func(ctx context.Context) []managedPlugin {
		out := make([]managedPlugin, len(entries))
		for i, e := range entries {
			out[i] = managedPlugin{e: e}
			if _, err := os.Stat(filepath.Join(e.dir, ".git")); err != nil {
				continue
			}
			out[i].source, _ = git(ctx, pluginCloneTimeout, e.dir, "remote", "get-url", "origin")
			out[i].commit, _ = git(ctx, pluginCloneTimeout, e.dir, "rev-parse", "--short", "HEAD")
		}
		return out
	}, func(ms []managedPlugin) {
		h.managedList = ms
		rows := make([]tuidecl.Row, len(ms))
		for i, m := range ms {
			source := m.source
			if source == "" {
				source = "local: put in the folder by hand"
			}
			rows[i] = tuidecl.Row{"key": m.e.dir, "name": m.e.label(), "place": h.placement(m.e), "commit": m.commit, "source": source}
		}
		h.managedPlugins.Reset(rows)
	})
}

// managedRow is the manager's row i, as listed.
func (h *Host) managedRow(i int) (managedPlugin, bool) {
	if i < 0 || i >= len(h.managedList) {
		h.set("App.pluginsHelp", "no plugin under the cursor · Add… installs one")
		return managedPlugin{}, false
	}
	return h.managedList[i], true
}

// startUpdatePlugin is the manager's Update…: the origin fetched, and PluginConfirm asked about the
// commit it would move to. Up to date is the fetched commit against the clone's HEAD, read with the
// fetch: the manager's row is listed off the loop, so for a moment after an update it still shows
// the commit before it.
func (h *Host) startUpdatePlugin(i int) {
	m, ok := h.managedRow(i)
	if !ok {
		return
	}
	if m.source == "" {
		h.notify(m.e.label() + " was put in the folder by hand: update it there")
		return
	}
	h.notifyOngoing(toastPlugin, "fetching "+m.e.m.Name+"…")
	type fetched struct {
		c    pluginChange
		head string
		err  error
	}
	do(h, func(ctx context.Context) fetched {
		c, err := fetchUpdate(ctx, m)
		if err != nil {
			return fetched{err: err}
		}
		head, err := git(ctx, pluginCloneTimeout, m.e.dir, "rev-parse", "--short", "HEAD")
		return fetched{c, head, err}
	}, func(r fetched) {
		switch {
		case r.err != nil:
			h.notifyDone(toastPlugin, m.e.m.Name+" not updated: "+r.err.Error())
		case r.c.commit == r.head:
			h.notifyDone(toastPlugin, m.e.m.Name+" is up to date at "+r.head)
		default:
			h.notifyDone(toastPlugin, "fetched "+m.e.m.Name+" at "+r.c.commit)
			h.askPlugin(r.c)
		}
	})
}

// fetchUpdate fetches m's origin and reads the manifest of the commit fetched, which the question
// shows: the build that would run is the new commit's.
func fetchUpdate(ctx context.Context, m managedPlugin) (pluginChange, error) {
	if _, err := git(ctx, pluginCloneTimeout, m.e.dir, "fetch", "--depth", "1", "origin"); err != nil {
		return pluginChange{}, err
	}
	commit, err := git(ctx, pluginCloneTimeout, m.e.dir, "rev-parse", "--short", "FETCH_HEAD")
	if err != nil {
		return pluginChange{}, err
	}
	src, err := git(ctx, pluginCloneTimeout, m.e.dir, "show", "FETCH_HEAD:plugin.toml")
	if err != nil {
		return pluginChange{}, errors.New("the fetched commit has no plugin.toml")
	}
	var next manifest
	if _, err := toml.Decode(src, &next); err != nil {
		return pluginChange{}, errors.New("the fetched plugin.toml: " + firstLine(err.Error()))
	}
	if next.Name != m.e.m.Name {
		return pluginChange{}, fmt.Errorf("the fetched plugin is %q, not %q", next.Name, m.e.m.Name)
	}
	return pluginChange{update: true, dir: m.e.dir, m: next, url: m.source, commit: commit}, nil
}

// placePlugin is the manager's Place: the plugin under the cursor to the next placement its
// manifest offers, kept as a preference, and moved at once when it is open.
func (h *Host) placePlugin(i int) {
	m, ok := h.managedRow(i)
	if !ok {
		return
	}
	places := m.e.m.Dialog.Placements
	if len(places) < 2 {
		h.set("App.pluginsHelp", fmt.Sprintf("%s is designed for %s only", m.e.label(), strings.Join(places, ", ")))
		return
	}
	next := places[(slices.Index(places, h.placement(m.e))+1)%len(places)]
	name := m.e.m.Name
	h.setPref(prefPluginPrefix+name+prefPluginPlace, next, func(p *prefs) {
		place := maps.Clone(p.pluginPlace)
		if place == nil {
			place = map[string]string{}
		}
		place[name] = next
		p.pluginPlace = place
	})
	if r := h.running[name]; r != nil {
		r.float.SetAnchor(pluginAnchors[next])
	}
	h.set("App.pluginsHelp", fmt.Sprintf("%s: %s", m.e.label(), next))
	h.loadManaged()
}

// startRemovePlugin is the manager's Remove….
func (h *Host) startRemovePlugin(i int) {
	m, ok := h.managedRow(i)
	if !ok {
		return
	}
	h.removingPlugin = m.e.dir
	h.set("App.removePluginQuestion", fmt.Sprintf("Remove %s? Its directory goes, with what its build made:\n%s",
		m.e.label(), m.e.dir))
	h.open("pluginRemove")
}

// removePluginConfirmed removes the plugin asked about: closed first when it is open, and only a
// directory of the plugins folder itself.
func (h *Host) removePluginConfirmed() {
	dir := h.removingPlugin
	h.removingPlugin = ""
	if dir == "" || filepath.Dir(dir) != filepath.Clean(h.pluginOpt.Dir) {
		return
	}
	for key, r := range h.running {
		if r.e.dir == dir {
			r.close("")
			delete(h.running, key)
		}
	}
	name := filepath.Base(dir)
	do(h, func(context.Context) error { return os.RemoveAll(dir) }, func(err error) {
		if err != nil {
			h.notify("not removed: " + err.Error())
			return
		}
		h.notify("removed " + name)
		h.loadPlugins()
		h.loadManaged()
	})
}
