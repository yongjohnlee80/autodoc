package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/ipc"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// TestALineTheCardCantShowIsRefused: editor.terminal_run refuses, before any card, a line the card
// couldn't show whole and as itself: empty; past 5 lines, 60 cells a line or 1200 bytes; a
// control character (an escape redraws the card), a format character (a bidi override reorders
// what's shown, a zero-width one hides), a separator, a private-use code point or any space but the
// plain one, and a tab, which the card draws as nothing. Newline and printable text of any script
// are allowed.
func TestALineTheCardCantShowIsRefused(t *testing.T) {
	for _, bad := range []string{"", "  \n ", strings.Repeat("echo x\n", 5) + "echo x", strings.Repeat("x", 61), strings.Repeat("漢", 31),
		strings.Repeat(strings.Repeat("e\u0301\u0301", 60)+"\n", 5)[:1504], // 60 cells a line, 1504 bytes: past the byte backstop
		"echo e\u0301\u0301\u0301", "ls\u2800-la", "echo 1\u20e3", "echo a\u20ddb", "echo =\u0338",
		"echo \x1b[2Jhidden", "echo a\rb", "echo \x00",
		"rm -rf ~/\u202e/gnp.txt", "echo safe\u200b; rm -rf ~", "echo\u2066x\u2069", "echo a\u00a0b", "echo a\u2028b",
		"echo \ue000", "echo \ufffd", "printf\tfoo",
		"printf\u034f foo", "echo \u3164x", "echo a\ufe0fb", "echo \u115fx", "echo \U000e0100"} {
		if err := terminalLine(bad); err == nil {
			t.Errorf("terminalLine(%q): accepted, want refused", bad)
		}
	}
	for _, ok := range []string{"sudo apt-get install -y docker.io", "set -e\nsudo usermod -aG docker \"$USER\"\nid",
		"ls ~/문서 && echo 完了", "echo café naïve", "echo 한글", "echo e\u0301\u0302", "echo नमस्ते", strings.Repeat("x", 60), strings.Repeat("漢", 30), strings.Repeat(strings.Repeat("y", 59)+"\n", 5)[:299]} {
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
	disarm(t)
	tuneEnv(t, "SHELL=/bin/sh", "HOME="+t.TempDir(), "PS1=$ ")
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: t.TempDir()})
	r.connected(t)
	path := onLoop(r, r.h.editorSocketPath)
	onLoop(r, func() []string {
		return r.h.agents.agentEnv(wsInfo{name: "kb"}, t.TempDir(), agentProfile{Codename: "zen"})
	})
	ctx := context.Background()
	cli, err := golibrpc.Dial(ctx, path, msgpackrpc.New(nil), golibrpc.ClientNetwork(ipc.Network), golibrpc.WithConnDialer(ipc.RPCDialer))
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
	r.s.WaitForText(t, "let the agent zen use terminal?")
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
	r.s.WaitForText(t, "echo ran-$((40+2)); exit 3")
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
	ch = run("echo second-run")
	r.s.WaitForText(t, "echo second-run")
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

	// with the arm kept, through the verb: Run pressed twice runs the first line, and the second's
	// card, opened by that answer, is refused as answered too soon, said to the user, no tab started
	const arm = 400 * time.Millisecond
	onLoop(r, func() bool { r.h.seams.terminalCardArm = arm; return true }) // read as a card is asked, on the loop
	one := run("echo armed-one")
	r.s.WaitForText(t, "echo armed-one")
	two := run("echo armed-two")
	r.s.WaitFor(t, "the second armed card queued", func(string) bool {
		return onLoop(r, func() bool { return len(r.h.asker.waiting) == 1 })
	})
	time.Sleep(arm + 100*time.Millisecond) // the first card read
	armedBefore := len(tabs())
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.asker.accepted(); r.h.asker.closed(); return true })
	if a := wait("the first armed line", one); a.re != nil || a.res["started"] != true {
		t.Fatalf("the first armed line: %v %v, want started", a.res, a.re)
	}
	if a := wait("the second armed line", two); a.re == nil || a.re.Code != codeEditorRefused || !strings.Contains(a.re.Message, "before it had shown") {
		t.Fatalf("Run typed ahead onto the second card: %v %v, want refused as too soon", a.res, a.re)
	}
	if got := len(tabs()); got != armedBefore+1 {
		t.Fatalf("Run pressed twice started %d tabs, want 1", got-armedBefore)
	}
	r.s.WaitFor(t, "the too-soon notice", func(string) bool {
		return onLoop(r, func() bool {
			for _, n := range r.h.noticeLog.items {
				if strings.Contains(n.text, "was answered before you could read it") {
					return true
				}
			}
			return false
		})
	})
	onLoop(r, func() bool { r.h.seams.terminalCardArm = 0; return true })

	// a card another question displaces comes back after it, unanswered: nothing ran meanwhile, and
	// only its own Run runs it (here Don't run)
	before := len(tabs())
	ch = run("echo displaced-$((40+7))")
	r.s.WaitForText(t, "echo displaced-$((40+7))")
	onLoop(r, func() bool { r.h.ask(Question{Title: "another question", Yes: "&Yes", No: "&No"}); return true })
	r.s.WaitForText(t, "another question")
	select {
	case a := <-ch:
		t.Fatalf("a displaced card answered while the other question showed: %v %v", a.res, a.re)
	default:
	}
	onLoop(r, func() bool { r.h.asker.rejected(); r.h.asker.closed(); return true }) // the other question
	r.s.WaitForText(t, "echo displaced-$((40+7))")
	if got := len(tabs()); got != before {
		t.Fatalf("a displaced card's line ran before its Run: %d tabs, want %d", got, before)
	}
	onLoop(r, func() bool { r.h.asker.rejected(); r.h.asker.closed(); return true })
	if a := wait("a displaced card, then Don't run", ch); a.re == nil || a.re.Code != codeEditorRefused {
		t.Fatalf("a displaced card answered Don't run: %v %v, want refused", a.res, a.re)
	}
	if got := len(tabs()); got != before {
		t.Fatalf("Don't run on a displaced card ran its line: %d tabs, want %d", got, before)
	}
}

