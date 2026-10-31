package tui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// clickText clicks the screen where text begins.
func (r *running) clickText(t *testing.T, text string) {
	t.Helper()
	for y, row := range strings.Split(r.s.String(), "\n") {
		if j := strings.Index(row, text); j >= 0 {
			x := len([]rune(row[:j]))
			r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
				tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
			return
		}
	}
	t.Fatalf("%q is not on the screen:\n%s", text, r.s)
}

// namedPluginRepo is a repository of the test plugin named name, whose build runs build.
func namedPluginRepo(t *testing.T, name, build string) string {
	t.Helper()
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	commitPluginNamed(t, repo, name, strings.ToUpper(name[:1])+name[1:], build)
	return repo
}

// card is the candidate on PluginConfirm.
func (r *running) card() *candidate {
	return onLoop(r, func() *candidate { return r.h.plugins.pendingPlugin })
}

func gone(path string) bool { _, err := os.Stat(path); return errors.Is(err, os.ErrNotExist) }

// Audit… hands the chosen agent a task file beside a fresh copy of the candidate at its commit, and
// leaves a toast whose click brings the card back. A second Audit, after the first agent changed its
// copy, gets a fresh, pristine copy, and the first is still there. Install installs the candidate
// and ends it once: its copies and task files go, and a late click on its toast does nothing.
func TestAuditHandsAFreshCopyAndItsToastReopensTheCard(t *testing.T) {
	heardDir := t.TempDir()
	agent := hearingAgent(t, heardDir)
	dir := t.TempDir()
	_, r, _ := runAgentTUIWith(t, t.TempDir(), []agentProfile{{Name: "Scribe", Codename: "scribe", Command: shellQuote(agent) + " scribe"}},
		"", nil, Options{Plugins: Plugins{Dir: dir, LogDir: t.TempDir()}})
	repo := pluginRepo(t, "Echo", "true")
	full := runGit(t, repo, "rev-parse", "HEAD")

	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	if q := r.question(t); !strings.HasSuffix(q, auditNote) {
		t.Fatalf("the card does not end with what Audit does:\n%s", q)
	}
	c := r.card()
	task := func(n int) (root, copy string) {
		t.Helper()
		r.s.WaitFor(t, "the audit handed over", func(string) bool { return len(heard(heardDir, "scribe")) >= 4*n })
		got := heard(heardDir, "scribe")[4*(n-1):]
		if got[0] != "Task: Audit echo at "+full[:7] || !strings.HasPrefix(got[3], "Instruction: Audit the repository") {
			t.Fatalf("Scribe heard %q", got)
		}
		file := strings.TrimPrefix(got[1], "File: ")
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		copy = filepath.Join(filepath.Dir(file), "echo")
		for _, want := range []string{"# Auditing an AutoDoc plugin", "- The copy: " + copy, "- The commit: " + full, "- From: file://" + repo, auditNote} {
			if !strings.Contains(string(b), want) {
				t.Fatalf("the task file has no %q:\n%s", want, b)
			}
		}
		if head := runGit(t, copy, "rev-parse", "HEAD"); head != full {
			t.Fatalf("the copy is at %s, want %s", head, full)
		}
		return filepath.Dir(file), copy
	}

	r.keys(t, key('a'))
	r.s.WaitForText(t, "audit with an agent")
	r.s.WaitForText(t, "Scribe  (scribe)")
	r.keys(t, enter())
	r.s.WaitForText(t, "start the agent?")
	r.keys(t, key('y'))
	root1, copy1 := task(1)
	r.s.WaitForText(t, "audit of echo sent to Scribe")
	if strings.Contains(r.s.String(), "Yes, at my own risk") || r.card() != nil {
		t.Fatal("the card stayed up while the audit was handed over")
	}
	if err := os.WriteFile(filepath.Join(copy1, "agent-was-here"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.clickText(t, "audit of echo sent")
	r.question(t)
	if r.card() != c {
		t.Fatal("the toast reopened another candidate")
	}
	r.keys(t, key('a'))
	r.s.WaitForText(t, "audit with an agent")
	r.s.WaitForText(t, "running")
	r.keys(t, enter())
	root2, copy2 := task(2)
	if root2 == root1 || !gone(filepath.Join(copy2, "agent-was-here")) || gone(filepath.Join(copy1, "agent-was-here")) {
		t.Fatalf("the second audit's copy %s is not fresh beside the first's %s", copy2, copy1)
	}

	r.clickText(t, "audit of echo sent")
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at "+full[:7])
	if head := runGit(t, filepath.Join(dir, "echo"), "rev-parse", "HEAD"); head != full {
		t.Fatalf("installed at %s, want %s", head, full)
	}
	r.s.WaitFor(t, "the copies and task files removed", func(string) bool { return gone(root1) && gone(root2) && gone(c.stage) })

	r.clearNotices()
	onLoop(r, func() bool { r.h.cands.reopen(c); return true })
	r.waitNotice(t, "that install has ended")
	if r.card() != nil {
		t.Fatal("a late click reopened an ended install")
	}
}

// Each candidate owns its card: A's card replaced by B's waits on its toast, and that toast opens
// A's card, not B's. A second add of a target already live is refused; Install twice on one card
// builds once; the session's end removes every live candidate's stage.
func TestEachCandidateOwnsItsCard(t *testing.T) {
	dir := t.TempDir()
	r, _ := pluginTUI(t, dir)
	counter := filepath.Join(t.TempDir(), "builds")
	alpha := namedPluginRepo(t, "alpha", "echo built >> "+shellQuote(counter))
	beta := namedPluginRepo(t, "beta", "true")
	add := func(repo string) { r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) }) }

	add(alpha)
	if q := r.question(t); !strings.Contains(q, "(alpha)") {
		t.Fatalf("the card:\n%s", q)
	}
	a := r.card()
	add(alpha)
	r.waitNotice(t, "not added: alpha is already being installed or audited")
	if got := entries(t, dir); len(got) != 1 {
		t.Fatalf("a refused add left a stage: %v", got)
	}

	add(beta)
	r.s.WaitFor(t, "B's card", func(string) bool { c := r.card(); return c != nil && c.name == "beta" })
	r.s.WaitForText(t, "the plugin alpha waits")
	r.keys(t, key('y'))
	r.waitNotice(t, "added beta at")

	r.clickText(t, "the plugin alpha waits")
	if q := r.question(t); !strings.Contains(q, "(alpha)") || r.card() != a {
		t.Fatalf("A's toast opened another card:\n%s", q)
	}
	onLoop(r, func() bool { // the card's Install, twice: a stale second click
		r.h.plugins.pluginConfirmed()
		r.h.plugins.pendingPlugin = a
		r.h.plugins.pluginConfirmed()
		return true
	})
	r.waitNotice(t, "added alpha at")
	if b, _ := os.ReadFile(counter); string(b) != "built\n" {
		t.Fatalf("Install twice built %q", b)
	}

	gamma := namedPluginRepo(t, "gamma", "true")
	add(gamma)
	r.question(t)
	g := r.card()
	onLoop(r, func() bool { r.h.cands.Stopped(); return true })
	if !gone(g.stage) {
		t.Fatal("the session's end left a candidate's stage")
	}
}

