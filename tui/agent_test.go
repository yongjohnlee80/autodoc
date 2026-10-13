//go:build linux || darwin

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// agent_test.go: the agent — SPC g starts the default profile's command in AutoDoc's own folder for
// the workspace, told about it; hidden, it keeps running; the profiles are added, edited, made the
// default and removed in System › Agent profiles…, and Use switches, asking first.

// fakeAgent is an agent CLI that writes where it started, with what, what it was told, and which
// autodoc it calls, to report (the screen wraps long paths), calls it, then echoes what it is sent
// until told to exit.
func fakeAgent(t *testing.T) (agent, report string) {
	t.Helper()
	dir := t.TempDir()
	p, report := filepath.Join(dir, "fake-agent"), filepath.Join(dir, "report")
	script := `#!/bin/sh
{
echo "cwd=$(pwd)"
echo "args=$*"
echo "env=$AUTODOC_WORKSPACE|$AUTODOC_ROOT|$FAKE_GREETING"
LC_ALL=C ls
echo "autodoc=$(command -v autodoc)"
} > ` + shellQuote(report) + `
autodoc --call workspace.list
echo "started $1"
while read line; do
  case "$line" in
    exit*) exit ${line#exit } ;;
  esac
  echo "heard-$line"
done
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p, report
}

// agentConfig is the config the agent tests' TUI runs on: not the default, and one word only quoted.
const agentConfig = "/a config's dir/autodoc.toml"

// runAgentTUI runs the TUI with the profiles given, the default named def, its agent folder dir, on
// agentConfig, with no autodoc installed: the autodoc it hands its agent is a fake that writes the
// arguments it is called with to calls.
func runAgentTUI(t *testing.T, dir string, profiles []agentProfile, def string) (d *daemon, r *running, calls string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin")
	bins := filepath.Join(t.TempDir(), "a build's dir") // a path the shell must have quoted
	if err := os.Mkdir(bins, 0o700); err != nil {
		t.Fatal(err)
	}
	calls = filepath.Join(bins, "calls")
	bin := filepath.Join(bins, "autodoc-build")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" >> "+shellQuote(calls)+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prefs := map[string]string{prefAgentProfiles: agentProfilesJSON(profiles)}
	if def != "" {
		prefs[prefAgentDefault] = def
	}
	d = startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	r = runTUI(t, NewSession(d.sock, nil), Options{AgentDir: dir, ConfigPath: agentConfig})
	onLoop(r, func() bool { r.h.agents.agentBinPath = bin; return true })
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	// The stored preferences carry the profiles and the default, and their answer is not ordered
	// with the listing's: SPC g before they land finds no default and opens the profiles.
	r.s.WaitFor(t, "the preferences read", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.started && r.h.agents.defaultProfile() == def && len(r.h.agents.profiles()) == len(profiles)
		})
	})
	return d, r, calls
}

func (r *running) agentRunningNow() bool {
	return onLoop(r, func() bool {
		for _, entry := range r.h.agents.AgentEntries() {
			if entry.Ref.Codename == r.h.agents.selectedAgent {
				return entry.Running
			}
		}
		return false
	})
}

func (r *running) noticed(text string) bool {
	return onLoop(r, func() bool {
		for _, n := range r.h.noticeLog.items {
			if strings.Contains(n.text, text) {
				return true
			}
		}
		return false
	})
}

func TestTheAgentStartsTheDefaultInItsOwnFolderToldAboutTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	agent, report := fakeAgent(t)
	// as a user may write a command: a variable set before it, its program's path quoted
	_, r, calls := runAgentTUI(t, dir, []agentProfile{
		{Name: "other", Command: "false"},
		{Name: "fake", Command: "FAKE_GREETING=hi " + shellQuote(agent) + " --root {root} --ws {workspace}"},
	}, "fake")
	root := onLoop(r, func() string { ws, _ := r.h.activeWorkspaceInfo(); return ws.root })
	folder := filepath.Join(dir, "kb")

	r.leader(t, 'g')
	r.s.WaitForText(t, "agent · fake")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	// In AutoDoc's folder for the workspace, never the workspace's, its guides beside it; {root}
	// and {workspace} filled in, one shell word each; the environment naming the workspace.
	r.s.WaitForText(t, "started --root")
	if b, err := os.ReadFile(report); err != nil {
		t.Fatal(err)
	} else if want := "cwd=" + folder + "\nargs=--root " + root + " --ws kb\nenv=kb|" + root + "|hi" +
		"\nAGENTS.md\nCLAUDE.md\nbin\nautodoc=" + filepath.Join(folder, "bin", "autodoc") + "\n"; string(b) != want {
		t.Fatalf("the agent reported\n%s\nwant\n%s", b, want)
	}
	// Its autodoc, with none installed, is the TUI's build on the TUI's config, not the default one.
	r.s.WaitFor(t, "the agent's call", func(string) bool { b, _ := os.ReadFile(calls); return len(b) > 0 })
	if b, _ := os.ReadFile(calls); string(b) != "--config "+agentConfig+" --call workspace.list\n" {
		t.Fatalf("the agent's autodoc was called with %q", b)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		b, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			t.Fatal(err)
		}
		if g := string(b); !strings.Contains(g, "name: `kb`") || !strings.Contains(g, "root: `"+root+"`") ||
			!strings.Contains(g, "# AutoDoc") || !strings.Contains(g, "`--config "+agentConfig+"`") {
			t.Errorf("%s does not tell the agent the workspace and how to call AutoDoc:\n%.600s", name, g)
		}
	}

	// It hears what is typed.
	r.keys(t, decltest.Type("hello\r")...)
	r.s.WaitForText(t, "heard-hello")

	// Hidden, it lives on: SPC g again shows the same agent, not a new one.
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden", func(sc string) bool { return !strings.Contains(sc, "agent · fake") })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	if !r.agentRunningNow() {
		t.Fatal("the agent stopped while hidden")
	}
	r.leader(t, 'g')
	r.s.WaitForText(t, "heard-hello")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })

	// It exits: the notifications say so, and the next SPC g starts the default again.
	r.keys(t, decltest.Type("exit 3\r")...)
	r.s.WaitFor(t, "the exit in the notifications", func(string) bool { return r.noticed("fake exited (3)") })
	if n := onLoop(r, func() string { return r.h.agents.agentRunning }); n != "" {
		t.Fatalf("after its exit %q is still running", n)
	}

	// The card lists it.
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden", func(sc string) bool { return !strings.Contains(sc, "agent · fake") })
	r.keys(t, key(' '))
	r.s.WaitForText(t, "g  AI agent")
	r.keys(t, esc())
}

// waitStored waits for the daemon's store to hold the default and the profiles: the TUI writes its
// preferences after it shows them.
func (d *daemon) waitStored(t *testing.T, def, profiles string) {
	t.Helper()
	var got map[string]string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		got, _ = d.db.Preferences(context.Background())
		if got[prefAgentDefault] == def && got[prefAgentProfiles] == profiles {
			return
		}
	}
	t.Fatalf("stored: default %q, profiles %s; want %q, %s", got[prefAgentDefault], got[prefAgentProfiles], def, profiles)
}

// A profile whose program is not installed is the shell's to report: its 127 says the command was
// not found, and the next SPC g tries again.
func TestAnAgentNotInstalledSaysItsCommandWasNotFound(t *testing.T) {
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "gone", Command: "autodoc-no-such-agent --x"}}, "gone")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "not found", func(string) bool {
		return r.noticed("the agent gone's command was not found (127): check it in System › Agent profiles…")
	})
	if n := onLoop(r, func() string { return r.h.agents.agentRunning }); n != "" {
		t.Fatalf("after its command was not found %q is running", n)
	}
}

// With no profile SPC g opens the profiles: the first added becomes the default, a name another has
// is refused, an edit renames the default with it, and a removal leaves none. All of it is kept.
func TestAgentProfilesAreAddedEditedAndRemoved(t *testing.T) {
	d, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitForText(t, "no profiles yet")

	onLoop(r, func() bool {
		r.h.agents.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.agents.saveAgent(" claude ", " claude --model opus ")
		return true
	})
	onLoop(r, func() bool {
		r.h.agents.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.agents.saveAgent("codex", "codex")
		return true
	})
	r.s.WaitForText(t, "SPC g opens claude")
	onLoop(r, func() bool {
		r.h.agents.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.agents.saveAgent("codex", "codex --other")
		return true
	})
	r.s.WaitForText(t, "not saved: codename taken by codex")
	onLoop(r, func() bool {
		r.h.agents.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.agents.saveAgent("blank", "  ")
		return true
	})
	r.s.WaitForText(t, "not saved: a profile is a name and the command")

	onLoop(r, func() bool {
		r.h.agents.startEditAgent(0)
		r.h.closeDialog("agentEdit")
		r.h.agents.saveAgent("claude-opus", "claude --model opus")
		return true
	})
	r.s.WaitForText(t, "SPC g opens claude-opus")
	d.waitStored(t, "claude-opus", `[{"name":"claude-opus","command":"claude --model opus","codename":"claude"},{"name":"codex","command":"codex","codename":"codex"}]`)

	onLoop(r, func() bool { r.h.agents.makeAgentDefault(1); return true })
	r.s.WaitForText(t, "SPC g opens codex")
	onLoop(r, func() bool { r.h.agents.removeAgent(1); return true })
	r.s.WaitForText(t, "remove the agent profile?")
	r.keys(t, key('y'))
	r.s.WaitForText(t, "Make default marks the one SPC g opens")
	d.waitStored(t, "", `[{"name":"claude-opus","command":"claude --model opus","codename":"claude"}]`)
	// Profiles but no default: SPC g asks for one.
	onLoop(r, func() bool { r.h.closeDialog("agentProfiles"); r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the profiles, to make one the default", func(sc string) bool {
		return strings.Contains(sc, "Make default marks the one SPC g opens") && !onLoop(r, func() bool { return r.h.panelOpen["agent"] })
	})
	again := attached(t, d)
	if n := onLoop(again, func() int { return len(again.h.agents.profiles()) }); n != 1 {
		t.Errorf("a new TUI reads %d profiles, want the 1 kept", n)
	}
}

func TestAnExplicitlyClearedAgentDefaultStaysClearedAfterLoad(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		prefAgentProfiles: `[{"name":"A","command":"false","codename":"a"}]`,
		prefAgentDefault:  "A", prefAgentDefaultCodename: "",
	}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitFor(t, "stored cleared default loaded", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.started && len(r.h.agents.profiles()) == 1 && r.h.agents.defaultProfile() == ""
		})
	})
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitForText(t, "Make default marks the one SPC g opens")
	if code := onLoop(r, func() string { return r.h.agents.agentRunning }); code != "" {
		t.Fatalf("restart revived the old default identity %q", code)
	}
}

func TestLegacyAgentDefaultMigratesOnceAndNeverReadsItsMirrorAgain(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: map[string]string{
		prefAgentProfiles: `[{"name":"A","command":"false"}]`, prefAgentDefault: "A",
	}})
	sess := NewSession(d.sock, nil)
	var writes atomic.Int32
	sess.SetBeforeCall(func(method string, args []any) {
		if method == "preference.set" && args[0] == prefAgentDefaultCodename {
			writes.Add(1)
		}
	})
	r := runTUI(t, sess, Options{})
	r.s.WaitFor(t, "canonical default persisted", func(string) bool {
		m, err := d.db.Preferences(context.Background())
		return err == nil && m[prefAgentDefaultCodename] == "a"
	})
	if _, err := sess.Call(context.Background(), "preference.set", prefAgentDefault, "B"); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.loadPrefs(); return true })
	r.s.WaitFor(t, "mirror reloaded without retargeting", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.Value(prefAgentDefault) == "B" && r.h.agents.defaultProfile() == "a" })
	})
	if got := writes.Load(); got != 1 {
		t.Fatalf("canonical migration wrote %d times, want once", got)
	}
}

func TestRemoveAgentOnlyRemovesTheConfirmedProfile(t *testing.T) {
	profiles := []agentProfile{{Name: "alpha", Command: "alpha", Codename: "alpha"}, {Name: "beta", Command: "beta", Codename: "beta"}, {Name: "gamma", Command: "gamma", Codename: "gamma"}}
	_, r, _ := runAgentTUI(t, t.TempDir(), profiles, "beta")
	count := func() int { return onLoop(r, func() int { return len(r.h.agents.profiles()) }) }
	onLoop(r, func() bool { r.h.agents.removeAgent(1); return true })
	r.s.WaitForText(t, "remove the agent profile?")
	if count() != 3 {
		t.Fatal("asking removed a profile before an answer")
	}
	r.keys(t, enter()) // No is initially focused.
	r.s.WaitFor(t, "No closes the question", func(sc string) bool { return !strings.Contains(sc, "remove the agent profile?") })
	if count() != 3 {
		t.Fatal("No removed a profile")
	}
	onLoop(r, func() bool {
		r.h.agents.removeAgent(1)
		r.h.ask(Question{Title: "another question?", Text: "Nothing is removed."})
		return true
	})
	r.s.WaitForText(t, "another question?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "replacement closes", func(sc string) bool { return !strings.Contains(sc, "another question?") })
	if count() != 3 {
		t.Fatal("answering a replacement removed the old question's profile")
	}
	onLoop(r, func() bool {
		r.h.agents.removeAgent(1) // beta at row 1 when asked
		r.h.agents.c.SetPref(prefAgentProfiles, agentProfilesJSON([]agentProfile{profiles[1], profiles[0], profiles[2]}))
		return true
	})
	r.s.WaitForText(t, "remove the agent profile?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "beta removed", func(string) bool { return count() == 2 })
	got := onLoop(r, func() []agentProfile { return r.h.agents.profiles() })
	if got[0] != profiles[0] || got[1] != profiles[2] {
		t.Fatalf("Yes removed a changed row instead of beta: %v", got)
	}
	if def := onLoop(r, func() string { return r.h.agents.defaultProfile() }); def != "" {
		t.Errorf("removed profile is still default: %q", def)
	}
}

// A profile edited after the question opens is not the profile the user
// approved removing. Refuse that stale confirmation and keep the new command
// and default, instead of treating a matching name as enough identity.
func TestRemoveAgentRefusesAProfileEditedWhileAsked(t *testing.T) {
	profile := agentProfile{Name: "beta", Command: "beta --old"}
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{profile}, "beta")
	onLoop(r, func() bool {
		r.h.agents.removeAgent(0)
		profiles := r.h.agents.profiles()
		profiles[0].Command = "beta --new"
		r.h.agents.c.SetPref(prefAgentProfiles, agentProfilesJSON(profiles))
		return true
	})
	r.s.WaitForText(t, "remove the agent profile?")
	r.keys(t, key('y'))
	r.s.WaitFor(t, "the stale question closed", func(sc string) bool {
		return !strings.Contains(sc, "remove the agent profile?")
	})
	got := onLoop(r, func() []agentProfile { return r.h.agents.profiles() })
	if len(got) != 1 || got[0].Name != "beta" || got[0].Command != "beta --new" {
		t.Fatalf("stale Yes removed or rewrote the edited profile: %v", got)
	}
	if def := onLoop(r, func() string { return r.h.agents.defaultProfile() }); def != "beta" {
		t.Errorf("refused removal changed the default to %q", def)
	}
}

// Use starts another profile while the first keeps its own process and token.
func TestUsingAnotherAgentKeepsBothInstancesRunning(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{
		{Name: "one", Command: agent + " first"},
		{Name: "two", Command: agent + " second"},
	}, "one")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitForText(t, "started first")
	// The profiles mark the default and the one running; Use on the one running only shows it.
	onLoop(r, func() bool { r.h.agents.toggleAgent(); r.h.agents.openAgentProfiles(); return true })
	r.s.WaitForText(t, "●▶   one")
	onLoop(r, func() bool { r.h.agents.useAgent(0); return true })
	r.s.WaitFor(t, "the one running shown, unasked", func(sc string) bool {
		return strings.Contains(sc, "agent · one") && !strings.Contains(sc, "agent profiles") && r.focused("agentView")
	})
	onLoop(r, func() bool { r.h.agents.openAgentProfiles(); r.h.agents.useAgent(1); return true })
	r.s.WaitForText(t, "agent · two")
	r.s.WaitForText(t, "started second")
	entries := onLoop(r, func() []AgentEntry { return r.h.agents.AgentEntries() })
	if len(entries) != 2 || !entries[0].Running || !entries[1].Running || entries[0].Ref.Instance == entries[1].Ref.Instance {
		t.Fatalf("independent live instances lost: %+v", entries)
	}
	delivered := make(chan error, 1)
	onLoop(r, func() bool {
		if err := (core{r.h}).HandTask(entries[0].Ref, TaskRef{Title: "only-first", Path: "/tasks/first.md", ID: "first"}, "inspect", func(err error) { delivered <- err }); err != nil {
			t.Error(err)
		}
		return true
	})
	select {
	case err := <-delivered:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("accepted task did not finish delivery")
	}
	onLoop(r, func() bool { r.h.agents.useAgent(0); return true })
	r.s.WaitForText(t, "heard-Task: only-first")
	onLoop(r, func() bool { r.h.agents.useAgent(1); return true })
	r.s.WaitForText(t, "started second")
	if strings.Contains(r.s.String(), "heard-Task: only-first") {
		t.Fatal("task intended for first instance leaked to second")
	}
	if n := onLoop(r, func() string { return r.h.agents.agentRunning }); n != "two" {
		t.Fatalf("after Yes %q is running, want two", n)
	}

	// Hidden and shown again, the one running is shown, though another is the default; the View
	// menu's check follows the panel.
	shown := func() bool {
		return onLoop(r, func() bool { v, _ := r.h.p.Tree().Source("App.agentShown"); return v.Raw == "true" })
	}
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden, unchecked", func(sc string) bool { return !strings.Contains(sc, "agent · two") && !shown() })
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "two shown again, checked", func(sc string) bool { return strings.Contains(sc, "agent · two") && shown() })
	if n := onLoop(r, func() string { return r.h.agents.agentRunning }); n != "two" {
		t.Fatalf("shown again, %q is running, want two", n)
	}
}

// The agent's panel is kept where its grip leaves it, for the place it opens from: the top until
// the user chooses another.
func TestTheAgentsPanelSizeIsKept(t *testing.T) {
	d, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	onLoop(r, func() bool { r.h.panelResized("agent", 60, 70); return true })
	r.s.WaitFor(t, "kept", func(string) bool {
		got, _ := d.db.Preferences(context.Background())
		return got["tui.agent.top.size"] == "60" && got["tui.agent.top.length"] == "70"
	})
	again := attached(t, d)
	if g := onLoop(again, func() [2]int {
		s, l := again.h.prefs.panelGeometry("agent", "top")
		return [2]int{s, l}
	}); g != [2]int{60, 70} {
		t.Errorf("a new TUI sizes the agent %v, want the kept 60 by 70", g)
	}
	for _, tc := range []struct {
		place string
		want  [2]int
	}{{"top", [2]int{50, 100}}, {"left", [2]int{50, 100}}, {"center", [2]int{80, 80}}} {
		s, l := (prefs{}).panelGeometry("agent", tc.place)
		if g := [2]int{s, l}; g != tc.want {
			t.Errorf("before a drag the agent at %s is %v, want %v", tc.place, g, tc.want)
		}
	}
}

// The agent opens at the top by default; Preferences moves it, the choice is kept, and a size set
// at one place does not follow it to another.
func TestTheAgentOpensWherePreferencesSay(t *testing.T) {
	d, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	if e := onLoop(r, func() string { return r.h.prefs.agentEdge }); e != "top" {
		t.Fatalf("the agent opens at %q by default, want top", e)
	}
	onLoop(r, func() bool { r.h.agents.setAgentSize(nearest(termSizes, 70)); return true })
	onLoop(r, func() bool { r.h.agents.setAgentLength(nearest(termLengths, 60)); return true })
	onLoop(r, func() bool { r.h.agents.setAgentEdge(indexOf(agentEdges, "center")); return true })
	onLoop(r, func() bool {
		r.h.agents.setAgentEdge(len(agentEdges))
		r.h.agents.setAgentSize(-1)
		r.h.agents.setAgentLength(99)
		return true
	}) // out of range: ignored
	r.s.WaitFor(t, "kept", func(string) bool {
		got, _ := d.db.Preferences(context.Background())
		return got["tui.agent.edge"] == "center" && got["tui.agent.top.size"] == "70" && got["tui.agent.top.length"] == "60"
	})
	src := func(name string) string { v, _ := r.h.p.Tree().Source(name); return v.Raw }
	got := onLoop(r, func() [3]string {
		return [3]string{src("App.agentEdge"), src("App.agentSize"), src("App.agentEdgeIndex")}
	})
	if got != [3]string{"center", "80", "4"} {
		t.Errorf("after choosing the centre the document reads %v, want center at its own 80%%", got)
	}
	again := attached(t, d)
	if e := onLoop(again, func() string { return again.h.prefs.agentEdge }); e != "center" {
		t.Errorf("a new TUI opens the agent at %q, want the chosen center", e)
	}
}

// {root} and {workspace} are one shell word each, whatever they hold.
func TestAnAgentsCommandIsFilledInQuoted(t *testing.T) {
	ws := wsInfo{name: "it's", root: "/a b/$HOME"}
	got := fillAgentCommand("agent {root} --ws={workspace}", ws)
	if want := `agent '/a b/$HOME' --ws='it'\''s'`; got != want {
		t.Fatalf("filled %q, want %q", got, want)
	}
}

