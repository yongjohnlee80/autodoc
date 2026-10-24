package tui

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// The cells of ADR 1792300990 §5: a plugin's session slot, the reason it is closed, a command's
// arguments, and a plugin's Lua package. The plugin is the test binary in mode "session" (sessioned,
// plugins_test.go), which says its slot's values reversed: a value itself appearing in a log or a
// notice is the host's leak.

// sessionCommands are a session plugin's [[commands]]: login sets its slot, echo says its args.
const sessionCommands = "[[commands]]\nid = \"login\"\ntitle = \"Log in\"\nkey = \"l\"\n" +
	"[[commands]]\nid = \"echo\"\ntitle = \"Echo\"\nkey = \"e\"\n"

func slotOf(r *running, name string) map[string]string {
	return onLoop(r, func() map[string]string { return maps.Clone(r.h.plugins.sessions[name]) })
}

// seed puts a slot in the host's keeping, on the loop, as a run before would have.
func seed(pl *plugins, name, token string) {
	if pl.sessions == nil {
		pl.sessions = map[string]map[string]string{}
	}
	pl.sessions[name] = map[string]string{"token": token}
}

func runOf(r *running, name string) *pluginRun {
	return onLoop(r, func() *pluginRun { return r.h.plugins.running[name] })
}

// pluginCommand runs a plugin's command with args, as autodoc.plugin.run does.
func (r *running) pluginCommand(name, id string, args ...string) {
	onLoop(r, func() bool { r.h.plugins.runPluginCommandArgs(name, id, args); return true })
}

// turnOff is Plugins › <name> › Turn off.
func (r *running) turnOff(name string) {
	onLoop(r, func() bool { r.h.plugins.pluginEntry(pluginTarget(name, targetClose)); return true })
}

// remove is Manage plugins › Remove, answered yes.
func (r *running) remove(t *testing.T, dir, name string) {
	t.Helper()
	onLoop(r, func() bool { r.h.plugins.removePluginConfirmed(filepath.Join(dir, name)); return true })
	r.waitNotice(t, "removed "+name)
}

func waitSlot(t *testing.T, r *running, name string, want map[string]string) {
	t.Helper()
	r.s.WaitFor(t, fmt.Sprintf("%s's slot %v", name, want), func(string) bool { return reflect.DeepEqual(slotOf(r, name), want) })
}

func waitExited(t *testing.T, run *pluginRun) {
	t.Helper()
	select {
	case <-run.exited:
	case <-time.After(20 * time.Second):
		t.Fatalf("%s did not stop", run.e.m.Name)
	}
}

// noLeak fails when a value appears in a plugin's log or in a notice.
func noLeak(t *testing.T, r *running, logs string, values ...string) {
	t.Helper()
	ds, _ := os.ReadDir(logs)
	for _, d := range ds {
		b, _ := os.ReadFile(filepath.Join(logs, d.Name()))
		for _, v := range values {
			if strings.Contains(string(b), v) {
				t.Errorf("%s holds the value %q:\n%s", d.Name(), v, b)
			}
		}
	}
	for _, n := range onLoop(r, func() []notice { return r.h.noticeLog.items }) {
		for _, v := range values {
			if strings.Contains(n.text, v) {
				t.Errorf("a notice holds the value %q: %s", v, n.text)
			}
		}
	}
}