// An update stages its own candidate from the plugin's source and installs it, never the installed
// tree: a file the installed folder holds, untracked or ignored, is no input to the new build, and
// stays in .previous. A newer commit fetched while a card is up is refused, so the card confirmed
// late installs its own commit. Cancel leaves the installed plugin as it was, and a stage changed
// after staging is refused.
func TestAnUpdateInstallsItsCandidateNeverTheInstalledTree(t *testing.T) {
	const build = "ls -A > built.txt"
	repo := pluginRepo(t, "Echo", build)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("gen.out\nbuilt.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", ".gitignore")
	runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "ignore")
	dir := t.TempDir()
	installed := filepath.Join(dir, "echo")
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")
	for _, f := range []string{"notes.txt", "gen.out"} { // untracked, and ignored: a build would pick both up
		if err := os.WriteFile(filepath.Join(installed, f), []byte("old tree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	update := func() { r.h.p.Post(func() { _ = r.h.plugins.updatePlugin("echo") }) }
	head := func() string { return runGit(t, installed, "rev-parse", "HEAD") }

	commitPlugin(t, repo, "Echo two", build)
	a := runGit(t, repo, "rev-parse", "HEAD")
	update()
	if q := r.question(t); !strings.Contains(q, "at    "+a[:7]) {
		t.Fatalf("the update's card:\n%s", q)
	}
	commitPlugin(t, repo, "Echo three", build)
	b := runGit(t, repo, "rev-parse", "HEAD")
	update()
	r.waitNotice(t, "echo not updated: echo is already being installed or audited")
	r.keys(t, key('y'))
	r.waitNotice(t, "updated echo at "+a[:7]+": Plugins › Echo two; its previous folder is kept in plugins/.previous/echo")
	if got := head(); got != a {
		t.Fatalf("the card for %s installed %s", a[:7], got)
	}
	built, err := os.ReadFile(filepath.Join(installed, "built.txt"))
	if err != nil || !strings.Contains(string(built), "plugin.toml") {
		t.Fatalf("the updated folder is not the candidate's build: built.txt %q, %v", built, err)
	}
	for _, f := range []string{"notes.txt", "gen.out"} {
		if strings.Contains(string(built), f) || !gone(filepath.Join(installed, f)) {
			t.Errorf("the old tree's %s reached the update (the build saw %q)", f, built)
		}
		if gone(filepath.Join(dir, previousDir, "echo", f)) {
			t.Errorf("the old tree's %s is not kept in .previous", f)
		}
	}

	update()
	r.question(t)
	r.keys(t, key('n'))
	r.waitNotice(t, "echo not updated")
	if got := head(); got != a {
		t.Fatalf("a cancelled update moved the plugin to %s", got)
	}

	update()
	if q := r.question(t); !strings.Contains(q, "at    "+b[:7]) {
		t.Fatalf("the update's card:\n%s", q)
	}
	if err := os.WriteFile(filepath.Join(r.card().stage, "extra.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.keys(t, key('y'))
	r.waitNotice(t, "echo not installed: the staged copy of echo changed; stage it again")
	if got := head(); got != a {
		t.Fatalf("a changed stage was installed: %s", got)
	}
}

// A failed update build leaves the installed plugin as it was, running.
func TestAFailedUpdateBuildLeavesThePluginRunning(t *testing.T) {
	repo := pluginRepo(t, "Echo", "true")
	dir := t.TempDir()
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added echo at")
	run := r.openPlugin("echo")
	r.waitShown(t, "open 30x6")

	commitPlugin(t, repo, "Echo two", "false")
	r.h.p.Post(func() { _ = r.h.plugins.updatePlugin("echo") })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "echo not installed: its build failed")
	if onLoop(r, func() bool { return r.h.plugins.running["echo"] != run || run.closing }) {
		t.Fatal("a failed update build stopped the plugin")
	}
}

// swapIn leaves the installed plugin as it was when any step fails, or, when the old folder can't
// be put back, says where it is.
func TestTheSwapRecoversFromEachFailedStep(t *testing.T) {
	setup := func(t *testing.T) (dir, stage string) {
		dir = t.TempDir()
		writeFiles(t, dir, map[string]string{"p/old.txt": "old", ".adding-1/new.txt": "new", previousDir + "/p/older.txt": "older"})
		return dir, filepath.Join(dir, ".adding-1")
	}
	has := func(t *testing.T, path string) {
		t.Helper()
		if gone(path) {
			t.Errorf("%s is gone", path)
		}
	}
	defer func(r func(string, string) error, d func(string) error) { pluginRename, pluginRemoveAll = r, d }(pluginRename, pluginRemoveAll)
	failing := func(n int) func(string, string) error { // the nth rename fails
		i := 0
		return func(a, b string) error {
			if i++; i == n {
				return errors.New("rename refused")
			}
			return os.Rename(a, b)
		}
	}

	t.Run("the older copy can't be removed", func(t *testing.T) {
		dir, stage := setup(t)
		pluginRename, pluginRemoveAll = os.Rename, func(string) error { return errors.New("busy") }
		if err := swapIn(dir, stage, "p"); err == nil || !strings.Contains(err.Error(), "the plugin was left as it was") {
			t.Fatalf("%v", err)
		}
		has(t, filepath.Join(dir, "p", "old.txt"))
	})
	t.Run("the first rename fails", func(t *testing.T) {
		dir, stage := setup(t)
		pluginRename, pluginRemoveAll = failing(1), os.RemoveAll
		if err := swapIn(dir, stage, "p"); err == nil || !strings.Contains(err.Error(), "the plugin was left as it was") {
			t.Fatalf("%v", err)
		}
		has(t, filepath.Join(dir, "p", "old.txt"))
	})
	t.Run("the second rename fails, and the old folder goes back", func(t *testing.T) {
		dir, stage := setup(t)
		pluginRename, pluginRemoveAll = failing(2), os.RemoveAll
		if err := swapIn(dir, stage, "p"); err == nil || !strings.Contains(err.Error(), "the plugin was left as it was") {
			t.Fatalf("%v", err)
		}
		has(t, filepath.Join(dir, "p", "old.txt"))
	})
	t.Run("the second rename fails, and so does the restore", func(t *testing.T) {
		dir, stage := setup(t)
		calls := 0
		pluginRename, pluginRemoveAll = func(a, b string) error {
			if calls++; calls >= 2 {
				return errors.New("rename refused")
			}
			return os.Rename(a, b)
		}, os.RemoveAll
		prev := filepath.Join(dir, previousDir, "p")
		if err := swapIn(dir, stage, "p"); err == nil || !strings.Contains(err.Error(), "its old folder is "+prev) {
			t.Fatalf("%v", err)
		}
		has(t, filepath.Join(prev, "old.txt"))
	})
	t.Run("both renames succeed", func(t *testing.T) {
		dir, stage := setup(t)
		pluginRename, pluginRemoveAll = os.Rename, os.RemoveAll
		if err := swapIn(dir, stage, "p"); err != nil {
			t.Fatal(err)
		}
		has(t, filepath.Join(dir, "p", "new.txt"))
		has(t, filepath.Join(dir, previousDir, "p", "old.txt"))
		if !gone(filepath.Join(dir, previousDir, "p", "older.txt")) || !gone(stage) {
			t.Error("the older copy, or the stage, was kept")
		}
	})
}

// The audit picker lists the default agent first.
func TestDefaultFirst(t *testing.T) {
	es := []AgentEntry{{Ref: AgentRef{Codename: "a"}}, {Ref: AgentRef{Codename: "b"}}, {Ref: AgentRef{Codename: "c"}}}
	code := func(es []AgentEntry) (out []string) {
		for _, e := range es {
			out = append(out, e.Ref.Codename)
		}
		return out
	}
	if got := code(defaultFirst(es, "c")); !slices.Equal(got, []string{"c", "a", "b"}) || !slices.Equal(code(es), []string{"a", "b", "c"}) {
		t.Errorf("default first: %v (the list given: %v)", got, code(es))
	}
	if got := code(defaultFirst(es, "x")); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("no default: %v", got)
	}
}

// luaPackageRepo is a repository of a Lua package named name.
func luaPackageRepo(t *testing.T, name string) string {
	t.Helper()
	repo := t.TempDir()
	writeFiles(t, repo, map[string]string{
		"package.toml": "name = \"" + name + "\"\nversion = \"0.1.0\"\ndescription = \"says hello\"\n",
		"init.lua":     `autodoc.command{ name = "` + name + `-pkg", run = function(ed) ed:insert("hello") end }`,
	})
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", name)
	return repo
}

// A Lua package's card replaced by another candidate's waits on its toast, whichever kind replaces
// it: another package's question takes the card over without cancelling the first, and a plugin's
// card takes the Lua card down, leaving no second dialog up. Each toast reopens its own card.
func TestALuaCardReplacedWaitsOnItsToast(t *testing.T) {
	plugins, lua := t.TempDir(), t.TempDir()
	r, _ := pluginLuaTUI(t, plugins, nil, lua)
	r.waitNamespaces(t)
	alpha, beta := luaPackageRepo(t, "alpha"), luaPackageRepo(t, "beta")
	type seen struct {
		live  bool
		state opState
		stage string
	}
	look := func(key string) seen { // read on the loop, which owns the candidates
		return onLoop(r, func() seen {
			if c := r.h.cands.ops[key]; c != nil {
				return seen{true, c.state, c.stage}
			}
			return seen{}
		})
	}

	r.mustRun(t, "lua-install "+alpha)
	r.s.WaitForText(t, "install the Lua package alpha?")
	r.mustRun(t, "lua-install "+beta)
	r.s.WaitForText(t, "install the Lua package beta?")
	r.s.WaitForText(t, "the Lua package alpha waits")
	if a := look("lua/alpha"); !a.live || a.state != opSuspended || gone(a.stage) {
		t.Fatalf("Lua alpha, replaced by Lua beta: %+v", a)
	}

	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + namedPluginRepo(t, "gamma", "true")) })
	r.question(t)
	// taken down, not covered: the generic card's question was closed (a larger card would hide its text)
	r.s.WaitFor(t, "the Lua card down", func(string) bool { return !onLoop(r, func() bool { return r.h.asker.pending }) })
	if b := look("lua/beta"); !b.live || b.state != opSuspended || gone(b.stage) {
		t.Fatalf("Lua beta, replaced by a plugin's card: %+v", b)
	}

	// beta's toast while gamma's card is up: the pointer can't reach a toast under that modal, so
	// its callback runs as a Lua clone finishing would show beta's card (plugin to Lua)
	r.s.WaitForText(t, "the Lua package beta waits")
	onLoop(r, func() bool { r.h.cands.reopen(r.h.cands.ops["lua/beta"]); return true })
	r.s.WaitForText(t, "install the Lua package beta?")
	if onLoop(r, func() bool { return r.h.plugins.pendingPlugin != nil }) {
		t.Fatal("gamma's card stayed up beside beta's")
	}
	if b, g := look("lua/beta"), look("plugin/gamma"); b.state != opConfirming || !g.live || g.state != opSuspended {
		t.Fatalf("beta's toast: beta %+v, gamma %+v", b, g)
	}
	stage := look("lua/beta").stage
	r.keys(t, esc()) // beta's card answered: Cancel
	r.s.WaitFor(t, "beta cancelled", func(string) bool { return !look("lua/beta").live && gone(stage) })
	if a := look("lua/alpha"); !a.live || a.state != opSuspended {
		t.Fatalf("cancelling beta reached alpha: %+v", a)
	}
	// alpha, replaced twice and waiting all along, reopens once beta is answered
	r.clickText(t, "the Lua package alpha waits")
	r.s.WaitForText(t, "install the Lua package alpha?")
	if a := look("lua/alpha"); a.state != opConfirming || gone(a.stage) {
		t.Fatalf("alpha's toast after beta: %+v", a)
	}
}

