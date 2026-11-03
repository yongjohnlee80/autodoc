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

// TestALineTheCardCantShowIsRefused: editor.terminal_run refuses, before any card, a line the card
// couldn't show whole and as itself: empty; past 6 lines, 100 characters a line or 600 bytes; a
// control character (an escape redraws the card), a format character (a bidi override reorders
// what's shown, a zero-width one hides), a separator, a private-use code point or any space but the
// plain one. Tab, newline and printable text of any script are allowed.
func TestALineTheCardCantShowIsRefused(t *testing.T) {
	for _, bad := range []string{"", "  \n ", strings.Repeat("echo x\n", 6) + "echo x", strings.Repeat("x", 101),
		strings.Repeat(strings.Repeat("x", 99)+"\n", 6)[:601],
		"echo \x1b[2Jhidden", "echo a\rb", "echo \x00",
		"rm -rf ~/\u202e/gnp.txt", "echo safe\u200b; rm -rf ~", "echo\u2066x\u2069", "echo a\u00a0b", "echo a\u2028b",
		"echo \ue000", "echo \ufffd"} {
		if err := terminalLine(bad); err == nil {
			t.Errorf("terminalLine(%q): accepted, want refused", bad)
		}
	}
	for _, ok := range []string{"sudo apt-get install -y docker.io", "echo a\techo b", "set -e\nsudo usermod -aG docker \"$USER\"\nid",
		"ls ~/문서 && echo 完了", strings.Repeat("x", 100), strings.Repeat(strings.Repeat("y", 99)+"\n", 6)[:599]} {
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

	// two at once: the second's card waits behind the first's, never in its place
	first := run("echo first-$((40+5))")
	r.s.WaitForText(t, "echo first-$((40+5))")
	second := run("echo second-$((40+6))")
	r.s.WaitFor(t, "the second's card queued", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.asker.waiting) == 1 })
	})
	if sc := r.s.String(); strings.Contains(sc, "echo second-$((40+6))") {
		t.Fatalf("the second line's card replaced the first's:\n%s", sc)
	}
	onLoop(r, func() bool { r.h.asker.rejected(); r.h.asker.closed(); return true })
	if a := wait("the first of two", first); a.re == nil || a.re.Code != codeEditorRefused {
		t.Fatalf("the first of two, Don't run: %v %v, want refused", a.res, a.re)
	}
	r.s.WaitForText(t, "echo second-$((40+6))")
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.asker.closed(); return true })
	if a := wait("the second of two", second); a.re != nil || a.res["started"] != true {
		t.Fatalf("the second of two, Run: %v %v, want started", a.res, a.re)
	}

	// a card another question replaces is a Don't run: nothing runs
	ch = run("echo replaced-$((40+7))")
	r.s.WaitForText(t, "echo replaced-$((40+7))")
	onLoop(r, func() bool { r.h.ask(Question{Title: "another question", Yes: "&Yes", No: "&No"}); return true })
	if a := wait("a replaced card", ch); a.re == nil || a.re.Code != codeEditorRefused {
		t.Fatalf("a replaced card: %v %v, want refused, nothing run", a.res, a.re)
	}
	onLoop(r, func() bool { r.h.asker.closed(); return true })
	for _, label := range tabs() {
		if label == "zen: run 4" {
			t.Fatalf("a replaced card's line ran: tabs %v", tabs())
		}
	}
}

// TestTheExitMailSaysTheCodeAndNothingOfTheTab: the mail an agent gets when its command ends says
// the code and the line, and that the tab's output is the user's, never what was typed there.
func TestTheExitMailSaysTheCodeAndNothingOfTheTab(t *testing.T) {
	s, b := terminalExitBody("sudo apt-get install -y ollama", 0)
	if !strings.Contains(s, "Command done") || !strings.Contains(b, "exited 0") || !strings.Contains(b, "sudo apt-get install -y ollama") {
		t.Errorf("success: %q / %q", s, b)
	}
	long := "echo " + strings.Repeat("z", 95)
	if s, _ := terminalExitBody(long, 0); len([]rune(s)) > 80 || !strings.HasSuffix(s, "…") {
		t.Errorf("a long line's subject: %q, want it cut short (the body carries the whole line)", s)
	}
	s, b = terminalExitBody("sudo usermod -aG docker me\nid", 1)
	if !strings.Contains(s, "Command failed (1): sudo usermod -aG docker me") || !strings.Contains(b, "exited 1") ||
		!strings.Contains(b, "don't read") {
		t.Errorf("failure: %q / %q", s, b)
	}
}
