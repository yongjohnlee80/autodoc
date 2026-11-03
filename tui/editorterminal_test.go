package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// TestALineTheCardCantShowIsRefused: editor.terminal_run refuses, before any card, a line that is
// empty, past 20 lines or 4 KiB, or holds a control character (an escape could redraw what the card
// shows); tab and newline are allowed.
func TestALineTheCardCantShowIsRefused(t *testing.T) {
	for _, bad := range []string{"", "  \n ", strings.Repeat("echo x\n", 21), strings.Repeat("x", 4097),
		"echo \x1b[2Jhidden", "echo a\rb", "echo \x00"} {
		if err := terminalLine(bad); err == nil {
			t.Errorf("terminalLine(%q): accepted, want refused", bad)
		}
	}
	for _, ok := range []string{"sudo apt-get install -y docker.io", "echo a\techo b", "set -e\nsudo usermod -aG docker \"$USER\"\nid"} {
		if err := terminalLine(ok); err != nil {
			t.Errorf("terminalLine(%q): %v, want accepted", ok, err)
		}
	}
}

// TestAnAgentRunsALineOnlyAfterTheUserSaysRun: an agent's editor.terminal_run needs the terminal
// grant (refused, and the user asked, until then); granted, every call still shows the line on a
// card. Don't run refuses it and runs nothing; Run starts it in a new tab of its own, named for the
// agent, and answers {started, tab}; its exit code is told to the user when it failed, and the tab
// stays for the user to read.
func TestAnAgentRunsALineOnlyAfterTheUserSaysRun(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PS1", "$ ")
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: t.TempDir()})
	r.connected(t)
	path := onLoop(r, r.h.editorSocketPath)
	onLoop(r, func() []string {
		return r.h.agents.agentEnv(wsInfo{name: "kb"}, t.TempDir(), agentProfile{Codename: "zen"})
	})
	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, path, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	token := onLoop(r, func() string { return r.h.agents.editorToken("zen") })
	type answer struct {
		res map[string]any
		re  *golibrpc.Error
	}
	run := func(line string) chan answer {
		ch := make(chan answer, 1)
		go func() {
			res, err := cli.Call(ctx, "editor.terminal_run", token, line)
			var re *golibrpc.Error
			errors.As(err, &re)
			m, _ := res.(map[string]any)
			ch <- answer{m, re}
		}()
		return ch
	}
	wait := func(what string, ch chan answer) answer {
		t.Helper()
		select {
		case a := <-ch:
			return a
		case <-time.After(20 * time.Second):
			t.Fatalf("%s: never answered", what)
		}
		return answer{}
	}
	tabs := func() []string {
		return onLoop(r, func() []string {
			panel, err := r.h.terminals.panel()
			if err != nil {
				return nil
			}
			var out []string
			for _, tab := range panel.Tabs() {
				out = append(out, tab.Label)
			}
			return out
		})
	}

	if a := wait("an ungranted run", run("echo never")); a.re == nil || a.re.Code != codeEditorNotGranted || !strings.Contains(a.re.Message, "needs terminal") {
		t.Fatalf("an ungranted run: %v %v, want needs terminal", a.res, a.re)
	}
	r.s.WaitForText(t, "let the Lua script agent:zen use terminal?")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.asker.closed(); return true })
	r.s.WaitFor(t, "the terminal grant kept", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.luaGrants["agent:zen"]["terminal"] })
	})

	// granted, a line still waits for its card: Don't run runs nothing
	ch := run("echo no-$((40+1))")
	r.s.WaitForText(t, "let the agent zen run this in AutoDoc's terminal?")
	r.s.WaitForText(t, "echo no-$((40+1))")
	onLoop(r, func() bool { r.h.asker.rejected(); r.h.asker.closed(); return true })
	if a := wait("a line not run", ch); a.re == nil || a.re.Code != codeEditorRefused || !strings.Contains(a.re.Message, "did not run") {
		t.Fatalf("Don't run: %v %v, want refused", a.res, a.re)
	}
	if got := tabs(); len(got) != 0 {
		t.Fatalf("after Don't run the terminal has tabs %v, want none", got)
	}

	// Run: a tab of its own, named for the agent, that runs exactly the line the card showed
	ch = run("echo ran-$((40+2)); exit 3")
	r.s.WaitForText(t, "let the agent zen run this in AutoDoc's terminal?")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.asker.closed(); return true })
	a := wait("a line run", ch)
	if a.re != nil || a.res["started"] != true || a.res["tab"] != "zen: run" || a.res["exit_by_mail"] != false {
		t.Fatalf("Run: %v %v, want started in tab zen: run, no crew to mail its exit", a.res, a.re)
	}
	r.s.WaitForText(t, "ran-42")
	r.s.WaitFor(t, "its exit told to the user", func(string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.noticeLog.items {
				if strings.Contains(n.text, "The command the agent zen ran in the terminal exited 3") {
					return true
				}
			}
			return false
		})
	})
	if got := tabs(); len(got) != 1 || got[0] != "zen: run" {
		t.Fatalf("after its command exited, the terminal has tabs %v, want its tab kept", got)
	}

	// a second one gets a tab of its own
	ch = run("true")
	r.s.WaitForText(t, "let the agent zen run this in AutoDoc's terminal?")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.asker.closed(); return true })
	if a := wait("a second line", ch); a.re != nil || a.res["tab"] != "zen: run 2" {
		t.Fatalf("a second run: %v %v, want tab zen: run 2", a.res, a.re)
	}
}

// TestTheExitMailSaysTheCodeAndNothingOfTheTab: the mail an agent gets when its command ends says
// the code and the line, and that the tab's output is the user's, never what was typed there.
func TestTheExitMailSaysTheCodeAndNothingOfTheTab(t *testing.T) {
	s, b := terminalExitBody("sudo apt-get install -y ollama", 0)
	if !strings.Contains(s, "Command done") || !strings.Contains(b, "exited 0") || !strings.Contains(b, "sudo apt-get install -y ollama") {
		t.Errorf("success: %q / %q", s, b)
	}
	s, b = terminalExitBody("sudo usermod -aG docker me\nid", 1)
	if !strings.Contains(s, "Command failed (1): sudo usermod -aG docker me") || !strings.Contains(b, "exited 1") ||
		!strings.Contains(b, "don't read") {
		t.Errorf("failure: %q / %q", s, b)
	}
}