// handingCore is a scripted Core whose agents take every task handed to them.
type handingCore struct{ *scriptedCore }

func (handingCore) HandTask(AgentRef, TaskRef, string, func(error)) error { return nil }

// scriptedCandidate is a live plugin candidate over a real, pristine stage, its card shown counted,
// on a scripted Core whose background answers wait until delivered and whose agents take tasks.
func scriptedCandidate(t *testing.T) (*scriptedCore, *candidates, *candidate, *int) {
	t.Helper()
	s := newScripted("kb")
	s.deferred = true
	cs := newCandidates(handingCore{s})
	repo := pluginRepo(t, "Echo", "true")
	c := &candidate{kind: "plugin", name: "echo", commit: runGit(t, repo, "rev-parse", "HEAD"), stage: repo, toasted: true}
	shown := 0
	if err := cs.begin(c, func(*candidate) { shown++ }, func(*candidate) {}); err != nil {
		t.Fatal(err)
	}
	return s, cs, c, &shown
}

// Two Lua cards in turn: the second question takes the generic card over, and the first, replaced,
// waits on its toast, still live; nothing ends it.
func TestASecondLuaCardKeepsTheFirstCandidate(t *testing.T) {
	s := newScripted("kb")
	cs := newCandidates(s)
	h := &Host{asker: newAsker(s), cands: cs}
	first := &candidate{kind: "lua", name: "alpha", commit: strings.Repeat("1", 40), stage: t.TempDir(), pkg: packageManifest{Name: "alpha"}}
	second := &candidate{kind: "lua", name: "beta", commit: first.commit, stage: t.TempDir(), pkg: packageManifest{Name: "beta"}}
	for _, c := range []*candidate{first, second} {
		if err := cs.begin(c, h.askPackage, h.hidePackage); err != nil {
			t.Fatal(err)
		}
		cs.show(c)
	}
	if first.state != opSuspended || cs.ops[first.key()] != first || gone(first.stage) {
		t.Fatalf("the replaced Lua candidate: state %d, live %v, stage gone %v", first.state, cs.ops[first.key()] == first, gone(first.stage))
	}
}