// TestTheToggleHidesACardSignedIn (§5.1): the toggle on a shown card hides it and shows it again,
// the same run all along and its slot unchanged; Turn off stops it with reason off, and its next
// start is handed the slot.
func TestTheToggleHidesACardSignedIn(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "card", "dialog", "session", rightCard+
		"[[commands]]\nid = \"toggle\"\ntitle = \"Show the card\"\nkey = \"s\"\n"+sessionCommands)
	r, logs := pluginTUI(t, dir)

	r.pluginCommand("card", "login", "tok-alpha")
	waitSaid(t, logs, "card", "login ahpla-kot <nil>")
	waitSlot(t, r, "card", map[string]string{"token": "tok-alpha"})
	run := runOf(r, "card")

	r.pluginCommand("card", "toggle") // shown: hidden
	waitSaid(t, logs, "card", "hide")
	r.pluginCommand("card", "toggle") // hidden: shown
	waitSaid(t, logs, "card", "show")
	r.pluginCommand("card", "toggle")
	waitCount(t, logs, "card", "hide", 2)
	if runOf(r, "card") != run {
		t.Fatal("the toggle replaced the card's run")
	}
	select {
	case <-run.exited:
		t.Fatal("the toggle stopped the card's plugin")
	default:
	}
	if got := slotOf(r, "card"); !reflect.DeepEqual(got, map[string]string{"token": "tok-alpha"}) {
		t.Fatalf("the slot after the toggles: %v", got)
	}

	r.turnOff("card")
	waitExited(t, run)
	waitSaid(t, logs, "card", "reason off")
	noLeak(t, r, logs, "tok-alpha")
	r.pluginCommand("card", "echo", "again")
	waitSaid(t, logs, "card", "session {token=ahpla-kot}")
	waitSaid(t, logs, "card", `args echo ["again"]`)
	noLeak(t, r, logs, "tok-alpha")
}

// TestTheSlotOutlivesARunNotItsPlugin (§5.2): a slot set is handed to the next start; removed with
// its plugin, whether running or not, so a reinstall starts with none. A slot over a bound, or not
// of strings, is refused, the old one kept, and the log says why without a value; a protocol-6
// plugin's host.session is refused, and its plugin.open carries no slot.
func TestTheSlotOutlivesARunNotItsPlugin(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "svc", "service", "session", sessionCommands+"[[commands]]\nid = \"big\"\ntitle = \"Big\"\n")
	raw := "[[commands]]\nid = \"session\"\ntitle = \"Session\"\n[[commands]]\nid = \"bigsession\"\ntitle = \"Big\"\n" +
		"[[commands]]\nid = \"badsession\"\ntitle = \"Bad\"\n"
	installPlugin7(t, dir, "raw", "service", "rawcalls", raw)
	installPluginAt(t, dir, "old", "service", "rawcalls", raw, 6)
	installPluginAt(t, dir, "six", "service", "session", sessionCommands, 6)
	r, logs := pluginTUI(t, dir)

	r.pluginCommand("svc", "login", "tok-one")
	waitSaid(t, logs, "svc", "session none")
	waitSlot(t, r, "svc", map[string]string{"token": "tok-one"})
	r.pluginCommand("svc", "big") // the SDK refuses it before sending
	waitSaid(t, logs, "svc", "big true")
	run := runOf(r, "svc")
	r.turnOff("svc")
	waitExited(t, run)
	r.pluginCommand("svc", "echo")
	waitSaid(t, logs, "svc", "session {token=eno-kot}")

	r.remove(t, dir, "svc") // running
	waitSlot(t, r, "svc", nil)
	installPlugin7(t, dir, "svc", "service", "session", sessionCommands)
	onLoop(r, func() bool { r.h.plugins.loadPlugins(); return true })
	r.pluginCommand("svc", "login", "tok-two")
	waitSaid(t, logs, "svc", "session none")
	waitSlot(t, r, "svc", map[string]string{"token": "tok-two"})
	run = runOf(r, "svc")
	r.turnOff("svc")
	waitExited(t, run)
	r.remove(t, dir, "svc") // not running: its slot goes all the same
	waitSlot(t, r, "svc", nil)

	r.pluginCommand("raw", "session")
	waitSlot(t, r, "raw", map[string]string{"token": "tok-raw"})
	r.pluginCommand("raw", "bigsession")
	r.pluginCommand("raw", "badsession")
	r.pluginCommand("raw", "ping") // after both: their refusals are logged by the time pong is
	waitSaid(t, logs, "raw", "pong")
	b, _ := os.ReadFile(filepath.Join(logs, "raw.log"))
	for _, want := range []string{"host.session refused: over the slot's bounds", "host.session refused: malformed"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("raw's log lacks %q:\n%s", want, b)
		}
	}
	if got := slotOf(r, "raw"); !reflect.DeepEqual(got, map[string]string{"token": "tok-raw"}) {
		t.Fatalf("a refused slot replaced the old one: %v", got)
	}

	r.pluginCommand("old", "session")
	r.pluginCommand("old", "ping")
	waitSaid(t, logs, "old", "pong")
	if b, _ := os.ReadFile(filepath.Join(logs, "old.log")); !strings.Contains(string(b), "host.session refused: the plugin's manifest says a protocol before 7") {
		t.Errorf("old's log:\n%s", b)
	}
	if got := slotOf(r, "old"); got != nil {
		t.Fatalf("a protocol-6 plugin set a slot: %v", got)
	}

	onLoop(r, func() bool { // a slot the host holds is not sent at protocol 6
		seed(r.h.plugins, "six", "tok-six")
		return true
	})
	r.pluginCommand("six", "echo")
	waitSaid(t, logs, "six", "session none")
	noLeak(t, r, logs, "tok-one", "tok-two", "tok-big", "tok-six")
}

