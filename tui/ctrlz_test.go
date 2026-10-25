//go:build linux || darwin

package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// suspendingAgent is an agent CLI as Claude Code is: its tty raw, it reads a key at a time, stops
// itself on Ctrl+Z (0x1A) after saying so, stops on s as a suspend from elsewhere would and says it
// resumed once continued, and echoes any other key as got-<key>.
func suspendingAgent(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "suspending-agent")
	script := `#!/bin/sh
stty raw -echo
printf 'ready\r\n'
while :; do
  c=$(dd bs=1 count=1 2>/dev/null)
  case "$c" in
    "$(printf '\032')") printf 'suspending\r\n'; kill -STOP $$ ;;
    s) printf 'stopping\r\n'; kill -STOP $$; printf 'resumed\r\n' ;;
    q) exit 0 ;;
    *) printf 'got-%s\r\n' "$c" ;;
  esac
done
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// runSuspendingAgent runs the agents panel on suspendingAgent, its keyboard there.
func runSuspendingAgent(t *testing.T) *running {
	t.Helper()
	agent := suspendingAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	return r
}

// Ctrl+Z never reaches an agent's CLI (its hostKeys keep it): a CLI that would stop itself on it runs
// on, and the next key reaches it.
func TestCtrlZNeverReachesAnAgentsCLI(t *testing.T) {
	r := runSuspendingAgent(t)
	r.keys(t, decltest.Ctrl('z'))
	r.keys(t, decltest.Type("x")...)
	r.s.WaitForText(t, "got-x")
	if strings.Contains(r.s.String(), "suspending") {
		t.Fatalf("Ctrl+Z reached the agent's CLI:\n%s", r.s.String())
	}
}

// Ctrl+Z never reaches a shell's program either: cat, which the tty would stop on it, still prints
// the line typed after it, and no ^Z was sent.
func TestCtrlZNeverReachesAShellsProgram(t *testing.T) {
	_, r := runShellTUI(t, nil)
	catInTheTerminal(t, r)
	r.keys(t, decltest.Ctrl('z'))
	r.keys(t, decltest.Type("after-ctrl-z\r")...)
	r.s.WaitFor(t, "cat printed the line", catPrinted("after-ctrl-z"))
	if sc := r.s.String(); strings.Contains(sc, "^Z") || strings.Contains(sc, "Stopped") {
		t.Fatalf("Ctrl+Z reached the shell's program:\n%s", sc)
	}
}

// autodoc.agents.resume sends a stopped agent's CLI its SIGCONT and it runs again; a resume of one
// running disturbs nothing; an unknown agent is refused.
func TestAgentsResumeWakesAStoppedCLI(t *testing.T) {
	r := runSuspendingAgent(t)
	r.luaLoad(t, `
autodoc.command{ name = "wake", run = function(_, args)
  local ok, err = autodoc.agents.resume(args[1])
  autodoc.notify("resume " .. tostring(args[1]) .. " " .. tostring(ok) .. " " .. tostring(err))
end }`)
	r.keys(t, decltest.Type("s")...)
	r.s.WaitForText(t, "stopping")
	r.keys(t, decltest.Type("x")...) // typed while it is stopped: read once it runs again
	if err := r.luaRun("wake heron"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "resumed", func(string) bool { return r.noticed("resume heron true nil") })
	r.s.WaitForText(t, "resumed")
	r.s.WaitForText(t, "got-x")
	if err := r.luaRun("wake heron"); err != nil {
		t.Fatal(err)
	}
	resumed := func() int {
		return onLoop(r, func() int {
			n := 0
			for _, it := range r.h.noticeLog.items {
				if strings.Contains(it.text, "resume heron true nil") {
					n++
				}
			}
			return n
		})
	}
	r.s.WaitFor(t, "a running agent resumed", func(string) bool { return resumed() >= 2 })
	r.keys(t, decltest.Type("y")...)
	r.s.WaitForText(t, "got-y")
	if err := r.luaRun("wake nobody"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "an unknown agent refused", func(string) bool { return r.noticed("resume nobody nil unavailable") })
}

// panel.resume wakes the active tab of the panel in hand.
func TestPanelResumeWakesTheTabInHand(t *testing.T) {
	r := runSuspendingAgent(t)
	r.keys(t, decltest.Type("s")...)
	r.s.WaitForText(t, "stopping")
	onLoop(r, func() bool { r.h.resumePanel(); return true })
	r.s.WaitForText(t, "resumed")
}