// While Audit… makes its copy, the candidate is copying: the old toast reopens nothing, Install and
// Cancel are refused, and another Audit does not start. The copy's answer then hands it over, and
// the candidate, suspended, reopens from its toast as before.
func TestAnAuditCopyInFlightCannotBeClaimed(t *testing.T) {
	s, cs, c, shown := scriptedCandidate(t)
	defer cs.Stopped()
	cs.auditing = c
	cs.auditWith(AgentEntry{Name: "Scribe", Ref: AgentRef{Codename: "scribe"}})
	cs.reopen(c) // the toast of the audit before
	if cs.claim(c) || cs.cancel(c, "") || *shown != 0 || c.state != opCopying {
		t.Fatalf("while copying: claimed or cancelled or reopened (state %d, shown %d)", c.state, *shown)
	}
	cs.audit(c)
	if cs.auditing == c {
		t.Fatal("another Audit started while the copy was made")
	}
	s.deliver(0)
	if c.state != opSuspended || len(c.auditCopies) != 1 {
		t.Fatalf("after the copy's answer: state %d, %d copies", c.state, len(c.auditCopies))
	}
	cs.reopen(c)
	if c.state != opConfirming || *shown != 1 {
		t.Fatalf("a handed-over audit's toast: state %d, shown %d", c.state, *shown)
	}
}