// TestAPluginIsToldWhyItCloses (§5.3): off, update, remove, failed and closed (a modal dialog's Esc,
// and a dialog's own host.close) each reach a cooperative plugin's CloseReason; a second stop keeps
// the first's reason. An update's old run cannot write the slot after its stop began.
func TestAPluginIsToldWhyItCloses(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "off", "service", "session", sessionCommands)
	installPlugin7(t, dir, "gone", "service", "session", sessionCommands)
	installPlugin7(t, dir, "fault", "service", "session", sessionCommands)
	installPlugin7(t, dir, "twice", "service", "session", sessionCommands)
	installPlugin7(t, dir, "modal", "dialog", "session", "[dialog]\nwidth = 40\nheight = 6\n")
	installPlugin7(t, dir, "selfclose", "dialog", "session", "[dialog]\nwidth = 40\nheight = 6\n")
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	commitSessionPlugin(t, repo, "Up one")
	r, logs := pluginTUI(t, dir)

	for _, name := range []string{"off", "gone", "fault", "twice"} {
		r.pluginCommand(name, "echo")
		waitSaid(t, logs, name, "session none")
	}
	stop := func(name string, how func(*pluginRun)) {
		run := runOf(r, name)
		onLoop(r, func() bool { how(run); return true })
		waitExited(t, run)
	}
	r.turnOff("off")
	waitSaid(t, logs, "off", "reason off")
	stop("fault", func(run *pluginRun) { run.close("a test's fault") })
	waitSaid(t, logs, "fault", "reason failed")
	stop("twice", func(run *pluginRun) { run.stopFor(plugin.CloseOff, ""); run.stopFor(plugin.CloseRemove, "") })
	if got := waitSaid(t, logs, "twice", "reason "); !slices.Contains(got, "reason off") || slices.Contains(got, "reason remove") {
		t.Fatalf("two stops: %q, want the first's reason alone", got)
	}
	r.remove(t, dir, "gone")
	waitSaid(t, logs, "gone", "reason remove")

	r.openPlugin("modal")
	waitSaid(t, logs, "modal", "session none")
	r.keys(t, esc())
	waitSaid(t, logs, "modal", "reason closed")
	r.openPlugin("selfclose")
	waitSaid(t, logs, "selfclose", "session none")
	r.keys(t, key('q')) // echo's q: host.close
	waitSaid(t, logs, "selfclose", "reason closed")

	// the update: through the manager, its old run stopped with reason update, the slot kept
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added up at")
	r.pluginCommand("up", "login", "tok-up")
	waitSlot(t, r, "up", map[string]string{"token": "tok-up"})
	old := runOf(r, "up")
	commitSessionPlugin(t, repo, "Up two")
	r.h.p.Post(r.h.plugins.managePlugins)
	i := -1
	r.s.WaitFor(t, "up managed", func(string) bool {
		i = onLoop(r, func() int {
			return slices.IndexFunc(r.h.plugins.managedPlugins.All(), func(m managedPlugin) bool { return m.e.m.Name == "up" })
		})
		return i >= 0
	})
	r.h.p.Post(func() { r.h.plugins.startUpdatePlugin(i) })
	r.question(t)
	r.keys(t, key('y'))
	waitExited(t, old)
	r.waitNotice(t, "updated up at")
	waitSaid(t, logs, "up", "reason update") // read before the next start truncates its log
	onLoop(r, func() bool { r.h.plugins.acceptSession(old, map[string]string{"token": "tok-stale"}); return true })
	r.pluginCommand("up", "echo")
	waitSaid(t, logs, "up", "session {token=pu-kot}")
	if got := slotOf(r, "up"); !reflect.DeepEqual(got, map[string]string{"token": "tok-up"}) {
		t.Fatalf("the old run's late write reached the slot: %v", got)
	}
	noLeak(t, r, logs, "tok-up", "tok-stale")
}

