package tui

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// TestANewRequestDoesNotKeepAnotherRequestsAnswer (Lector's #100 r0 F1 repro, on the real dialog):
// an answer typed for request A, then cancelled, is not in the text area when request B opens.
func TestANewRequestDoesNotKeepAnotherRequestsAnswer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"note.md", "# Note\n"}})
	r := attached(t, d)
	onLoop(r, func() bool {
		x := r.h.crewInbox
		x.endGen()
		x.gen++
		x.requests = map[string]crewRequest{
			"a": {id: "a", from: "agent-a", subject: "question-A", body: "request body A"},
			"b": {id: "b", from: "agent-b", subject: "question-B", body: "request body B"},
		}
		x.openReply("a")
		return true
	})
	r.s.WaitForText(t, "agent-a asks: question-A")
	r.keys(t, decltest.Type("answer-only-for-A")...)
	r.s.WaitForText(t, "answer-only-for-A")
	onLoop(r, func() bool { r.h.closeDialog(crewReplyDialog); return true })
	r.s.WaitFor(t, "A closed", func(string) bool { return onLoop(r, func() bool { return r.h.crewInbox.replying == "" }) })
	onLoop(r, func() bool { r.h.crewInbox.openReply("b"); return true })
	var stale bool
	r.s.WaitFor(t, "B open", func(screen string) bool {
		if !strings.Contains(screen, "agent-b asks: question-B") {
			return false
		}
		stale = strings.Contains(screen, "answer-only-for-A")
		return true
	})
	if stale {
		t.Fatal("request B's answer still holds what was typed for request A")
	}
}