// TestTheExitMailSaysTheCodeAndNothingOfTheTab: the mail an agent gets when its command ends says
// the code and the line, and that the tab's output is the user's, never what was typed there.
func TestTheExitMailSaysTheCodeAndNothingOfTheTab(t *testing.T) {
	s, b := terminalExitBody("sudo apt-get install -y ollama", 0)
	if !strings.Contains(s, "Command done") || !strings.Contains(b, "exited 0") || !strings.Contains(b, "sudo apt-get install -y ollama") {
		t.Errorf("success: %q / %q", s, b)
	}
	long := "echo " + strings.Repeat("z", 56) // 61 characters: past the subject's 60
	if s, b := terminalExitBody(long, 0); len([]rune(s)) > 80 || !strings.HasSuffix(s, "…") || !strings.Contains(b, long) {
		t.Errorf("a long line's subject: %q, want it cut short (the body carries the whole line)", s)
	}
	s, b = terminalExitBody("sudo usermod -aG docker me\nid", 1)
	if !strings.Contains(s, "Command failed (1): sudo usermod -aG docker me") || !strings.Contains(b, "exited 1") ||
		!strings.Contains(b, "don't read") {
		t.Errorf("failure: %q / %q", s, b)
	}
}

// TestTheLongestLineShowsWholeOnASmallTerminal: the longest line editor.terminal_run accepts (5
// lines of 60 cells), in ASCII or in two-cell CJK, shows on the card whole, every line to its last
// character, with the longest folder the card names and both buttons, on the smallest window
// AutoDoc runs in (80x20), in every shipped language: what the user approves is what they could read.
func TestTheLongestLineShowsWholeOnASmallTerminal(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	h := newHost(NewSession(d.sock, nil), Options{seams: seamsFor(t)})
	s := decltest.RunWith(t, 80, 20, h.attach, h.options(Options{})...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	r.connected(t)
	longest := func(fill string, cells int) string {
		var lines []string
		for i := 1; i <= maxTerminalLines; i++ {
			head, tail := fmt.Sprintf("echo L%d-", i), fmt.Sprintf("-E%d", i)
			n := (maxTerminalLineCells - len(head) - len(tail)) / cells
			lines = append(lines, head+strings.Repeat(fill, n)+tail)
		}
		return strings.Join(lines, "\n")
	}
	folder := cutMiddle("/home/someone/Projects/"+strings.Repeat("deep/", 30)+"a-workspace", maxCardFolderRunes)
	for _, lang := range []string{"en", "es", "ja_JP", "ko_KR", "pt_BR", "zh_CN"} {
		for _, line := range []string{longest("q", 1), longest("漢", 2)} {
			if err := terminalLine(line); err != nil {
				t.Fatalf("the longest line is refused: %v\n%s", err, line)
			}
			onLoop(r, func() bool {
				r.h.setLanguage(lang)
				r.h.asker.askWhenFree(Question{
					Title: r.h.trf("autodoc.question.editorterminal.let_the_agent_run_this_in_the_terminal", "zen"),
					Text:  r.h.trf("autodoc.question.editorterminal.it_runs_in_a_new_tab_in", line, folder),
					Yes:   r.h.tr("autodoc.question.editorterminal.run"), No: r.h.tr("autodoc.question.editorterminal.dont_run"),
				})
				return true
			})
			yes := strings.ReplaceAll(onLoop(r, func() string { return r.h.tr("autodoc.question.editorterminal.run") }), "&", "")
			no := strings.ReplaceAll(onLoop(r, func() string { return r.h.tr("autodoc.question.editorterminal.dont_run") }), "&", "")
			yes, no = strings.SplitN(yes, "(", 2)[0], strings.SplitN(no, "(", 2)[0] // 実行(&r): the label before its key
			r.s.WaitFor(t, lang+": every line to its end, and both buttons", func(sc string) bool {
				for i := 1; i <= maxTerminalLines; i++ {
					if !strings.Contains(sc, fmt.Sprintf("-E%d", i)) {
						return false
					}
				}
				return strings.Contains(sc, yes) && strings.Contains(sc, no)
			})
			onLoop(r, func() bool { r.h.asker.rejected(); r.h.asker.closed(); return true })
		}
	}
}

// TestALongFolderIsCutInTheMiddle: the folder the card names is at most 48 characters, its start
// and its last folders kept, so it never pushes the line off the card.
func TestALongFolderIsCutInTheMiddle(t *testing.T) {
	long := "/home/someone/Projects/" + strings.Repeat("deep/", 30) + "a-workspace"
	got := cutMiddle(long, maxCardFolderRunes)
	if n := len([]rune(got)); n != maxCardFolderRunes || !strings.HasPrefix(got, "/home/some") || !strings.HasSuffix(got, "a-workspace") || !strings.Contains(got, "…") {
		t.Errorf("cutMiddle: %q (%d characters)", got, n)
	}
	if got := cutMiddle("/home/me/kb", maxCardFolderRunes); got != "/home/me/kb" {
		t.Errorf("a short folder: %q, want it as it is", got)
	}
}

// disarm makes a terminal card's Run count at once, for a cell that answers cards as soon as they
// show (TestARunTypedAheadNeverRunsTheNextLine keeps the arm).
func disarm(t *testing.T) {
	t.Helper()
	tune(t).terminalCardArm = 0
}

// TestARunTypedAheadNeverRunsTheNextLine: a terminal card queued behind another opens the moment
// that one is answered, Run in the same place on the same key; a Run that reaches it before it is
// armed (a key typed ahead, a double click) is its Don't run, said to the user, and its line never
// runs. A Run once armed runs as ever.
func TestARunTypedAheadNeverRunsTheNextLine(t *testing.T) {
	tuneEnv(t, "SHELL=/bin/sh", "HOME="+t.TempDir())
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{EditorDir: t.TempDir()})
	r.connected(t)
	arm := defaultSeams.terminalCardArm
	var outcomes []string
	ask := func(line string) {
		onLoop(r, func() bool {
			r.h.asker.askWhenFree(Question{Title: "run " + line, Text: line, Arm: arm,
				OnTooSoon: func() { outcomes = append(outcomes, line+": too soon") },
				OnYes:     func() { outcomes = append(outcomes, line+": run") },
				OnCancel:  func() { outcomes = append(outcomes, line+": not run") }})
			return true
		})
	}
	ask("first")
	ask("second")
	r.s.WaitForText(t, "run first")
	time.Sleep(arm + 100*time.Millisecond) // the first card read and armed
	// the user presses Run twice, the second time before the next card has been on screen
	onLoop(r, func() bool { r.h.asker.accepted(); r.h.asker.accepted(); r.h.asker.closed(); return true })
	got := onLoop(r, func() []string { return append([]string(nil), outcomes...) })
	if strings.Join(got, "; ") != "first: run; second: too soon; second: not run" {
		t.Fatalf("Run pressed twice: %q, want the first run and the second refused as too soon", got)
	}
	// armed, a Run counts
	ask("third")
	r.s.WaitForText(t, "run third")
	time.Sleep(arm + 100*time.Millisecond)
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	if got := onLoop(r, func() []string { return append([]string(nil), outcomes...) }); got[len(got)-1] != "third: run" {
		t.Fatalf("an armed Run: %q, want third run", got)
	}
}