// commitSessionPlugin commits a protocol-7 session service, named up, into repo.
func commitSessionPlugin(t *testing.T, repo, title string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	toml := fmt.Sprintf("name = \"up\"\ntitle = %q\nkind = \"service\"\nprotocol = 7\ncommand = [%q, %q, \"session\"]\n%s[install]\nbuild = [\"true\"]\n",
		title, exe, testPluginArg, sessionCommands)
	if err := os.WriteFile(filepath.Join(repo, "plugin.toml"), []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "plugin.toml")
	runGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", title)
}

// TestTheSlotIsFencedToTheLiveRun (§5.3b): a write from a run whose stop began, or that another run
// replaced, is dropped; one from the live run is kept; after a removal none brings the slot back,
// and a reinstall starts with none. The posted acceptSession is called in the order the cell sets:
// the old run's write held past its replacement.
func TestTheSlotIsFencedToTheLiveRun(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "svc", "service", "session", sessionCommands)
	r, logs := pluginTUI(t, dir)
	accept := func(run *pluginRun, v string) {
		onLoop(r, func() bool { r.h.plugins.acceptSession(run, map[string]string{"token": v}); return true })
	}

	r.pluginCommand("svc", "login", "tok-a")
	waitSlot(t, r, "svc", map[string]string{"token": "tok-a"})
	first := runOf(r, "svc")
	r.turnOff("svc")
	accept(first, "tok-stopping") // its stop began: dropped
	waitExited(t, first)
	r.pluginCommand("svc", "echo")
	waitSaid(t, logs, "svc", "session {token=a-kot}")
	second := runOf(r, "svc")
	accept(first, "tok-replaced") // another run is the live one: dropped
	if got := slotOf(r, "svc"); !reflect.DeepEqual(got, map[string]string{"token": "tok-a"}) {
		t.Fatalf("an old run's write reached the slot: %v", got)
	}
	accept(second, "tok-live") // the control: the live run's write is kept
	waitSlot(t, r, "svc", map[string]string{"token": "tok-live"})

	r.remove(t, dir, "svc")
	accept(second, "tok-zombie") // removed: dropped
	if got := slotOf(r, "svc"); got != nil {
		t.Fatalf("a removed plugin's slot came back: %v", got)
	}
	installPlugin7(t, dir, "svc", "service", "session", sessionCommands)
	onLoop(r, func() bool { r.h.plugins.loadPlugins(); return true })
	r.pluginCommand("svc", "echo")
	waitSaid(t, logs, "svc", "session none")
	noLeak(t, r, logs, "tok-a", "tok-stopping", "tok-replaced", "tok-live", "tok-zombie")
}

