package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/mail"
)

// The user's answer to an agent's grant card reaches that agent as the user's own mail, through the
// crew (inbox.send, a message, rung): a yes says what it may now do and to call again, a no (or a
// card closed unanswered) says not to call again. Each answer is one mail to that agent alone.
func TestAGrantAnswerIsMailedToTheAgentThatAsked(t *testing.T) {
	a, c, w := messagingRig(t)
	var sends [][]any
	w.inboxSend = func(p []any) (any, error) { sends = append(sends, p); return map[string]any{"message": "m1"}, nil }
	a.crew.grantAnswered("one", []string{"editor.write"}, true)
	settleMessaging(c, w)
	a.crew.grantAnswered("one", []string{"editor.write", "commands"}, false)
	settleMessaging(c, w)
	if len(sends) != 2 {
		t.Fatalf("%d sends for two answers, want one each", len(sends))
	}
	for i, want := range []struct{ subject, says, not string }{
		{"Allowed: editor.write", "Call it again now", "Don't call it again"},
		{"Not allowed: editor.write, commands", "Don't call it again", "Call it again now"},
	} {
		p := sends[i]
		to, _ := p[3].([]any)
		body, _ := p[4].(string)
		kind, _ := p[6].(string)
		subject, _ := p[7].(string)
		if len(to) != 1 || to[0] != "one" || kind != mail.KindMessage || subject != want.subject {
			t.Errorf("answer %d: to %v, kind %q, subject %q; want one, %q, %q", i, to, kind, subject, mail.KindMessage, want.subject)
		}
		if !strings.Contains(body, want.says) || strings.Contains(body, want.not) {
			t.Errorf("answer %d's body: %q, want it to say %q and not %q", i, body, want.says, want.not)
		}
	}
}

// A window holding no crew scope mails nothing (there is no crew to carry it), and its agent's
// refusal never promises a mail (editorVerb, crewHeld).
func TestAGrantAnswerWithoutACrewScopeMailsNothing(t *testing.T) {
	h := &Host{}
	if _, _, ok := h.crewHeld(); ok {
		t.Fatal("a host with no crew host holds a scope")
	}
	h.grantAnswered("agent:one", []string{"editor.write"}, true) // must not panic, nor send
	h.grantAnswered("plugin:x", []string{"editor.write"}, true)  // a plugin hears nothing yet
}

// A retry of an agent's grant request while its card is open asks nothing more: the card, once
// answered no, closed or yes, gives that request its one answer, and no second card opens for it
// (luaShown). A request queued behind it is asked in its turn, and answered once too.
func TestARetryWhileItsGrantCardIsOpenGetsTheOneAnswer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "x\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	var answers []string
	onLoop(r, func() bool {
		r.h.onGrantAnswered = func(req string, caps []string, ok bool) {
			answers = append(answers, req+" "+strings.Join(caps, ",")+" "+map[bool]string{true: "yes", false: "no"}[ok])
		}
		return true
	})
	request := func(who string) {
		onLoop(r, func() bool { luaGrants{r.h}.Request(who, []string{"editor.write"}); return true })
	}
	settled := func(what string) {
		r.s.WaitFor(t, what, func(string) bool {
			return onLoop(r, func() bool { return !r.h.asker.Pending && !r.h.luaAsking && len(r.h.luaAsks) == 0 })
		})
	}
	for i, end := range []struct {
		name   string
		answer func()
		want   string
	}{
		{"answered no", func() { r.h.asker.Rejected(); r.h.asker.Closed() }, "agent:zen editor.write no"},
		{"closed", func() { r.h.asker.Closed() }, "agent:zen editor.write no"},
		{"answered yes", func() { r.h.asker.Accepted(); r.h.asker.Closed() }, "agent:zen editor.write yes"},
	} {
		request("agent:zen")
		r.s.WaitForText(t, "let the agent zen use editor.write?")
		request("agent:zen") // the agent retries while its card is open
		onLoop(r, func() bool { end.answer(); return true })
		settled(end.name)
		if len(answers) != i+1 || answers[i] != end.want {
			t.Fatalf("%s: answers %v, want one more, %q", end.name, answers, end.want)
		}
	}

	request("agent:one")
	r.s.WaitForText(t, "let the agent one use editor.write?")
	request("agent:two")
	request("agent:two") // queued, asked twice: one card in its turn
	onLoop(r, func() bool { r.h.asker.Rejected(); r.h.asker.Closed(); return true })
	r.s.WaitForText(t, "let the agent two use editor.write?")
	request("agent:two")
	onLoop(r, func() bool { r.h.asker.Rejected(); r.h.asker.Closed(); return true })
	settled("the queued request answered")
	if got := answers[3:]; len(got) != 2 || got[0] != "agent:one editor.write no" || got[1] != "agent:two editor.write no" {
		t.Fatalf("the open and the queued request: %v, want one answer each", got)
	}
}