// A copy whose candidate ended while it was made is removed when its answer arrives; it never moves
// the ended candidate.
func TestAnAuditCopyAnsweredAfterItsCandidateEndedIsRemoved(t *testing.T) {
	s, cs, c, _ := scriptedCandidate(t)
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	cs.auditing = c
	cs.auditWith(AgentEntry{})
	cs.end(c, "cancelled")
	s.deliver(0, 1) // the end's removal, then the copy's answer
	if copies, _ := filepath.Glob(filepath.Join(root, "autodoc-audit-*")); len(copies) != 0 || c.state != opEnded {
		t.Fatalf("copies left %v, state %d", copies, c.state)
	}
}

// The session's end removes what candidates' workers made, answered or not: an audit copy whose
// answer never reached the loop, and a stage or copy a worker makes after the end is removed by that
// worker itself.
func TestTheSessionsEndRemovesWhatWorkersMadeAnsweredOrNot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	_, cs, c, _ := scriptedCandidate(t)
	cs.auditing = c
	cs.auditWith(AgentEntry{}) // the copy is made; its answer is held, never delivered
	copies, _ := filepath.Glob(filepath.Join(root, "autodoc-audit-*"))
	if len(copies) != 1 {
		t.Fatalf("the copy: %v", copies)
	}
	cs.Stopped()
	if !gone(copies[0]) || !gone(c.stage) {
		t.Fatalf("the session's end left the copy (%v) or the stage (%v)", !gone(copies[0]), !gone(c.stage))
	}

	// a worker still working when the session ended: what it makes after is its own to remove
	late := pluginRepo(t, "Echo", "true")
	if _, _, err := auditCopy(context.Background(), &candidate{name: "late", stage: late, commit: runGit(t, late, "rev-parse", "HEAD")}, cs.allocs); !errors.Is(err, errSessionEnded) {
		t.Errorf("a copy made after the end: %v", err)
	}
	plugins := t.TempDir()
	if _, _, err := cloneStage(context.Background(), plugins, "file://"+pluginRepo(t, "Echo", "true"), cs.allocs); !errors.Is(err, errSessionEnded) {
		t.Errorf("a stage made after the end: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(root, "autodoc-audit-*")); len(left) != 0 || len(entries(t, plugins)) != 0 {
		t.Errorf("a worker after the end left %v and %v", left, entries(t, plugins))
	}
}