// TestQuitClearsTheSlotsOfEveryRun (§5.3, §5.3b): AutoDoc quitting tells a cooperative plugin quit,
// stops one that never answered its handshake by the signals, and drops every slot; meanwhile a
// plugin writes its slot as fast as it can, turned off and started again, under -race.
func TestQuitClearsTheSlotsOfEveryRun(t *testing.T) {
	shorten(t, 20*time.Second, 300*time.Millisecond, 300*time.Millisecond)
	dir := t.TempDir()
	installPlugin7(t, dir, "coop", "service", "session", sessionCommands)
	installPlugin7(t, dir, "flood", "service", "session", sessionCommands+"[[commands]]\nid = \"flood\"\ntitle = \"Flood\"\n")
	installPlugin7(t, dir, "mute", "service", "silent", sessionCommands)
	r, logs := pluginTUI(t, dir)

	r.pluginCommand("coop", "login", "tok-coop")
	waitSlot(t, r, "coop", map[string]string{"token": "tok-coop"})
	onLoop(r, func() bool { seed(r.h.plugins, "mute", "tok-mute"); return true })
	r.pluginCommand("mute", "echo") // started; it never answers

	r.pluginCommand("flood", "flood")
	r.s.WaitFor(t, "the flood", func(string) bool { return slotOf(r, "flood") != nil })
	run := runOf(r, "flood")
	r.turnOff("flood")
	waitExited(t, run)
	r.pluginCommand("flood", "flood")
	r.s.WaitFor(t, "the flood again", func(string) bool {
		return onLoop(r, func() bool {
			again := r.h.plugins.running["flood"]
			return again != nil && again != run && again.ready
		})
	})

	onLoop(r, func() bool { r.h.plugins.stopPlugins(); return true })
	waitSaid(t, logs, "coop", "reason quit")
	if got := onLoop(r, func() map[string]map[string]string { return r.h.plugins.sessions }); len(got) != 0 {
		t.Fatalf("slots after quit: %d", len(got))
	}
	time.Sleep(100 * time.Millisecond) // writes posted before the stop are past their fence
	if got := onLoop(r, func() int { return len(r.h.plugins.sessions) }); got != 0 {
		t.Fatalf("a write after quit brought %d slots back", got)
	}
	noLeak(t, r, logs, "tok-coop", "tok-mute", "tok-flood")
}

// installPackage writes a plugin's Lua package, <dir>/<name>/lua/: its package.toml (requires
// listed) and its init.lua.
func installPackage(t *testing.T, dir, name, requires, init string) {
	t.Helper()
	writeFiles(t, filepath.Join(dir, name, "lua"), map[string]string{
		"package.toml": fmt.Sprintf("name = %q\nversion = \"1.0.0\"\ndescription = \"its commands\"\nrequires = [%s]\n", name, requires),
		"init.lua":     init,
	})
}

// runInsert is a Lua command that runs fn's body and inserts what it returns.
func runInsert(name, body string) string {
	return fmt.Sprintf("autodoc.command{ name = %q, run = function(ed) local ok, err = %s; ed:insert(tostring(ok) .. \" \" .. tostring(err)) end }\n", name, body)
}

