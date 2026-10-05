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

// agent_test.go: the agent — SPC ~ starts the default profile's command in AutoDoc's own folder for
// the workspace, told about it; hidden, it keeps running; the profiles are added, edited, made the
// default and removed in Options › Agent profiles…, and Use switches, asking first.

// fakeAgent is an agent CLI that writes where it started, with what, and what it was told to
// report (the screen wraps long paths), then echoes what it is sent until told to exit.
func fakeAgent(t *testing.T) (agent, report string) {
	t.Helper()
	dir := t.TempDir()
	p, report := filepath.Join(dir, "fake-agent"), filepath.Join(dir, "report")
	script := `#!/bin/sh
{
echo "cwd=$(pwd)"
echo "args=$*"
echo "env=$AUTODOC_WORKSPACE|$AUTODOC_ROOT"
ls
} > ` + shellQuote(report) + `
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

// runAgentTUI runs the TUI with the profiles given, the default named def, its agent folder dir.
func runAgentTUI(t *testing.T, dir string, profiles []agentProfile, def string) (*daemon, *running) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	prefs := map[string]string{prefAgentProfiles: agentProfilesJSON(profiles)}
	if def != "" {
		prefs[prefAgentDefault] = def
	}
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: prefs})
	r := runTUI(t, NewSession(d.sock, nil), Options{AgentDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return d, r
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
	_, r := runAgentTUI(t, dir, []agentProfile{
		{Name: "other", Command: "false"},
		{Name: "fake", Command: agent + " --root {root} --ws {workspace}"},
	}, "fake")
	root := onLoop(r, func() string { ws, _ := r.h.activeWorkspaceInfo(); return ws.root })
	folder := filepath.Join(dir, "kb")

	r.leader(t, '~')
	r.s.WaitForText(t, "agent · fake")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	// In AutoDoc's folder for the workspace, never the workspace's, its guides beside it; {root}
	// and {workspace} filled in, one shell word each; the environment naming the workspace.
	r.s.WaitForText(t, "started --root")
	if b, err := os.ReadFile(report); err != nil {
		t.Fatal(err)
	} else if want := "cwd=" + folder + "\nargs=--root " + root + " --ws kb\nenv=kb|" + root +
		"\nAGENTS.md\nCLAUDE.md\n"; string(b) != want {
		t.Fatalf("the agent reported\n%s\nwant\n%s", b, want)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		b, err := os.ReadFile(filepath.Join(folder, name))
		if err != nil {
			t.Fatal(err)
		}
		if g := string(b); !strings.Contains(g, "name: `kb`") || !strings.Contains(g, "root: `"+root+"`") ||
			!strings.Contains(g, "# AutoDoc") {
			t.Errorf("%s does not tell the agent the workspace and how to call AutoDoc:\n%.600s", name, g)
		}
	}

	// It hears what is typed.
	r.keys(t, decltest.Type("hello\r")...)
	r.s.WaitForText(t, "heard-hello")

	// Hidden, it lives on: SPC ~ again shows the same agent, not a new one.
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden", func(sc string) bool { return !strings.Contains(sc, "agent · fake") })
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	if !r.agentTerm().Running() {
		t.Fatal("the agent stopped while hidden")
	}
	r.leader(t, '~')
	r.s.WaitForText(t, "heard-hello")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })

	// It exits: the notifications say so, and the next SPC ~ starts the default again.
	r.keys(t, decltest.Type("exit 3\r")...)
	r.s.WaitFor(t, "the exit in the notifications", func(string) bool { return r.noticed("fake exited (3)") })
	if n := onLoop(r, func() string { return r.h.agentRunning }); n != "" {
		t.Fatalf("after its exit %q is still running", n)
	}

	// The card lists it.
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "hidden", func(sc string) bool { return !strings.Contains(sc, "agent · fake") })
	r.keys(t, key(' '))
	r.s.WaitForText(t, "~  the agent")
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

// A profile whose program is not installed starts nothing, and says so.
func TestAnAgentNotInstalledIsRefusedBeforeAnythingStarts(t *testing.T) {
	dir := t.TempDir()
	_, r := runAgentTUI(t, dir, []agentProfile{{Name: "gone", Command: "autodoc-no-such-agent --x"}}, "gone")
	onLoop(r, func() bool { r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "the refusal", func(string) bool { return r.noticed("autodoc-no-such-agent is not installed") })
	if onLoop(r, func() bool { return r.h.panelOpen["agent"] || r.h.agentRunning != "" }) {
		t.Fatal("an agent not installed opened the panel")
	}
	if _, err := os.Stat(filepath.Join(dir, "kb")); !os.IsNotExist(err) {
		t.Fatalf("an agent not installed wrote its folder (%v)", err)
	}
}

// With no profile SPC ~ opens the profiles: the first added becomes the default, a name another has
// is refused, an edit renames the default with it, and a removal leaves none. All of it is kept.
func TestAgentProfilesAreAddedEditedAndRemoved(t *testing.T) {
	d, r := runAgentTUI(t, t.TempDir(), nil, "")
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
	r.s.WaitForText(t, "SPC ~ opens claude")
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
	r.s.WaitForText(t, "SPC ~ opens claude-opus")
	d.waitStored(t, "claude-opus", `[{"name":"claude-opus","command":"claude --model opus"},{"name":"codex","command":"codex"}]`)

	onLoop(r, func() bool { r.h.makeAgentDefault(1); return true })
	r.s.WaitForText(t, "SPC ~ opens codex")
	onLoop(r, func() bool { r.h.removeAgent(1); return true })
	r.s.WaitForText(t, "Make default marks the one SPC ~ opens")
	d.waitStored(t, "", `[{"name":"claude-opus","command":"claude --model opus"}]`)
	// Profiles but no default: SPC ~ asks for one.
	onLoop(r, func() bool { r.h.closeDialog("agentProfiles"); r.h.toggleAgent(); return true })
	r.s.WaitFor(t, "the profiles, to make one the default", func(sc string) bool {
		return strings.Contains(sc, "Make default marks the one SPC ~ opens") && !onLoop(r, func() bool { return r.h.panelOpen["agent"] })
	})
	again := attached(t, d)
	if n := onLoop(again, func() int { return len(again.h.prefs.agents) }); n != 1 {
		t.Errorf("a new TUI reads %d profiles, want the 1 kept", n)
	}
}

// Use starts another agent: with one running it asks first, and only Yes stops it.
func TestUsingAnotherAgentAsksBeforeStoppingTheOneRunning(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r := runAgentTUI(t, t.TempDir(), []agentProfile{
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

// The agent's panel is kept where its grip leaves it.
func TestTheAgentsPanelSizeIsKept(t *testing.T) {
	d, r := runAgentTUI(t, t.TempDir(), nil, "")
	onLoop(r, func() bool { r.h.panelResized("agent", 60, 70); return true })
	r.s.WaitFor(t, "kept", func(string) bool {
		got, _ := d.db.Preferences(context.Background())
		return got["tui.agent.center.size"] == "60" && got["tui.agent.center.length"] == "70"
	})
	again := attached(t, d)
	if g := onLoop(again, func() [2]int {
		s, l := again.h.prefs.sideGeometry("agent", agentEdge)
		return [2]int{s, l}
	}); g != [2]int{60, 70} {
		t.Errorf("a new TUI sizes the agent %v, want the kept 60 by 70", g)
	}
	if g := onLoop(r, func() [2]int {
		s, l := (prefs{}).sideGeometry("agent", agentEdge)
		return [2]int{s, l}
	}); g != [2]int{agentDefaultSize, agentDefaultLength} {
		t.Errorf("before a drag the agent is %v, want 80 by 80", g)
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
