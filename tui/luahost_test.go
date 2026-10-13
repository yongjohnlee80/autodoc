//go:build linux || darwin

package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

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

// autodoc.agents lists the agents and sends one text as a paste, typed and not submitted; agents.lua's
// :agent-buffer sends the page to the one running, and :agent-forward the selection.
func TestScriptsSendTextToAnAgent(t *testing.T) {
	agent, _ := fakeAgent(t)
	_, r, _ := runAgentTUI(t, t.TempDir(), []agentProfile{{Name: "heron", Command: shellQuote(agent), Codename: "heron"}}, "heron")
	onLoop(r, func() bool { r.h.agents.toggleAgent(); return true })
	r.s.WaitFor(t, "the agent running", func(string) bool { return r.agentRunningNow() })
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
	// a paste's newline is Enter where the program did not ask for bracketed paste: the first line is
	// heard; the last is typed and left for the user to submit
	r.s.WaitForText(t, "heard-first-line")
	if strings.Contains(r.s.String(), "heard-second-line") {
		t.Error("the paste's last line was submitted")
	}
	r.s.WaitFor(t, "an unknown agent refused", func(string) bool { return r.noticed("nobody unavailable") })

	onLoop(r, func() bool { r.h.core.SetValue("page-text-for-the-agent"); return true })
	if err := r.luaRun("agent-buffer"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "page-text-for-the-agent") // typed at the agent's prompt, not submitted
	r.s.WaitFor(t, "said", func(string) bool { return r.noticed("sent the page to heron") })

	onLoop(r, func() bool { r.h.core.SetValue("ignored selected-part ignored"); return true })
	onLoop(r, func() bool {
		r.h.core.Select(widget.TextRange{From: widget.TextPos{Col: 8}, To: widget.TextPos{Col: 21}})
		return true
	})
	if err := r.luaRun("agent-forward"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "selected-part")
	if strings.Contains(r.s.String(), "ignored selected-part") {
		t.Error("more than the selection was sent")
	}
}

// SendText goes through HandTask's delivery: a running agent gets one paste, empty text is refused,
// and a stopped one is asked about, naming what will be sent.
func TestSendTextSharesHandTasksDelivery(t *testing.T) {
	a, c := modelAgents(t)
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
	if len(got) != 1 || got[0] != nil || !slices.Equal(c.panel.pastes, []string{string(first.tab) + ":line one\nline two"}) || len(c.panel.sends) != 0 {
		t.Fatalf("a running agent: done %v, pastes %q, sends %q", got, c.panel.pastes, c.panel.sends)
	}
	if err := a.SendText(AgentRef{Codename: "two"}, "for two", func(err error) { got = append(got, err) }); err != nil {
		t.Fatal(err)
	}
	if len(c.questions) != 1 || !strings.Contains(c.questions[0].Text, "send this text") {
		t.Fatalf("a stopped agent: questions %+v", c.questions)
	}
	c.questions[0].OnYes()
	if len(got) != 2 || got[1] != nil || len(c.panel.pastes) != 2 || !strings.HasSuffix(c.panel.pastes[1], ":for two") {
		t.Fatalf("started and sent: done %v, pastes %q", got, c.panel.pastes)
	}
}
