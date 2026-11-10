//go:build linux || darwin

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

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

// Desktop drops are ordinary pastes from the GUI backend. The currently focused
// agent terminal must receive the quoted paths unchanged, without a focus switch.
func TestTheFocusedAgentReceivesDroppedPathsAsAnOrdinaryPaste(t *testing.T) {
	t.Parallel()
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "fake", Command: shellQuote(agent)}}, "fake")
	r.leader(t, 'g')
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	r.s.WaitForText(t, "started")
	paths := "'/tmp/screen shot.png' /tmp/second.png"
	r.keys(t, tuicore.PasteEvent{Text: paths})
	if !r.focused("agentView") {
		t.Fatal("pasting dropped paths changed keyboard focus")
	}
	r.keys(t, decltest.Type("\r")...)
	r.s.WaitForText(t, "heard-"+paths)
}

// {root} and {workspace} are one shell word each, whatever they hold.
func TestAnAgentsCommandIsFilledInQuoted(t *testing.T) {
	t.Parallel()
	ws := feature.Workspace{Name: "it's", Root: "/a b/$HOME"}
	got, err := fillAgentCommand(nil, "agent {root} --ws={workspace}", ws)
	if want := `agent '/a b/$HOME' --ws='it'\''s'`; err != nil || got != want {
		t.Fatalf("filled %q, %v; want %q", got, err, want)
	}
}

// A profile without a name or a command is dropped as the preference is read, and a preference
// that is not JSON reads as none.
func TestAgentProfilesReadOnlyTheWholeOnes(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	if got, want := agentWrapper("/opt/auto doc", ""), "exec '/opt/auto doc' \"$@\"\n"; !strings.HasSuffix(got, want) || strings.Contains(got, "--config") {
		t.Fatalf("the wrapper is\n%s\nwant it to end %q, with no --config", got, want)
	}
}

// SPC G opens Agent profiles, and the card names it beside SPC g, the agent.
func TestSpaceShiftGOpensAgentProfiles(t *testing.T) {
	t.Parallel()
	_, r, _ := runAgentTUI(t, t.TempDir(), nil, "")
	r.keys(t, key(' '))
	r.s.WaitForText(t, "G  agent profiles")
	r.keys(t, esc())
	r.leader(t, 'G')
	r.s.WaitFor(t, "the dialog, not the card", func(sc string) bool {
		return !strings.Contains(sc, "SPC — commands") && strings.Contains(sc, "┌ agent profiles")
	})
}

// Shift+Enter in the agents panel is a new line in the prompt, not a send. An agent CLI (Claude
// Code, Codex) asks for the kitty keyboard protocol at startup (CSI > 5 u), and then reads
// Shift+Enter as CSI 13;2u, apart from Enter's \r. The fake agent asks as they do, then reports
// the bytes the key sends it.
func TestShiftEnterReachesAnAgentApartFromEnter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	agent := filepath.Join(t.TempDir(), "kitty-agent")
	script := `#!/bin/sh
printf '\033[>5u'
echo ready
stty raw -echo
got=$(dd bs=1 count=7 2>/dev/null | od -An -tx1 | tr -d ' \n')
stty sane
echo "got-$got"
read line
`
	if err := os.WriteFile(agent, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, r, _ := runAgentTUI(t, dir, []agentProfile{{Name: "kitty", Command: shellQuote(agent)}}, "kitty")
	r.leader(t, 'g')
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	r.s.WaitForText(t, "ready")
	r.keys(t, tuicore.KeyEvent{Code: tuicore.KeyEnter, Mods: tuicore.ModShift})
	// ESC [ 1 3 ; 2 u: Shift+Enter in the protocol the agent asked for. A plain \r here (0d) is
	// the bug: the agent would have sent the prompt.
	r.s.WaitForText(t, "got-1b5b31333b3275")
}