// TestAPluginsPackageRunsItsOwnPluginOnly (§5.4, §5.5): a plugin's lua/ loads as plugin:<name>; its
// autodoc.plugin.run reaches its own plugin with args, at its init and from its command later; two
// plugins with a command id in common each get their own package's; an undeclared id is unknown; a
// user's script or package is refused; args to a protocol-6 plugin need protocol 7.
func TestAPluginsPackageRunsItsOwnPluginOnly(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "own", "service", "session", sessionCommands)
	installPackage(t, dir, "own", "", `autodoc.plugin.run("echo", "a", "b")
`+runInsert("own-run", `autodoc.plugin.run("echo", "c", "d")`)+runInsert("own-unknown", `autodoc.plugin.run("nope")`))
	installPlugin7(t, dir, "twin", "service", "session", sessionCommands)
	installPackage(t, dir, "twin", "", runInsert("twin-run", `autodoc.plugin.run("echo", "t")`))
	installPluginAt(t, dir, "six", "service", "session", sessionCommands, 6)
	installPackage(t, dir, "six", "", runInsert("six-run", `autodoc.plugin.run("echo", "x")`)+
		runInsert("six-bare", `autodoc.plugin.run("echo")`))
	luaDir := t.TempDir()
	writeFiles(t, luaDir, map[string]string{
		"user.lua":        runInsert("user-run", `autodoc.plugin.run("echo", "u")`),
		"pk/package.toml": "name = \"pk\"\nversion = \"1.0.0\"\ndescription = \"a user's\"\n",
		"pk/init.lua":     runInsert("pk-run", `autodoc.plugin.run("echo", "p")`),
	})
	r, logs := pluginLuaTUI(t, dir, nil, luaDir)

	waitSaid(t, logs, "own", `args echo ["a" "b"]`) // from its init
	for _, c := range []struct{ cmd, want string }{
		{"own-run", "true nil"},
		{"own-unknown", "false unknown: nope"},
		{"twin-run", "true nil"},
		{"user-run", "false autodoc.plugin.run: not a plugin's package"},
		{"pk-run", "false autodoc.plugin.run: not a plugin's package"},
		{"six-run", "false autodoc.plugin.run: six needs protocol 7 for arguments; its manifest says 6"},
		{"six-bare", "true nil"},
	} {
		r.page("", 1, 1)
		r.mustRun(t, c.cmd)
		r.wantText(t, ":"+c.cmd, c.want)
	}
	waitSaid(t, logs, "own", `args echo ["c" "d"]`)
	waitSaid(t, logs, "twin", `args echo ["t"]`)
	waitSaid(t, logs, "six", "args echo []") // protocol 6: the command, never args
	for _, s := range said(t, logs, "own") {
		if strings.Contains(s, `"t"`) || strings.Contains(s, `"u"`) || strings.Contains(s, `"p"`) {
			t.Fatalf("own was sent another's command: %q", s)
		}
	}
	if slices.ContainsFunc(said(t, logs, "twin"), func(s string) bool { return strings.Contains(s, `"a"`) || strings.Contains(s, `"c"`) }) {
		t.Fatalf("twin was sent own's command: %q", said(t, logs, "twin"))
	}
}

// TestAPluginsPackageIsGated (§5.4): its requires are asked naming the plugin, and until granted a
// gated call answers "needs …: not granted"; the grant is the plugin's script's.
func TestAPluginsPackageIsGated(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "gated", "service", "session", sessionCommands)
	installPackage(t, dir, "gated", `"workspace.read"`, runInsert("gated-read",
		`autodoc.doc.read(autodoc.current_workspace(), "a.md", function() end)`))
	r, _ := pluginTUI(t, dir)
	r.waitNamespaces(t)
	r.s.WaitForText(t, "let the plugin gated's Lua package use workspace.read?")
	r.page("", 1, 1)
	r.mustRun(t, "gated-read")
	r.wantText(t, ":gated-read ungranted", "nil needs workspace.read: not granted") // ok, err as autodoc.doc.read answers
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	r.s.WaitFor(t, "the grant", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.luaGrants["plugin:gated"]["workspace.read"] })
	})
	r.page("", 1, 1)
	r.mustRun(t, "gated-read")
	r.wantText(t, ":gated-read granted", "nil nil")
}

