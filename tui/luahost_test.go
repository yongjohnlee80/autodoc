//go:build linux || darwin

package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// luahost_test.go: the host functions scripts reach — the clipboard, a picker, AutoDoc's agents —
// and agents.lua's commands over them.

// Writing sets the system clipboard; a read waits for the clipboard's answer and gives it to the
// callback on the loop; a failed read is said by its name.
func TestScriptsReadAndWriteTheClipboard(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.luaLoad(t, `
autodoc.command{ name = "clip", run = function()
  local ok, err = autodoc.clipboard.write("written-by-lua")
  autodoc.notify("write " .. tostring(ok) .. " " .. tostring(err))
  autodoc.clipboard.read(function(text, err) autodoc.notify("read [" .. tostring(text) .. "] " .. tostring(err)) end)
end }`)
	if err := r.luaRun("clip"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the write", func(string) bool { return r.noticed("write true nil") })
	if got := string(r.s.Backend.Clipboard()); got != "written-by-lua" {
		t.Errorf("the clipboard holds %q", got)
	}
	if r.noticed("read [") {
		t.Fatal("the read answered before the clipboard did")
	}
	r.s.WaitFor(t, "the read held", func(string) bool { return r.s.Backend.AnswerClipboard("from-the-system", nil) })
	r.s.WaitFor(t, "the read answered", func(string) bool { return r.noticed("read [from-the-system] nil") })

	if err := r.luaRun("clip"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "a second read held", func(string) bool { return r.s.Backend.AnswerClipboard("", errors.New("the system said no")) })
	r.s.WaitFor(t, "a failed read named", func(string) bool { return r.noticed("read [nil] the system said no") })
}

// A script's picker lists its items, a filter narrows them, and choosing one gives the callback the
// item's value; Esc gives it "cancelled"; a second picker while one is open is "busy".
func TestScriptsPickFromAList(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.luaLoad(t, `
autodoc.command{ name = "pk", run = function()
  local function said(v, err) autodoc.notify("picked " .. tostring(v) .. " " .. tostring(err)) end
  autodoc.ui.pick({ title = "pick a bird", items = { "alpha-kestrel", { label = "beta-heron", detail = "a long-legged one", value = 42 } } }, said)
  autodoc.ui.pick({ items = { "x" } }, function(v, err) autodoc.notify("second " .. tostring(err)) end)
end }`)
	if err := r.luaRun("pk"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "pick a bird")
	r.s.WaitFor(t, "a second picker is busy", func(string) bool { return r.noticed("second busy") })
	r.s.WaitForText(t, "alpha-kestrel")
	r.keys(t, decltest.Type("heron")...)
	r.s.WaitFor(t, "the filter", func(sc string) bool { return !strings.Contains(sc, "alpha-kestrel") })
	r.s.WaitForText(t, "a long-legged one") // the item's detail, previewed
	r.keys(t, enter())
	r.s.WaitFor(t, "the value chosen", func(string) bool { return r.noticed("picked 42 nil") })

	if err := r.luaRun("pk"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "pick a bird")
	r.keys(t, esc())
	r.s.WaitFor(t, "cancelled", func(string) bool { return r.noticed("picked nil cancelled") })
}

// autodoc.agents lists the agents and sends one text as a bracketed paste, typed and not submitted;
// agents.lua's :agent-buffer sends the page to the one running, and :agent-forward the selection.
func TestScriptsSendTextToAnAgent(t *testing.T) {
	agent, report := recordingAgent(t, true)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	r.luaLoad(t, `
autodoc.command{ name = "ag", run = function()
  local all = autodoc.agents.list()
  autodoc.notify("agents " .. #all .. " " .. all[1].codename .. " running " .. tostring(all[1].running) .. " closing " .. tostring(all[1].closing))
  autodoc.agents.send("heron", "first-line\nsecond-line", function(ok, err) autodoc.notify("sent " .. tostring(ok) .. " " .. tostring(err)) end)
  autodoc.agents.send("nobody", "x", function(ok, err) autodoc.notify("nobody " .. tostring(err)) end)
end }`)
	if err := r.luaRun("ag"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the list", func(string) bool { return r.noticed("agents 1 heron running true closing false") })
	r.s.WaitFor(t, "sent", func(string) bool { return r.noticed("sent true nil") })
	// one paste, whole: its newline the text's, then the Enter that sends it (agentmessaging.go)
	first := "\x1b[200~first-line\nsecond-line\x1b[201~\r"
	r.s.WaitFor(t, "the paste heard", func(string) bool { return strings.HasSuffix(reported(report), first) })
	r.s.WaitFor(t, "an unknown agent refused", func(string) bool { return r.noticed("nobody unavailable") })

	// :agent-buffer asks the instruction, then sends a request: the untitled draft's text goes in it
	onLoop(r, func() bool { r.h.core.SetValue("page-text-for-the-agent"); return true })
	if err := r.luaRun("agent-buffer"); err != nil {
		t.Fatal(err)
	}
	r.instruct(t, "hand the page to heron", "summarise it")
	r.s.WaitFor(t, "said", func(string) bool { return r.noticed("sent the page to heron") })
	r.s.WaitFor(t, "the page's request pasted", func(string) bool {
		return strings.HasSuffix(reported(report), "\x1b[200~Page: the untitled draft (unsaved)\n"+
			"From: SPC a b (:agent-buffer), sent by the user\nInstruction: summarise it\n"+
			"File: the untitled draft (never saved)\nText:\npage-text-for-the-agent\x1b[201~\r")
	})

	// :agent-forward: the selection, with its lines, in a request
	onLoop(r, func() bool { r.h.core.SetValue("ignored selected-part ignored"); return true })
	onLoop(r, func() bool {
		r.h.core.Select(widget.TextRange{From: widget.TextPos{Col: 8}, To: widget.TextPos{Col: 21}})
		return true
	})
	if err := r.luaRun("agent-forward"); err != nil {
		t.Fatal(err)
	}
	r.instruct(t, "hand the selection to heron", "explain this")
	r.s.WaitFor(t, "the selection's request pasted", func(string) bool {
		return strings.HasSuffix(reported(report), "\x1b[200~Selection: the untitled draft, lines 1 to 1\n"+
			"From: SPC a f (:agent-forward), sent by the user\nInstruction: explain this\n"+
			"File: the untitled draft, lines 1 to 1\nText:\nselected-part\x1b[201~\r")
	})
	if got := reported(report); strings.Contains(got, "ignored") || strings.Count(got, "\r") != 3 {
		t.Errorf("more than the selection, or other than one Enter per send, was sent: %q", got)
	}
}

// instruct answers the instruction line agents.lua asks (autodoc.ui.input), titled title, with text.
func (r *running) instruct(t *testing.T, title, text string) {
	t.Helper()
	r.s.WaitForText(t, title)
	r.keys(t, decltest.Type(text)...)
	r.keys(t, enter())
}

// TestAPageIsHandedOverByItsPathNeverItsText (the 2026-11-01 report: SPC a b sent a 36 KB page as
// mail from the user, with no instruction): a saved page is handed over as its workspace, path and
// version, its text left out; a page with unsaved changes goes with its text; one over 8 KiB is
// refused, nothing asked; an empty instruction, or Esc, sends nothing (the mutants: the text sent
// for a saved page; no instruction asked; no bound; an empty instruction sent).
func TestAPageIsHandedOverByItsPathNeverItsText(t *testing.T) {
	agent, report := recordingAgent(t, true)
	_, r, _ := runAgentTUIIn(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron",
		nil, Options{}, map[string][]string{"kb": {"a.md", "a\n", "plan.md", "the-saved-page-text\n"}})
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	r.h.p.Post(func() { r.h.openPath("plan.md") })
	r.s.WaitFor(t, "plan.md open", func(string) bool { return onLoop(r, func() bool { return r.h.core.Value() == "the-saved-page-text\n" }) })

	if err := r.luaRun("agent-buffer"); err != nil {
		t.Fatal(err)
	}
	r.instruct(t, "hand the page to heron", "review it")
	r.s.WaitFor(t, "the saved page's request pasted", func(string) bool {
		got := reported(report)
		return strings.Contains(got, "\x1b[200~Page: plan.md in kb\nFrom: SPC a b (:agent-buffer), sent by the user\n"+
			"Instruction: review it\nWorkspace: kb\nFile: plan.md\nVersion: ") &&
			strings.HasSuffix(got, "Its text is not in this message: read the file in the workspace.\x1b[201~\r")
	})
	if got := reported(report); strings.Contains(got, "the-saved-page-text") {
		t.Fatalf("a saved page's text was sent: %q", got)
	}

	// unsaved changes: the page as it is now goes in, marked so
	onLoop(r, func() bool { r.h.core.SetValue("an-unsaved-edit"); return true })
	if err := r.luaRun("agent-buffer"); err != nil {
		t.Fatal(err)
	}
	r.instruct(t, "hand the page to heron", "check the edit")
	r.s.WaitFor(t, "the unsaved page's request pasted", func(string) bool {
		return strings.HasSuffix(reported(report), "File: plan.md (with unsaved changes: the text below is the page now)\nText:\nan-unsaved-edit\x1b[201~\r")
	})

	before := reported(report)
	// over 8 KiB unsaved: refused before anything is asked
	onLoop(r, func() bool { r.h.core.SetValue(strings.Repeat("x", 8*1024+1)); return true })
	if err := r.luaRun("agent-buffer"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the bound said", func(string) bool { return r.noticed("is over 8 KiB, too much to send as text") })
	if strings.Contains(r.s.String(), "hand the page to heron") {
		t.Error("an instruction was asked for a page over the bound")
	}

	// an empty instruction, then Esc: nothing sent
	onLoop(r, func() bool { r.h.core.SetValue("short"); return true })
	if err := r.luaRun("agent-buffer"); err != nil {
		t.Fatal(err)
	}
	r.instruct(t, "hand the page to heron", "   ")
	r.s.WaitFor(t, "the empty instruction refused", func(string) bool { return r.noticed("not sent: say what heron should do with the page") })
	if err := r.luaRun("agent-buffer"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "hand the page to heron")
	r.keys(t, esc())
	r.s.WaitFor(t, "the line closed", func(sc string) bool { return !strings.Contains(sc, "hand the page to heron") })
	if got := reported(report); got != before {
		t.Errorf("a refused, empty or cancelled hand-over sent something: %q", strings.TrimPrefix(got, before))
	}
}

// TestScriptsAskALineAndSendARequest: autodoc.ui.input asks one line and gives the callback the
// text; Esc gives it "cancelled"; a second line while one is open is "busy". autodoc.agents.send
// with {subject, body} sends a request (its subject heading the paste); a subject with a line
// break, or none, is the script's error, nothing sent.
func TestScriptsAskALineAndSendARequest(t *testing.T) {
	agent, report := recordingAgent(t, true)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	r.luaLoad(t, `
autodoc.command{ name = "ask", run = function()
  autodoc.ui.input({ title = "a bird", label = "which bird?", text = "kes" }, function(v, err) autodoc.notify("answered " .. tostring(v) .. " " .. tostring(err)) end)
  autodoc.ui.input({ title = "another" }, function(v, err) autodoc.notify("second " .. tostring(err)) end)
end }
autodoc.command{ name = "req", run = function()
  autodoc.agents.send("heron", { subject = "Task: count", body = "count the birds" }, function(ok, err) autodoc.notify("request " .. tostring(ok) .. " " .. tostring(err)) end)
end }`)
	if err := r.luaRun("ask"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "which bird?")
	r.s.WaitFor(t, "a second line is busy", func(string) bool { return r.noticed("second busy") })
	r.keys(t, decltest.Type("trel")...)
	r.keys(t, enter())
	r.s.WaitFor(t, "the line's text", func(string) bool { return r.noticed("answered kestrel nil") })
	if err := r.luaRun("ask"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "which bird?")
	r.keys(t, esc())
	r.s.WaitFor(t, "cancelled", func(string) bool { return r.noticed("answered nil cancelled") })

	if err := r.luaRun("req"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the request sent", func(string) bool { return r.noticed("request true nil") })
	r.s.WaitFor(t, "its paste, the subject first", func(string) bool {
		return strings.HasSuffix(reported(report), "\x1b[200~Task: count\ncount the birds\x1b[201~\r")
	})
	before := reported(report)
	for _, bad := range []string{`{ subject = "two\nlines", body = "b" }`, `{ body = "no subject" }`, `{ subject = "no body" }`} {
		if err := r.luaRun(`:lua autodoc.agents.send("heron", ` + bad + `)`); err == nil {
			t.Errorf("a bad request %s ran as ok", bad)
		}
	}
	if got := reported(report); got != before {
		t.Errorf("a bad request sent something: %q", strings.TrimPrefix(got, before))
	}
}

// TestScriptsSendALongRichTextThroughLua: the 2026-10-29 report's send, a ~2.5 KB brief with
// newlines, quotes, backticks, $, ~, → and —, run as a :lua command line (the line editor.run
// runs), arrives as one whole paste, as does 2,600 x's. A chunk that no longer parses (a JSON
// decoder's raw newline inside a Lua string) is the command's error, never ok; a send with no
// callback that fails is a notice.
func TestScriptsSendALongRichTextThroughLua(t *testing.T) {
	agent, report := recordingAgent(t, true)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	cb := `function(ok, err) autodoc.notify("%s " .. tostring(ok) .. " " .. tostring(err)) end`

	if err := r.luaRun(`:lua autodoc.agents.send("heron", string.rep("x", 2600), ` + fmt.Sprintf(cb, "long") + `)`); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the long send", func(string) bool { return r.noticed("long true nil") })
	r.s.WaitFor(t, "2,600 x's pasted", func(string) bool {
		return strings.HasSuffix(reported(report), "\x1b[200~"+strings.Repeat("x", 2600)+"\x1b[201~\r")
	})

	var b strings.Builder
	for b.Len() < 2500 {
		b.WriteString("Step → run `go test ./...` in ~/src with $GOFLAGS set — then say \"done\" \\ next.\n")
	}
	rich := b.String()
	lit := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(rich)
	if err := r.luaRun(`:lua autodoc.agents.send("heron", "` + lit + `", ` + fmt.Sprintf(cb, "rich") + `)`); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the rich send", func(string) bool { return r.noticed("rich true nil") })
	r.s.WaitFor(t, "the rich text pasted whole", func(string) bool {
		return strings.HasSuffix(reported(report), "\x1b[200~"+rich+"\x1b[201~\r")
	})

	// the escapes decoded once too often: a raw newline inside a Lua string no longer parses
	raw := strings.ReplaceAll(lit, `\n`, "\n")
	if err := r.luaRun(`:lua autodoc.agents.send("heron", "` + raw + `", ` + fmt.Sprintf(cb, "raw") + `)`); err == nil {
		t.Fatal("a chunk that does not parse ran as ok")
	}
	if r.noticed("raw ") {
		t.Fatal("a chunk that does not parse sent something")
	}

	if err := r.luaRun(`:lua autodoc.agents.send("nobody", "x")`); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "a failed send without a callback noticed", func(string) bool { return r.noticed("the send to nobody failed: unavailable") })
}

// autodoc.agents.send to a program that never asked for bracketed paste sends it nothing, never
// keys, and the script hears "unbracketed"
// (docs/decisions/todos.md#text-reaches-an-agent-only-as-a-bracketed-paste).
func TestScriptsSendNothingToAnUnbracketedAgent(t *testing.T) {
	agent, report := recordingAgent(t, false)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	r.luaLoad(t, `
autodoc.command{ name = "ag", run = function()
  autodoc.agents.send("heron", "never-keys", function(ok, err) autodoc.notify("sent " .. tostring(ok) .. " " .. tostring(err)) end)
end }`)
	if err := r.luaRun("ag"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the refusal heard", func(string) bool { return r.noticed("sent nil unbracketed") })
	r.s.WaitFor(t, "the agent's keyboard", func(string) bool { return r.focused("agentView") })
	r.keys(t, decltest.Type("k")...) // a key the user types still reaches it: the agent reads on
	r.s.WaitFor(t, "the key typed", func(string) bool { return strings.Contains(reported(report), "k") })
	if got := reported(report); strings.Contains(got, "never-keys") {
		t.Errorf("an unbracketed agent was sent the text: %q", got)
	}
}

// SendText goes through HandTask's delivery: a running agent gets one paste, empty text is refused,
// and a stopped one is asked about, naming what will be sent.
func TestSendTextSharesHandTasksDelivery(t *testing.T) {
	a, c := modelAgents(t)
	a.promptsReported = func(string, bool) bool { return true } // its prompts reported: the base sends with its Enter
	one, _ := a.profile("one")
	first, _ := a.startInstance(one)
	var got []error
	if err := a.SendText(AgentRef{Codename: "one"}, "", func(err error) { got = append(got, err) }); !errors.Is(err, ErrAgentText) {
		t.Fatalf("empty text: %v", err)
	}
	if err := a.SendText(AgentRef{Codename: "one", Instance: first.token}, "line one\nline two", func(err error) { got = append(got, err) }); err != nil {
		t.Fatal(err)
	}
	c.drainPosts()
	c.fireAfters() // the Enter, submitDelay after the paste, and then done
	if len(got) != 1 || got[0] != nil || !slices.Equal(c.panel.pastes, []string{string(first.tab) + ":[bracketed]line one\nline two"}) {
		t.Fatalf("a running agent: done %v, pastes %q", got, c.panel.pastes)
	}
	if err := a.SendText(AgentRef{Codename: "two"}, "for two", func(err error) { got = append(got, err) }); err != nil {
		t.Fatal(err)
	}
	if len(c.questions) != 1 || !strings.Contains(c.questions[0].Text, "send this text") {
		t.Fatalf("a stopped agent: questions %+v", c.questions)
	}
	c.questions[0].OnYes()
	c.drainPosts()
	c.fireAfters()
	if len(got) != 2 || got[1] != nil || len(c.panel.pastes) != 2 || !strings.HasSuffix(c.panel.pastes[1], ":[bracketed]for two") {
		t.Fatalf("started and sent: done %v, pastes %q", got, c.panel.pastes)
	}
}

// SPC a opens the agents' card, its keys listed; b hands the page to the agent running, f the
// selection, each with the instruction asked, and Backspace goes back to SPC's card.
func TestTheAgentsCardSendsThePage(t *testing.T) {
	agent, report := recordingAgent(t, true)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
	r.s.WaitForText(t, "ready")
	onLoop(r, func() bool {
		r.h.core.SetValue("card-sent-page")
		return r.h.p.Call("editor", "forceActiveFocus") == nil
	})
	r.s.WaitFor(t, "the page's keyboard", func(string) bool { return r.focused("editor") })
	r.leader(t, 'a')
	r.s.WaitForText(t, "SPC a — agents")
	for _, does := range []string{"send the page to an agent", "send the selection or the clipboard", "the agent panel", "agent profiles"} {
		if !strings.Contains(r.s.String(), does) {
			t.Errorf("the card does not list %q:\n%s", does, r.s.String())
		}
	}
	r.keys(t, key('b'))
	r.instruct(t, "hand the page to heron", "read it")
	r.s.WaitFor(t, "the page's request pasted", func(string) bool {
		return strings.Contains(reported(report), "Instruction: read it\nFile: the untitled draft (never saved)\nText:\ncard-sent-page\x1b[201~")
	})
	r.s.WaitFor(t, "said", func(string) bool { return r.noticed("sent the page to heron") })

	// f: the selection
	onLoop(r, func() bool { r.h.core.SetValue("ignored card-selected ignored"); return true })
	onLoop(r, func() bool {
		r.h.core.Select(widget.TextRange{From: widget.TextPos{Col: 8}, To: widget.TextPos{Col: 21}})
		return r.h.p.Call("editor", "forceActiveFocus") == nil
	})
	r.leader(t, 'a')
	r.s.WaitForText(t, "SPC a — agents")
	r.keys(t, key('f'))
	r.instruct(t, "hand the selection to heron", "use it")
	r.s.WaitFor(t, "the selection's request pasted", func(string) bool {
		return strings.Contains(reported(report), "Instruction: use it\nFile: the untitled draft, lines 1 to 1\nText:\ncard-selected\x1b[201~")
	})

	// g: the agent panel, hidden; G: the profiles
	shown := func() bool { return onLoop(r, func() bool { return r.h.panelOpen["agent"] }) }
	onLoop(r, func() bool { return r.h.p.Call("editor", "forceActiveFocus") == nil })
	r.leader(t, 'a')
	r.s.WaitForText(t, "SPC a — agents")
	r.keys(t, key('g'))
	r.s.WaitFor(t, "the panel hidden by g", func(string) bool { return !shown() })
	if !r.agentRunningNow() {
		t.Error("g stopped the agent")
	}
	r.leader(t, 'a')
	r.s.WaitForText(t, "SPC a — agents")
	r.keys(t, key('G'))
	r.s.WaitForText(t, "CODENAME") // the profiles dialog's column, on no card
	r.keys(t, esc())

	r.leader(t, 'a')
	r.s.WaitForText(t, "SPC a — agents")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyBackspace})
	r.s.WaitForText(t, "SPC — commands")
}