// heldGit puts a git on PATH whose clone waits, once it is running, until release is called: a
// worker caught between holding its directory and finishing its work.
func heldGit(t *testing.T) (running func() bool, release func()) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin, controls := t.TempDir(), t.TempDir()
	ready, go_ := filepath.Join(controls, "ready"), filepath.Join(controls, "release")
	script := "#!/bin/sh\nif [ \"$1\" = clone ]; then\n touch " + shellQuote(ready) + "\n while [ ! -f " + shellQuote(go_) +
		" ]; do sleep 0.01; done\nfi\nexec " + shellQuote(real) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() bool { return !gone(ready) }, func() { _ = os.WriteFile(go_, nil, 0o600) }
}

// The session ends while a worker still writes into the directory it already holds (a clone
// running): the end removes it, the clone writes it again, and the worker's last word removes it
// for good and answers that the session ended. A stage and an audit copy alike.
func TestTheSessionsEndWhileAWorkerStillWrites(t *testing.T) {
	repo := pluginRepo(t, "Echo", "true")
	head := runGit(t, repo, "rev-parse", "HEAD")
	for _, kind := range []string{"a stage", "an audit copy"} {
		t.Run(kind, func(t *testing.T) {
			tmp, parent := t.TempDir(), filepath.Join(t.TempDir(), "plugins")
			t.Setenv("TMPDIR", tmp)
			running, release := heldGit(t)
			own := newAllocs()
			answer := make(chan error, 1)
			go func() {
				var err error
				if kind == "a stage" {
					_, _, err = cloneStage(context.Background(), parent, "file://"+repo, own)
				} else {
					_, _, err = auditCopy(context.Background(), &candidate{name: "echo", stage: repo, commit: head}, own)
				}
				answer <- err
			}()
			deadline := time.Now().Add(10 * time.Second)
			for !running() {
				if time.Now().After(deadline) {
					t.Fatal("the clone never started")
				}
				time.Sleep(time.Millisecond)
			}
			own.close() // the session ends while the clone runs
			release()
			select {
			case err := <-answer:
				if !errors.Is(err, errSessionEnded) {
					t.Fatalf("the worker answered %v; want the session ended", err)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the worker never answered")
			}
			stages, _ := os.ReadDir(parent)
			copies, _ := filepath.Glob(filepath.Join(tmp, "autodoc-audit-*"))
			if len(stages) != 0 || len(copies) != 0 {
				t.Fatalf("left behind: stages %v, copies %v", stages, copies)
			}
		})
	}
}
