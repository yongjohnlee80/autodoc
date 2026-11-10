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
	crewOf(a).grantAnswered("one", []string{"editor.write"}, true)
	settleMessaging(c, w)
	crewOf(a).grantAnswered("one", []string{"editor.write", "commands"}, false)
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
