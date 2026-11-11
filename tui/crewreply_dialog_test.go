package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/tui/crew"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/parse/qml"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// TestANewRequestDoesNotKeepAnotherRequestsAnswer (on the real dialog):
// an answer typed for request A, then cancelled, is not in the text area when request B opens.
// Both are open in the daemon's feed, and each reply is opened from the Inbox, as its Reply does.
func TestANewRequestDoesNotKeepAnotherRequestsAnswer(t *testing.T) {
	request := func(seq int64, from, subject, body string) map[string]any {
		m := mailItem(seq, "request", "open")
		m["from"], m["subject"], m["body"] = from, subject, body
		return m
	}
	a := request(2, "agent-a", "question-A", "request body A")
	b := request(1, "agent-b", "question-B", "request body B")
	f := newWatchingInbox(&fakeInbox{items: []any{a, b}, acks: make(chan string, 4)}, a, b)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"note.md", "# Note\n"}}, daemonOpts{modules: []module.Module{f}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	r.s.WaitForText(t, "agent-b asks: question-B") // the toasts: the window's crew inbox holds both open
	onLoop(r, func() bool { r.h.runEntry("crew.inbox"); return true })
	r.s.WaitFor(t, "the inbox drawn", func(sc string) bool {
		return strings.Contains(sc, "Inbox — All") && strings.Contains(sc, "question-A") && strings.Contains(sc, "question-B")
	})
	reply := func(row string) {
		t.Helper()
		if err := onLoop(r, func() error {
			return r.h.reg.Handlers["App.inboxReply"]([]qml.SpecValue{{Kind: feature.ArgNum, Raw: row}})
		}); err != nil {
			t.Fatal(err)
		}
	}
	reply("0") // A, the newest
	r.s.WaitFor(t, "A open", func(sc string) bool {
		return replyOpen(sc, "agent-a asks: question-A") && strings.Contains(sc, "request body A")
	})
	r.keys(t, decltest.Type("answer-only-for-A")...)
	r.s.WaitForText(t, "answer-only-for-A")
	onLoop(r, func() bool { r.h.closeDialog(crew.ReplyDialog); return true })
	r.s.WaitFor(t, "A closed", func(sc string) bool {
		return !replyOpen(sc, "agent-a asks: question-A") && !strings.Contains(sc, "answer-only-for-A")
	})
	reply("1")
	var stale bool
	r.s.WaitFor(t, "B open", func(screen string) bool {
		if !replyOpen(screen, "agent-b asks: question-B") || !strings.Contains(screen, "request body B") {
			return false
		}
		stale = strings.Contains(screen, "answer-only-for-A")
		return true
	})
	if stale {
		t.Fatal("request B's answer still holds what was typed for request A")
	}
}