// A profile without a name or a command is dropped as the preference is read, and a preference
// that is not JSON reads as none.
func TestAgentProfilesReadOnlyTheWholeOnes(t *testing.T) {
	got := agentProfilesOf(`[{"name":"a","command":"x"},{"name":" ","command":"y"},{"name":"b","command":""},{"name":"c","command":"z"}]`)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("read %v, want a and c", got)
	}
	for _, bad := range []string{"not json", `[{"name":"a","command":"x"},5]`} {
		if got := agentProfilesOf(bad); got != nil {
			t.Fatalf("read %v from %s", got, bad)
		}
	}
	if agentProfilesJSON(nil) != "[]" {
		t.Fatalf("no profiles written as %s", agentProfilesJSON(nil))
	}
}

// With no config named, the agent's autodoc is the build alone, on the default config.
func TestAnAgentsAutodocWithoutAConfigIsTheBuildAlone(t *testing.T) {
	if got, want := agentWrapper("/opt/auto doc", ""), "exec '/opt/auto doc' \"$@\"\n"; !strings.HasSuffix(got, want) || strings.Contains(got, "--config") {
		t.Fatalf("the wrapper is\n%s\nwant it to end %q, with no --config", got, want)
	}
}

// SPC G opens Agent profiles, and the card names it beside SPC g, the agent.
func TestSpaceShiftGOpensAgentProfiles(t *testing.T) {
	_, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	r.keys(t, key(' '))
	r.s.WaitForText(t, "G  agent profiles")
	r.keys(t, esc())
	r.leader(t, 'G')
	r.s.WaitFor(t, "the dialog, not the card", func(sc string) bool {
		return !strings.Contains(sc, "SPC — commands") && strings.Contains(sc, "┌ agent profiles")
	})
}