// TestAUsersPackageReservesItsName (§5.6): a user's package of a plugin's name loads, and nothing
// of the plugin's runs (no init, so no sentinel command reaches the plugin; no grant asked); a
// broken user package still reserves the name; with the user's gone, the plugin's loads; and the
// plugin removed, its package's commands go with it.
func TestAUsersPackageReservesItsName(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "res", "service", "session", sessionCommands)
	installPackage(t, dir, "res", `"workspace.read"`, `autodoc.plugin.run("echo", "sentinel")
autodoc.command{ name = "plugin-res", run = function(ed) ed:insert("the plugin's") end }
`)
	luaDir := t.TempDir()
	writeFiles(t, luaDir, map[string]string{
		"res/package.toml": "name = \"res\"\nversion = \"1.0.0\"\ndescription = \"the user's\"\n",
		"res/init.lua":     `autodoc.command{ name = "user-res", run = function(ed) ed:insert("the user's") end }`,
	})
	r, logs := pluginLuaTUI(t, dir, nil, luaDir)
	r.waitNotice(t, "the plugin res's Lua package is replaced by yours, lua/res")
	r.page("", 1, 1)
	r.mustRun(t, "user-res")
	r.wantText(t, ":user-res", "the user's")
	unloaded := func(when string) {
		t.Helper()
		if err := r.luaRun("plugin-res"); err == nil {
			t.Fatalf("%s: the plugin's package loaded", when)
		}
		if runOf(r, "res") != nil {
			t.Fatalf("%s: the plugin's init ran (its sentinel started the plugin)", when)
		}
		if strings.Contains(r.s.String(), "let the plugin res's Lua package") {
			t.Fatalf("%s: the plugin's grant was asked", when)
		}
	}
	unloaded("the user's package loaded")

	writeFiles(t, luaDir, map[string]string{"res/package.toml": "name = \"other\"\n"}) // broken
	r.clearNotices()
	if err := onLoop(r, r.h.reloadScripts); err != nil {
		t.Logf("the reload: %v", err) // the broken package is its error
	}
	r.waitNotice(t, "the plugin res's Lua package is replaced by yours, lua/res")
	unloaded("the user's package broken")

	if err := os.RemoveAll(filepath.Join(luaDir, "res")); err != nil {
		t.Fatal(err)
	}
	if err := onLoop(r, r.h.reloadScripts); err != nil {
		t.Fatal(err)
	}
	waitSaid(t, logs, "res", `args echo ["sentinel"]`)
	r.s.WaitForText(t, "let the plugin res's Lua package use workspace.read?")
	r.page("", 1, 1)
	r.mustRun(t, "plugin-res")
	r.wantText(t, ":plugin-res", "the plugin's")

	r.remove(t, dir, "res")
	r.s.WaitFor(t, "its commands gone", func(string) bool { return r.luaRun("plugin-res") != nil })
}

// TestAPluginOpensAPathAndUpdatesItself (ADR 1792301900 §4.10): file.open_path opens an absolute
// path, as File › Open any file does, and refuses a relative one; plugins.update from a plugin may
// name only itself (a plugin put in the folder by hand is then told to update it there).
func TestAPluginOpensAPathAndUpdatesItself(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "opener", "service", "rawcalls",
		"calls = [\"file.open_path\", \"plugins.update\"]\n[[commands]]\nid = \"callwith\"\ntitle = \"Call\"\n[[commands]]\nid = \"ping\"\ntitle = \"Ping\"\n")
	installPlugin7(t, dir, "other", "service", "session", sessionCommands)
	r, logs := pluginTUI(t, dir)
	r.pluginCommand("opener", "ping")
	waitSaid(t, logs, "opener", "pong")
	r.s.WaitFor(t, "opener ready", func(string) bool { return onLoop(r, func() bool { return r.h.plugins.running["opener"].ready }) })

	abs := filepath.Join(t.TempDir(), "note.sql")
	if err := os.WriteFile(abs, []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.pluginCommand("opener", "callwith", "1", "file.open_path", abs)
	waitSaid(t, logs, "opener", "result 1 ")
	r.waitOpen(t, "", abs)
	r.pluginCommand("opener", "callwith", "2", "file.open_path", "note.sql")
	waitSaid(t, logs, "opener", "result 2 ")
	r.pluginCommand("opener", "callwith", "3", "plugins.update", "other")
	waitSaid(t, logs, "opener", "result 3 ")
	r.pluginCommand("opener", "callwith", "4", "plugins.update", "opener")
	waitSaid(t, logs, "opener", "result 4 ")
	want := []string{"result 1 true ", "result 2 false failed file.open_path: takes one absolute path",
		"result 3 false refused plugins.update: a plugin may update only itself, opener", "result 4 true "}
	if got := results(t, logs, "opener"); !slices.Equal(got, want) {
		t.Fatalf("the calls:\n got %q\nwant %q", got, want)
	}
	r.waitNotice(t, "opener not updated: it was put in the folder by hand: update it there")
}

