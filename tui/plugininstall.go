package tui

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"slices"

	"github.com/yongjohnlee80/autodoc/plugin"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
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

// managedPlugin is the manager's row: an installed plugin, and where it came from.
type managedPlugin struct {
	e      pluginEntry
	source string // the clone's origin, "" for a plugin put in the folder by hand
	commit string
}

// startAddPlugin is Plugins › Add from a git URL….
func (pl *plugins) startAddPlugin() {
	if pl.pluginOpt.Dir == "" {
		pl.c.Notify(pl.c.Tr("autodoc.notice.plugininstall.no_plugins_folder_autodoc_was_started_without_on"))
		return
	}
	setField(pl.c, "App.pluginUrl", "")
	pl.c.Open("pluginAdd")
}

// addPlugin stages url as a candidate and asks PluginConfirm about it. A name in place of a URL is
// the registry's plugin of that name (plugins.add <name>).
func (pl *plugins) addPlugin(url string) {
	url = strings.TrimSpace(url)
	if isRegistryName(url) {
		pl.c.Close("pluginAdd")
		pl.c.NotifyOngoing(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.looking_up_in_the_registry", url))
		resolveRegistry(pl.c, regindex.KindPlugin, url, nil, func(repo string, err error) {
			if err != nil {
				pl.c.NotifyDone(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.not_added", err.Error()))
				return
			}
			pl.addPlugin(repo)
		})
		return
	}
	if url == "" {
		pl.c.Notify(pl.c.Tr("autodoc.notice.plugininstall.add_a_plugin_no_url"))
		pl.c.Open("pluginAdd")
		return
	}
	pl.c.Close("pluginAdd")
	pl.c.NotifyOngoing(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.cloning", url))
	dir, own := pl.pluginOpt.Dir, pl.cands.allocs
	coreDo(pl.c, func(ctx context.Context) answerOf[*candidate] {
		return answered(stagePlugin(ctx, dir, url, nil, own))
	}, func(r answerOf[*candidate]) {
		if r.err == nil {
			r.err = pl.cands.begin(r.v, pl.askPlugin, pl.hidePlugin)
		}
		if r.err != nil {
			pl.c.NotifyDone(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.not_added", r.err.Error()))
			return
		}
		pl.c.NotifyDone(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.cloned_at", r.v.name, regindex.Short(r.v.commit)))
		pl.cands.show(r.v)
	})
}

// cloneStage clones url, shallow, into a new stage of parent, and reads the commit it is at, in
// full: a candidate's stage, a plugin's or a Lua package's alike. The stage is held in own from the
// moment it exists, so the session's end removes it even if this answer never reaches the loop. A
// failed clone leaves no stage.
func cloneStage(ctx context.Context, parent, url string, own *allocs) (stage, commit string, err error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", "", err
	}
	if stage, err = os.MkdirTemp(parent, stagePrefix); err != nil {
		return "", "", err
	}
	if !own.hold(stage) {
		own.remove(stage)
		return "", "", errSessionEnded
	}
	if _, err = git(ctx, pluginCloneTimeout, "", "clone", "--depth", "1", "--", url, stage); err == nil {
		commit, err = git(ctx, pluginCloneTimeout, stage, "rev-parse", "HEAD") // in full: an audit names a full commit
	}
	if err != nil {
		own.remove(stage)
		return "", "", err
	}
	if err := own.keep(stage); err != nil { // the session ended while the clone ran
		return "", "", err
	}
	return stage, commit, nil
}

// stagePlugin stages url as a plugin candidate: an add (installed nil), refused when that plugin is
// installed already, or an update of installed, refused when the staged plugin is another. A stage
// that is not a runnable plugin is thrown away.
func stagePlugin(ctx context.Context, dir, url string, installed *pluginEntry, own *allocs) (*candidate, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("adding a plugin needs git")
	}
	stage, commit, err := cloneStage(ctx, dir, url, own)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*candidate, error) { own.remove(stage); return nil, err }
	e := readPlugin(stage)
	switch {
	case e == nil:
		return fail(errors.New("the repository has no plugin.toml at its top"))
	case e.reason != "":
		return fail(errors.New(e.reason))
	case installed != nil && e.m.Name != installed.m.Name:
		return fail(fmt.Errorf("the fetched plugin is %q, not %q", e.m.Name, installed.m.Name))
	}
	if _, err := os.Stat(filepath.Join(dir, e.m.Name)); installed == nil && err == nil {
		return fail(fmt.Errorf("%s is installed already: Manage plugins… updates it", e.m.Name))
	}
	return &candidate{kind: regindex.KindPlugin, name: e.m.Name, repo: url, commit: commit, stage: stage,
		update: installed != nil, entry: cachedEntry(regindex.KindPlugin, url), m: e.m}, nil
}

// askPlugin is a plugin candidate's card: PluginConfirm, about c, which is the card's alone.
func (pl *plugins) askPlugin(c *candidate) {
	pl.pendingPlugin = c
	title := "autodoc.text.plugininstall.add_the_plugin" // a sentence each, as every language says it
	if c.update {
		title = "autodoc.text.plugininstall.update_the_plugin"
	}
	pl.c.Set("App.pluginConfirmTitle", pl.c.Trf(title, pluginTitle(c)))
	pl.c.Set("App.pluginQuestion", pluginQuestion(c, pluginTitle(c)))
	pl.c.Open("pluginConfirm")
}

// hidePlugin takes c's PluginConfirm down when another candidate's card replaces it: closed, it
// answers nothing (pluginDeclined does not run).
func (pl *plugins) hidePlugin(c *candidate) {
	if pl.pendingPlugin == c {
		pl.pendingPlugin = nil
		pl.c.Close("pluginConfirm")
	}
}

func pluginTitle(c *candidate) string { return cmp.Or(c.m.Title, c.m.Name) }

// pluginQuestion is PluginConfirm's text: what the plugin is, where it came from, what its build
// runs and what it starts, its audit and the risk, what its manifest declares it does in AutoDoc,
// and what Audit… does.
func pluginQuestion(c *candidate, title string) string {
	build := "nothing: it has no build step"
	if len(c.m.Install.Build) > 0 {
		build = strings.Join(c.m.Install.Build, " ")
	}
	audit := ""
	if line := auditLine(c.entry, c.commit); line != "" {
		audit = line + "\n\n" // above the risk, which stays word for word
	}
	q := fmt.Sprintf("%s (%s)\nfrom  %s\nat    %s\n\nIts build runs:  %s\nIt then starts:  %s\n\n%s%s",
		title, c.m.Name, c.repo, regindex.Short(c.commit), build, strings.Join(c.m.Command, " "), audit, pluginRisk)
	if d := pluginDeclares(&c.m); d != "" {
		q += "\n\nDeclares:  " + d
	}
	return q + "\n\n" + auditNote
}

// pluginDeclares is what a manifest's protocol-2 and -3 keys say the plugin does, for the install
// confirmation (ADR 1791268009 §2.1): "" for a 0209 dialog.
func pluginDeclares(m *manifest) string { return strings.Join(m.Declares(), " · ") }

// pluginConfirmed is PluginConfirm's Yes: the candidate on the card, installed once.
func (pl *plugins) pluginConfirmed() {
	c := pl.pendingPlugin
	pl.pendingPlugin = nil
	if !pl.cands.claim(c) {
		return
	}
	pl.installCandidate(c)
}

// pluginDeclined is PluginConfirm's No: the candidate ends, its stage with it; nothing installed is
// touched.
func (pl *plugins) pluginDeclined() {
	c := pl.pendingPlugin
	pl.pendingPlugin = nil
	if c == nil {
		return
	}
	outcome := c.name + " not added"
	if c.update {
		outcome = c.name + " not updated"
	}
	if pl.cands.cancel(c, outcome) {
		pl.c.Notify(outcome)
	}
}

// pluginAudit is PluginConfirm's Audit…: the card's candidate to an agent (candidate.go).
func (pl *plugins) pluginAudit() {
	c := pl.pendingPlugin
	pl.pendingPlugin = nil
	pl.cands.audit(c)
}

// installCandidate builds c in its stage, never in the installed tree, the stage pristine first:
// an add's build moves in; an update's running plugin is stopped only once the build succeeded,
// and the build swaps in for it (swapIn). A failed build leaves an update's plugin as it was,
// running.
func (pl *plugins) installCandidate(c *candidate) {
	pl.c.NotifyOngoing(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.building", c.name))
	dir, logs := pl.pluginOpt.Dir, pl.pluginOpt.LogDir
	coreDo(pl.c, func(ctx context.Context) error {
		if err := pristine(ctx, c); err != nil {
			return err
		}
		if err := buildPlugin(ctx, logs, &c.m, c.stage); err != nil {
			return err
		}
		if c.update {
			return nil
		}
		return pluginRename(c.stage, filepath.Join(dir, c.name))
	}, func(err error) {
		if err != nil || !c.update {
			pl.installed(c, err)
			return
		}
		if r := pl.running[c.name]; r != nil {
			r.stopFor(plugin.CloseUpdate, "") // it is replaced under itself otherwise; its slot stays
		}
		coreDo(pl.c, func(context.Context) error { return swapIn(dir, c.stage, c.name) }, func(err error) { pl.installed(c, err) })
	})
}

// installed says how c's install went and ends c. The folder is read again either way: a swap that
// failed may have left the plugin stopped, or in .previous.
func (pl *plugins) installed(c *candidate, err error) {
	if err != nil {
		msg := c.name + " not installed: " + err.Error()
		pl.c.NotifyDone(toastPlugin, msg)
		pl.cands.end(c, msg)
		if c.update {
			pl.loadPlugins()
			pl.loadManaged()
		}
		return
	}
	pl.loadPlugins()
	pl.loadManaged()
	if pl.scriptsChanged != nil {
		pl.scriptsChanged()
	}
	msg := fmt.Sprintf("added %s at %s: Plugins › %s", c.name, regindex.Short(c.commit), c.m.Title)
	if c.update {
		msg = fmt.Sprintf("updated %s at %s: Plugins › %s; its previous folder is kept in plugins/%s/%s until the next update",
			c.name, regindex.Short(c.commit), c.m.Title, previousDir, c.name)
	}
	pl.c.NotifyDone(toastPlugin, msg)
	pl.cands.end(c, msg)
}

// previousDir is where an update keeps the folder it replaced, in the plugins folder; discovery skips
// it, as it does every name starting with ".".
const previousDir = ".previous"

// pluginRename and pluginRemoveAll are swapIn's renames and removal; a test makes each fail in turn.
var (
	pluginRename    = os.Rename
	pluginRemoveAll = os.RemoveAll
)

// swapIn puts the built stage in place of the installed plugin name, in two renames within dir (one
// filesystem): the installed folder to .previous/<name>, then the stage to its name. Each failure
// leaves the plugin as it was, or, when the old folder can't be put back, says where it is.
func swapIn(dir, stage, name string) error {
	installed, prev := filepath.Join(dir, name), filepath.Join(dir, previousDir, name)
	if err := pluginRemoveAll(prev); err != nil {
		return fmt.Errorf("the plugin was left as it was: the older %s could not be removed: %w", prev, err)
	}
	if err := os.MkdirAll(filepath.Dir(prev), 0o755); err != nil {
		return fmt.Errorf("the plugin was left as it was: %w", err)
	}
	if err := pluginRename(installed, prev); err != nil {
		return fmt.Errorf("the plugin was left as it was: %w", err)
	}
	if err := pluginRename(stage, installed); err != nil {
		if rerr := pluginRename(prev, installed); rerr != nil {
			return fmt.Errorf("the new build could not replace it (%v), and the old folder could not be put back (%v): "+
				"the plugin is stopped, and its old folder is %s", err, rerr, prev)
		}
		return fmt.Errorf("the plugin was left as it was: %w", err)
	}
	return nil
}

// buildPlugin runs m's [install] build in dir, bounded, its output in the plugin's install log.
func buildPlugin(ctx context.Context, logs string, m *manifest, dir string) error {
	if len(m.Install.Build) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, pluginBuildTimeout)
	defer cancel()
	log := openPluginLog(logs, m.Name+"-install")
	defer log.Close()
	cmd := exec.CommandContext(ctx, m.Install.Build[0], m.Install.Build[1:]...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "AUTODOC_PLUGIN_DIR="+dir)
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
func (pl *plugins) managePlugins() {
	if pl.pluginOpt.Dir == "" {
		pl.c.Notify(pl.c.Tr("autodoc.notice.plugininstall.no_plugins_folder_autodoc_was_started_without_on"))
		return
	}
	pl.c.Set("App.pluginsHelp", pluginsHelp)
	pl.c.Open("pluginManager")
	pl.loadManaged()
}

const pluginsHelp = "Add… from a git URL · Update… or Remove… the one under the cursor · the folder: "

// loadManaged lists the installed plugins in the manager, each with its source, read off the loop.
func (pl *plugins) loadManaged() {
	pl.c.Set("App.pluginsHelp", pluginsHelp+pl.pluginOpt.Dir)
	entries := discoverPlugins(pl.pluginOpt.Dir)
	coreDo(pl.c, func(ctx context.Context) answerOf[[]managedPlugin] {
		out := make([]managedPlugin, len(entries))
		for i, e := range entries {
			out[i] = managedPlugin{e: e}
			if _, err := os.Stat(filepath.Join(e.dir, ".git")); err != nil {
				continue
			}
			out[i].source, _ = git(ctx, pluginCloneTimeout, e.dir, "remote", "get-url", "origin")
			out[i].commit, _ = git(ctx, pluginCloneTimeout, e.dir, "rev-parse", "--short", "HEAD")
		}
		return answered(out, nil)
	}, func(a answerOf[[]managedPlugin]) {
		if a.err != nil {
			pl.c.Failed("the installed plugins", a.err) // it panicked: an empty list, said
		}
		pl.managedPlugins.Reset(a.v)
	})
}

// managedPluginRowOf is an installed plugin as the manager shows it.
func (pl *plugins) managedPluginRowOf(_ int, m managedPlugin) rowOf {
	source := m.source
	if source == "" {
		source = "local: put in the folder by hand"
	}
	return rowOf{"key": m.e.dir, "name": m.e.label(), "place": pl.placement(m.e), "commit": m.commit, "source": source}
}

// managedRow is the manager's row i, as listed.
func (pl *plugins) managedRow(i int) (managedPlugin, bool) {
	m, ok := pl.managedPlugins.At(i)
	if !ok {
		pl.c.Set("App.pluginsHelp", pl.c.Tr("autodoc.text.plugininstall.no_plugin_under_the_cursor_add_installs_one"))
	}
	return m, ok
}

// startUpdatePlugin is the manager's Update…: the origin fetched, and PluginConfirm asked about the
// commit it would move to. Up to date is the fetched commit against the clone's HEAD, read with the
// fetch: the manager's row is listed off the loop, so for a moment after an update it still shows
// the commit before it.
func (pl *plugins) startUpdatePlugin(i int) {
	m, ok := pl.managedRow(i)
	if !ok {
		return
	}
	if m.source == "" {
		pl.c.Notify(pl.c.Trf("autodoc.notice.plugininstall.was_put_in_the_folder_by_hand_update_it_there", m.e.label()))
		return
	}
	pl.fetchAndAsk(m)
}

// updatePlugin is plugins.update: the installed plugin named, as the manager's Update… updates it.
// Its source is read with the fetch, since the manager's listing may not have run.
func (pl *plugins) updatePlugin(name string) error {
	i := slices.IndexFunc(pl.pluginList, func(e pluginEntry) bool { return e.m.Name == name })
	if i < 0 {
		return fmt.Errorf("plugins.update: no plugin %s", name)
	}
	pl.fetchAndAsk(managedPlugin{e: pl.pluginList[i]})
	return nil
}

// fetchAndAsk stages m's source as an update candidate and asks about it, unless the staged commit
// is the one installed. m's source, when not known yet, is its clone's origin. The installed
// folder is only read: nothing is fetched into it.
func (pl *plugins) fetchAndAsk(m managedPlugin) {
	pl.c.NotifyOngoing(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.fetching", m.e.m.Name))
	dir, own := pl.pluginOpt.Dir, pl.cands.allocs
	type staged struct {
		c    *candidate
		head string
		err  error
	}
	coreDo(pl.c, func(ctx context.Context) staged {
		if m.source == "" {
			if _, err := os.Stat(filepath.Join(m.e.dir, ".git")); err != nil {
				return staged{err: errors.New("it was put in the folder by hand: update it there")}
			}
			src, err := git(ctx, pluginCloneTimeout, m.e.dir, "remote", "get-url", "origin")
			if err != nil {
				return staged{err: err}
			}
			m.source = src
		}
		head, err := git(ctx, pluginCloneTimeout, m.e.dir, "rev-parse", "HEAD") // in full, as the staged commit is
		if err != nil {
			return staged{err: err}
		}
		c, err := stagePlugin(ctx, dir, m.source, &m.e, own)
		if err == nil && c.commit == head {
			own.remove(c.stage)
		}
		return staged{c, head, err}
	}, func(r staged) {
		switch {
		case r.err != nil:
			pl.c.NotifyDone(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.not_updated", m.e.m.Name, r.err.Error()))
		case r.c.commit == r.head:
			pl.c.NotifyDone(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.is_up_to_date_at", m.e.m.Name, regindex.Short(r.head)))
		default:
			if err := pl.cands.begin(r.c, pl.askPlugin, pl.hidePlugin); err != nil {
				pl.c.NotifyDone(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.not_updated", m.e.m.Name, err.Error()))
				return
			}
			pl.c.NotifyDone(toastPlugin, pl.c.Trf("autodoc.notice.plugininstall.fetched_at", m.e.m.Name, regindex.Short(r.c.commit)))
			pl.cands.show(r.c)
		}
	})
}

// placePlugin is the manager's Place: the plugin under the cursor to the next placement its
// manifest offers, kept as a preference, and moved at once when it is open.
func (pl *plugins) placePlugin(i int) {
	m, ok := pl.managedRow(i)
	if !ok {
		return
	}
	places := m.e.m.Dialog.Placements
	if len(places) < 2 {
		pl.c.Set("App.pluginsHelp", pl.c.Trf("autodoc.text.plugininstall.is_designed_for_only", m.e.label(), strings.Join(places, ", ")))
		return
	}
	next := places[(slices.Index(places, pl.placement(m.e))+1)%len(places)]
	name := m.e.m.Name
	pl.c.Keep(pl.c.SetPluginPref(prefPluginPrefix+name+prefPluginPlace, next))
	if m.e.m.Card() { // its panel goes to the edge, docked: the preference's change sets the edge
		pl.c.DockPanel(cardPanelID(name))
	} else if r := pl.running[name]; r != nil && r.float != nil {
		r.float.SetAnchor(pluginAnchors[next])
	}
	pl.c.Set("App.pluginsHelp", pl.c.Trf("autodoc.text.plugininstall.text", m.e.label(), next))
	pl.loadManaged()
}

// startRemovePlugin is the manager's Remove….
func (pl *plugins) startRemovePlugin(i int) {
	m, ok := pl.managedRow(i)
	if !ok {
		return
	}
	dir := m.e.dir
	pl.c.Ask(Question{Title: pl.c.Tr("autodoc.question.plugininstall.remove_the_plugin"), Text: pl.c.Trf("autodoc.question.plugininstall.remove_its_directory_goes_with_what_its_build_ma", m.e.label(), dir), No: pl.c.Tr("autodoc.question.plugininstall.no_keep_it"), Yes: pl.c.Tr("autodoc.question.plugininstall.yes_remove_it"), OnYes: func() { pl.removePluginConfirmed(dir) }})
}

// removePluginConfirmed removes the plugin asked about: closed first when it is open, and only a
// directory of the plugins folder itself.
func (pl *plugins) removePluginConfirmed(dir string) {
	if dir == "" || filepath.Dir(dir) != filepath.Clean(pl.pluginOpt.Dir) {
		return
	}
	for key, r := range pl.running {
		if r.e.dir == dir {
			r.stopFor(plugin.CloseRemove, "")
			delete(pl.running, key)
		}
	}
	// its slot goes, whether it ran or not; after its run's stop began, so a write that run sends
	// now is past its fence and nothing brings the slot back
	for _, e := range pl.pluginList {
		if e.dir == dir {
			delete(pl.sessions, e.m.Name)
		}
	}
	name := filepath.Base(dir)
	prev := filepath.Join(filepath.Dir(dir), previousDir, name)
	coreDo(pl.c, func(context.Context) error {
		if err := os.RemoveAll(dir); err != nil {
			return err
		}
		if err := os.RemoveAll(prev); err != nil { // the folder its last update replaced
			return err
		}
		_ = os.Remove(filepath.Dir(prev)) // .previous itself, once nothing else is kept there
		return nil
	}, func(err error) {
		if err != nil {
			pl.c.Failed("not removed", err)
			return
		}
		pl.c.Notify(pl.c.Trf("autodoc.notice.plugininstall.removed", name))
		pl.loadPlugins()
		pl.loadManaged()
		if pl.scriptsChanged != nil {
			pl.scriptsChanged()
		}
	})
}
