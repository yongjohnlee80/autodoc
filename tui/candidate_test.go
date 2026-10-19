package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	r.s.WaitForText(t, "audit of echo handed to Scribe")
	if strings.Contains(r.s.String(), "Yes, at my own risk") || r.card() != nil {
		t.Fatal("the card stayed up while the audit was handed over")
	}
	if err := os.WriteFile(filepath.Join(copy1, "agent-was-here"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.clickText(t, "audit of echo handed")
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

	r.clickText(t, "audit of echo handed")
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
