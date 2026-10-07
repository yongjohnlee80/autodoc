//go:build linux || darwin

package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// agent_test.go: the agent — SPC g starts the default profile's command in AutoDoc's own folder for
// the workspace, told about it; hidden, it keeps running; the profiles are added, edited, made the
// default and removed in Options › Agent profiles…, and Use switches, asking first.

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
	onLoop(r, func() bool { r.h.agentBinPath = bin; return true })
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return d, r, calls
}

func (r *running) agentTerm() *widget.Terminal {
	return onLoop(r, func() *widget.Terminal {
		c, _ := r.h.p.Find("agentView")
		term, _ := c.(*widget.Terminal)
		return term
	})
}

func (r *running) noticed(text string) bool {
	return onLoop(r, func() bool {
		for _, n := range r.h.notices {
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
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden", func(sc string) bool { return !strings.Contains(sc, "agent · fake") })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	if !r.agentTerm().Running() {
		t.Fatal("the agent stopped while hidden")
	}
	r.leader(t, 'g')
	r.s.WaitForText(t, "heard-hello")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })

	// It exits: the notifications say so, and the next SPC g starts the default again.
	r.keys(t, decltest.Type("exit 3\r")...)
	r.s.WaitFor(t, "the exit in the notifications", func(string) bool { return r.noticed("fake exited (3)") })
	if n := onLoop(r, func() string { return r.h.agentRunning }); n != "" {
		t.Fatalf("after its exit %q is still running", n)
	}

	// The card lists it.
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden", func(sc string) bool { return !strings.Contains(sc, "agent · fake") })
	r.keys(t, key(' '))
	r.s.WaitForText(t, "g  the agent")
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
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "not found", func(string) bool {
		return r.noticed("the agent gone's command was not found (127): check it in Options › Agent profiles…")
	})
	if n := onLoop(r, func() string { return r.h.agentRunning }); n != "" {
		t.Fatalf("after its command was not found %q is running", n)
	}
}

// With no profile SPC g opens the profiles: the first added becomes the default, a name another has
// is refused, an edit renames the default with it, and a removal leaves none. All of it is kept.
func TestAgentProfilesAreAddedEditedAndRemoved(t *testing.T) {
	d, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitForText(t, "no profiles yet")

	onLoop(r, func() bool {
		r.h.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.saveAgent(" claude ", " claude --model opus ")
		return true
	})
	onLoop(r, func() bool {
		r.h.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.saveAgent("codex", "codex")
		return true
	})
	r.s.WaitForText(t, "SPC g opens claude")
	onLoop(r, func() bool {
		r.h.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.saveAgent("codex", "codex --other")
		return true
	})
	r.s.WaitForText(t, "not saved: another profile is named codex")
	onLoop(r, func() bool {
		r.h.startAddAgent()
		r.h.closeDialog("agentEdit")
		r.h.saveAgent("blank", "  ")
		return true
	})
	r.s.WaitForText(t, "not saved: a profile is a name and the command")

	onLoop(r, func() bool {
		r.h.startEditAgent(0)
		r.h.closeDialog("agentEdit")
		r.h.saveAgent("claude-opus", "claude --model opus")
		return true
	})
	r.s.WaitForText(t, "SPC g opens claude-opus")
	d.waitStored(t, "claude-opus", `[{"name":"claude-opus","command":"claude --model opus"},{"name":"codex","command":"codex"}]`)

	onLoop(r, func() bool { r.h.makeAgentDefault(1); return true })
	r.s.WaitForText(t, "SPC g opens codex")
	onLoop(r, func() bool { r.h.removeAgent(1); return true })
	r.s.WaitForText(t, "Make default marks the one SPC g opens")
	d.waitStored(t, "", `[{"name":"claude-opus","command":"claude --model opus"}]`)
	// Profiles but no default: SPC g asks for one.
	onLoop(r, func() bool { r.h.closeDialog("agentProfiles"); r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "the profiles, to make one the default", func(sc string) bool {
		return strings.Contains(sc, "Make default marks the one SPC g opens") && !onLoop(r, func() bool { return r.h.panelOpen["agent"] })
	})
	again := attached(t, d)
	if n := onLoop(again, func() int { return len(again.h.prefs.agents) }); n != 1 {
		t.Errorf("a new TUI reads %d profiles, want the 1 kept", n)
	}
}

// Use starts another agent: with one running it asks first, and only Yes stops it.
func TestUsingAnotherAgentAsksBeforeStoppingTheOneRunning(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{
		{Name: "one", Command: agent + " first"},
		{Name: "two", Command: agent + " second"},
	}, "one")
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitForText(t, "started first")
	// The profiles mark the default and the one running; Use on the one running only shows it.
	onLoop(r, func() bool { r.h.toggleAgent(); r.h.openAgentProfiles(); return true })
	r.s.WaitForText(t, "●▶   one")
	onLoop(r, func() bool { r.h.useAgent(0); return true })
	r.s.WaitFor(t, "the one running shown, unasked", func(sc string) bool {
		return strings.Contains(sc, "agent · one") && !strings.Contains(sc, "agent profiles") && r.focused("agentView")
	})
	onLoop(r, func() bool { r.h.openAgentProfiles(); r.h.useAgent(1); return true })
	r.s.WaitForText(t, "Stop one and start two?")
	if n := onLoop(r, func() string { return r.h.agentRunning }); n != "one" {
		t.Fatalf("asking, %q is running, want one still", n)
	}
	onLoop(r, func() bool { r.h.switchAgentConfirmed(); return true })
	r.s.WaitForText(t, "agent · two")
	r.s.WaitForText(t, "started second")
	if n := onLoop(r, func() string { return r.h.agentRunning }); n != "two" {
		t.Fatalf("after Yes %q is running, want two", n)
	}

	// Hidden and shown again, the one running is shown, though another is the default; the View
	// menu's check follows the panel.
	shown := func() bool {
		return onLoop(r, func() bool { v, _ := r.h.p.Tree().Source("App.agentShown"); return v.Raw == "true" })
	}
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden, unchecked", func(sc string) bool { return !strings.Contains(sc, "agent · two") && !shown() })
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "two shown again, checked", func(sc string) bool { return strings.Contains(sc, "agent · two") && shown() })
	if n := onLoop(r, func() string { return r.h.agentRunning }); n != "two" {
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
		s, l := again.h.prefs.agentGeometry("top")
		return [2]int{s, l}
	}); g != [2]int{60, 70} {
		t.Errorf("a new TUI sizes the agent %v, want the kept 60 by 70", g)
	}
	for _, tc := range []struct {
		place string
		want  [2]int
	}{{"top", [2]int{50, 100}}, {"left", [2]int{50, 100}}, {"center", [2]int{80, 80}}} {
		s, l := (prefs{}).agentGeometry(tc.place)
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
	onLoop(r, func() bool { r.h.setAgentSize(nearest(termSizes, 70)); return true })
	onLoop(r, func() bool { r.h.setAgentLength(nearest(termLengths, 60)); return true })
	onLoop(r, func() bool { r.h.setAgentEdge(indexOf(agentEdges, "center")); return true })
	onLoop(r, func() bool {
		r.h.setAgentEdge(len(agentEdges))
		r.h.setAgentSize(-1)
		r.h.setAgentLength(99)
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