// TestABrokenPluginPackageIsSaidAndNotRecorded: a plugin whose lua/ has no package.toml has no
// package, silently; one whose package.toml names another plugin, is not TOML, or has no init.lua
// is said and loads nothing; one whose init fails is said, and its identity is not kept, so
// autodoc.plugin.run from what it registered before failing is refused.
func TestABrokenPluginPackageIsSaidAndNotRecorded(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"bare", "misnamed", "badtoml", "noinit", "fails"} {
		installPlugin7(t, dir, name, "service", "session", sessionCommands)
	}
	writeFiles(t, filepath.Join(dir, "bare", "lua"), map[string]string{"init.lua": `error("never run")`})
	installPackage(t, dir, "misnamed", "", `error("never run")`)
	writeFiles(t, filepath.Join(dir, "misnamed", "lua"), map[string]string{"package.toml": "name = \"other\"\n"})
	writeFiles(t, filepath.Join(dir, "badtoml", "lua"), map[string]string{"package.toml": "name = [\n", "init.lua": ""})
	writeFiles(t, filepath.Join(dir, "noinit", "lua"), map[string]string{"package.toml": "name = \"noinit\"\n"})
	installPackage(t, dir, "fails", "", runInsert("fails-run", `autodoc.plugin.run("echo", "late")`)+`error("the init failed")`)
	r, logs := pluginTUI(t, dir)
	for _, want := range []string{
		`the plugin misnamed's Lua package: package.toml names "other", but the plugin is misnamed`,
		"the plugin badtoml's Lua package: package.toml:",
		"the plugin noinit's Lua package: no init.lua in its lua folder",
		"the init failed",
	} {
		r.waitNotice(t, want)
	}
	for _, n := range onLoop(r, func() []notice { return r.h.noticeLog.items }) {
		if strings.Contains(n.text, "bare") || strings.Contains(n.text, "never run") {
			t.Fatalf("a lua/ with no package.toml was read: %s", n.text)
		}
	}
	if got := onLoop(r, func() int { return len(r.h.pluginPackages) }); got != 0 {
		t.Fatalf("%d broken packages recorded", got)
	}
	r.page("", 1, 1)
	if err := r.luaRun("fails-run"); err == nil { // registered before the init failed: refused
		r.wantText(t, ":fails-run", "false autodoc.plugin.run: not a plugin's package")
	}
	if slices.ContainsFunc(said(t, logs, "fails"), func(s string) bool { return strings.Contains(s, "late") }) {
		t.Fatal("a failed package's command reached its plugin")
	}
}

// A run's shutdown is safe from any goroutine, beside the loop's stop transition: the reason it
// sends is read under the run's lock, never raced (run under -race to observe it).
func TestAShutdownBesideTheStopTransitionDoesNotRace(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "both", "service", "session", sessionCommands)
	r, logs := pluginTUI(t, dir)
	r.pluginCommand("both", "echo")
	waitSaid(t, logs, "both", "session none")
	run := runOf(r, "both")
	done := make(chan struct{})
	go func() { run.shutdown(); close(done) }() // as a test's cleanup, or a goroutine of the host's
	onLoop(r, func() bool { run.stopFor(plugin.CloseOff, ""); return true })
	<-done
	waitExited(t, run)
}
