package crew

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTheLayoutIDsNameTheLayoutsElements: every id the crew mail reaches the host's layout by names
// an element the layout has. A dialog's is an instance main.qml declares (Reply { id: crewReply });
// an id inside one (own@instance, as golib names it) is one the instance's own file declares, in
// tui/qml's dialogs. A rename on either side, or one that reaches into the id's string, would
// leave the crew mail calling an element that isn't there: once, the reply's text area was never
// set, so the dialog kept another request's answer.
func TestTheLayoutIDsNameTheLayoutsElements(t *testing.T) {
	main, err := os.ReadFile(filepath.Join("..", "qml", "main.qml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{ReplyDialog, ReplyTextID, inboxDialog, inboxMessageDialog, InboxListID, GrantsListID} {
		own, instance, inside := strings.Cut(id, "@")
		if !inside {
			instance = own
		}
		m := regexp.MustCompile(`(?m)^\s*(\w+) \{ id: ` + regexp.QuoteMeta(instance) + ` \}`).FindSubmatch(main)
		if m == nil {
			t.Errorf("%q: main.qml declares no instance %q", id, instance)
			continue
		}
		if !inside {
			continue
		}
		file := filepath.Join("..", "qml", "dialogs", string(m[1])+".qml")
		b, err := os.ReadFile(file)
		if err != nil {
			t.Errorf("%q: %s's file: %v", id, m[1], err)
			continue
		}
		if !regexp.MustCompile(`\bid: ` + regexp.QuoteMeta(own) + `\b`).Match(b) {
			t.Errorf("%q: %s declares no id %q", id, file, own)
		}
	}
}