// A clean exit (the user quits the agent) closes its panel; a failure leaves it, with what the
// agent printed (TestTheAgentStartsTheDefaultInItsOwnFolderToldAboutTheWorkspace exits with 3).
func TestAnAgentsCleanExitClosesItsPanel(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake")
	r.leader(t, 'g')
	r.s.WaitForText(t, "agent · fake")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	r.keys(t, decltest.Type("exit 0\r")...)
	r.s.WaitFor(t, "the panel closed", func(sc string) bool { return !strings.Contains(sc, "agent · fake") })
	r.s.WaitFor(t, "the exit in the notifications", func(string) bool { return r.noticed("fake exited:") })
	if open := onLoop(r, func() bool { return r.h.panelOpen["agent"] }); open {
		t.Fatal("the agent's panel is still open after a clean exit")
	}
}

// TestF11WorksWithTheKeyboardInARunningAgent: with the keyboard in a running agent, F11 maximizes
// the agent panel and restores it (its hostKeys leave F11 to the Window), and the agent never gets
// it: "exit 7" typed after starts its line, as the agent's own exit* match needs, so it exits 7 (F11's
// bytes before it would make the line not match and the agent run on).
func TestF11WorksWithTheKeyboardInARunningAgent(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake")
	r.leader(t, 'g')
	r.s.WaitForText(t, "started")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	maximized := func() bool { return onLoop(r, func() bool { return r.h.panelMax["agent"] }) }
	f11 := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF11}
	r.keys(t, f11)
	r.s.WaitFor(t, "F11 in the agent maximized it", maximizedNow(maximized, true))
	r.keys(t, f11)
	r.s.WaitFor(t, "F11 in the agent restored it", maximizedNow(maximized, false))
	if !r.focused("agentView") {
		t.Error("F11 took the keyboard out of the agent")
	}
	r.keys(t, decltest.Type("exit 7\r")...)
	r.s.WaitFor(t, "the agent exited 7", func(string) bool { return r.noticed("fake exited (7)") })
}
