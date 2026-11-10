package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// On screen, against a daemon serving the inbox verbs: :inbox opens the dialog on the user's mail,
// its title, columns and rows drawn; the message under the cursor shows below the list; opening a
// report marks it read and the row says so.
func TestTheInboxShowsTheMailOnScreen(t *testing.T) {
	f := &fakeInbox{items: []any{mailItem(2, "message", "open"), mailItem(1, "request", "open")}, acks: make(chan string, 4)}
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{modules: []module.Module{f}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	onLoop(r, func() bool { r.h.runEntry("crew.inbox"); return true })
	r.s.WaitFor(t, "the inbox drawn", func(sc string) bool {
		return strings.Contains(sc, "Inbox — All") && strings.Contains(sc, "SUBJECT") && strings.Contains(sc, "subject m002") &&
			strings.Contains(sc, "subject m001") && strings.Contains(sc, "request")
	})
	if err := onLoop(r, func() error {
		return r.h.reg.Handlers["App.inboxShown"]([]qml.SpecValue{{Kind: feature.ArgNum, Raw: "0"}})
	}); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "body of m002")
	if err := onLoop(r, func() error {
		return r.h.reg.Handlers["App.inboxOpen"]([]qml.SpecValue{{Kind: feature.ArgNum, Raw: "0"}})
	}); err != nil {
		t.Fatal(err)
	}
	if got := <-f.acks; got != "m002" {
		t.Fatalf("opened the report: acked %q, want m002", got)
	}
	r.s.WaitForText(t, "subject m002 — from gold") // the message read whole
	r.keys(t, decltest.Rune('q'))                  // back to the list
	r.s.WaitFor(t, "the report listed read", func(sc string) bool {
		for _, line := range strings.Split(sc, "\n") {
			if strings.Contains(line, "subject m002") {
				return strings.Contains(line, "read") && !strings.Contains(line, "●")
			}
		}
		return false
	})
}

// inboxTUI is a window on a daemon serving f, its Inbox open on screen with the first message listed.
func inboxTUI(t *testing.T, f *fakeInbox, first string) *running {
	t.Helper()
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{modules: []module.Module{f}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	onLoop(r, func() bool { r.h.runEntry("crew.inbox"); return true })
	r.s.WaitFor(t, "the inbox drawn", func(sc string) bool {
		return strings.Contains(sc, "Inbox — All") && strings.Contains(sc, "subject "+first)
	})
	return r
}

// TestEveryInboxLetterWorksFromTheList: the Inbox opens with the list holding the keyboard, and
// every tab's and button's marked letter works from it: u, t, v and a switch the tab; d asks to
// dismiss the request under the cursor, r opens its reply, s shows its sender; m marks the report
// under the cursor read; o reads the message whole in its own dialog (q back to the list) and marks
// it read; e reads the page below.
func TestEveryInboxLetterWorksFromTheList(t *testing.T) {
	var items []any
	items = append(items, mailItem(60, "request", "open"), mailItem(59, "message", "open"), mailItem(58, "message", "open"))
	for seq := int64(57); seq > 0; seq-- {
		items = append(items, mailItem(seq, "message", "read"))
	}
	f := &fakeInbox{items: items, acks: make(chan string, 8)}
	r := inboxTUI(t, f, "m060")
	if !r.focused(inboxListID) {
		t.Fatal("the Inbox opened with the keyboard elsewhere than its list")
	}
	rec := &inboxReplyRec{}
	onLoop(r, func() bool { r.h.inboxView.replies = rec; return true })
	press := func(ch rune) {
		t.Helper()
		if err := onLoop(r, func() error { return r.h.p.Call(inboxListID, "forceActiveFocus") }); err != nil {
			t.Fatal(err)
		}
		r.keys(t, decltest.Rune(ch))
	}
	for _, c := range []struct {
		key rune
		tab int
	}{{'u', inboxUnread}, {'t', inboxRequests}, {'v', inboxUndeliverable}, {'a', inboxAll}} {
		press(c.key)
		r.s.WaitFor(t, fmt.Sprintf("%c switching to tab %d", c.key, c.tab), func(string) bool {
			return onLoop(r, func() int { return r.h.inboxView.tabOf }) == c.tab
		})
	}
	r.s.WaitFor(t, "All listed again", func(sc string) bool { return strings.Contains(sc, "subject m060") })
	press('d') // the request, under the cursor
	r.s.WaitFor(t, "d asking to dismiss", func(string) bool {
		return onLoop(r, func() bool {
			return r.h.asker.Pending && r.h.asker.Current.Title == r.h.tr("autodoc.question.inbox.dismiss")
		})
	})
	r.keys(t, decltest.Rune('n')) // No, as the user answers it
	r.s.WaitFor(t, "the question answered No", func(string) bool { return !onLoop(r, func() bool { return r.h.asker.Pending }) })
	press('r')
	press('s')
	r.s.WaitFor(t, "r opening the request's reply and s showing its sender", func(string) bool {
		return onLoop(r, func() string { return fmt.Sprint(rec.replied, rec.raised) }) == "[m060] [s-gold/gold]"
	})
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown})
	press('m')
	if got := <-f.acks; got != "m059" {
		t.Fatalf("m on the report: acked %q, want m059", got)
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown})
	press('o')
	r.s.WaitForText(t, "subject m058 — from gold")
	if got := <-f.acks; got != "m058" {
		t.Fatalf("o on the report: acked %q, want m058", got)
	}
	r.keys(t, decltest.Rune('q'))
	r.s.WaitFor(t, "back on the list", func(string) bool { return r.focused(inboxListID) })
	press('e')
	r.s.WaitFor(t, "e reading the page below", func(string) bool { return f.olderPages() == 1 })
}

// TestTheInboxCursorStaysOnItsMessage: the cursor stays on the message it is on whatever re-reads the
// list: a Mark read, More, new mail arriving above it, and the message read and closed again (the
// mutant: the rows reset, the cursor at the top).
func TestTheInboxCursorStaysOnItsMessage(t *testing.T) {
	var items []any
	for seq := int64(60); seq > 0; seq-- {
		items = append(items, mailItem(seq, "message", "open"))
	}
	f := &fakeInbox{items: items, acks: make(chan string, 8)}
	r := inboxTUI(t, f, "m060")
	down := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}
	r.keys(t, down, down)
	on := func(when string) {
		t.Helper()
		r.s.WaitFor(t, when+": the cursor on m058", func(sc string) bool {
			return strings.Contains(sc, "body of m058") && onLoop(r, func() string { return r.h.inboxView.key }) == "m058"
		})
	}
	on("moved")
	r.keys(t, decltest.Rune('m'))
	<-f.acks
	on("after Mark read")
	r.keys(t, decltest.Rune('e'))
	r.s.WaitFor(t, "the page below read", func(string) bool { return f.olderPages() == 1 })
	on("after More")
	f.setItems(append([]any{mailItem(62, "message", "open"), mailItem(61, "message", "open")}, items...)...)
	onLoop(r, func() bool { r.h.inboxView.changed(); return true })
	r.s.WaitForText(t, "subject m062")
	on("after new mail")
	r.keys(t, decltest.Rune('o'))
	r.s.WaitForText(t, "subject m058 — from gold")
	r.keys(t, decltest.Rune('q'))
	on("after reading it")
}

// TestGoInboxOpensTheInbox: the menu bar's Go › Inbox (Alt+g, then x) opens the Inbox.
func TestGoInboxOpensTheInbox(t *testing.T) {
	f := &fakeInbox{items: []any{mailItem(1, "message", "open")}, acks: make(chan string, 1)}
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{modules: []module.Module{f}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	r.keys(t, decltest.Alt('g'))
	r.s.WaitForText(t, "Inbox…")
	r.keys(t, decltest.Rune('x'))
	r.s.WaitFor(t, "the Inbox open", func(sc string) bool {
		return strings.Contains(sc, "Inbox — All") && strings.Contains(sc, "subject m001")
	})
}

// TestTheInboxListAndItsMessageAreSetApart: on an 80-column screen, the list full, an empty row
// sets the list apart from the message under the cursor: between the last message listed (its
// unread mark) and the message's first row, a row with nothing on it. The list and that row share
// the list's half of the dialog, so the message keeps its rows: as many as the list's half (its
// header, its rows, the empty row), less the one a tie gives the list. 80×20 and 80×22 draw a
// dialog of 17 rows and one of 18, either parity of what the two halves share. Each screen is
// logged.
func TestTheInboxListAndItsMessageAreSetApart(t *testing.T) {
	for _, height := range []int{20, 22} {
		t.Run(fmt.Sprintf("80x%d", height), func(t *testing.T) {
			var items []any
			for seq := int64(40); seq > 0; seq-- {
				items = append(items, mailItem(seq, "message", "open"))
			}
			f := &fakeInbox{items: items, acks: make(chan string, 4)}
			d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{modules: []module.Module{f}})
			r := runTUISized(t, NewSession(d.sock, nil), Options{}, 80, height)
			r.connected(t)
			onLoop(r, func() bool { r.h.runEntry("crew.inbox"); return true })
			r.s.WaitForText(t, "Inbox — All")
			if err := onLoop(r, func() error {
				return r.h.reg.Handlers["App.inboxShown"]([]qml.SpecValue{{Kind: feature.ArgNum, Raw: "0"}})
			}); err != nil {
				t.Fatal(err)
			}
			const head = "**From** gold@autodoc:w"
			r.s.WaitFor(t, "the message and the status drawn", func(sc string) bool {
				return strings.Contains(sc, head) && strings.Contains(sc, "40 listed")
			})
			lines := strings.Split(r.s.String(), "\n")
			t.Logf("the screen:\n%s", strings.Join(lines, "\n"))
			header, at, status := -1, -1, -1
			for i, l := range lines {
				switch {
				case header < 0 && strings.Contains(l, "SUBJECT"):
					header = i
				case at < 0 && strings.Contains(l, head):
					at = i
				case at >= 0 && status < 0 && strings.Contains(l, "40 listed"):
					status = i
				}
			}
			if header < 0 || at < header || status < at {
				t.Fatalf("the list's header at row %d, the message at %d, the status at %d: want them in that order", header, at, status)
			}
			last := -1 // the last message listed above the message's first row
			for i := at - 1; i > header && last < 0; i-- {
				if strings.Contains(lines[i], "●") {
					last = i
				}
			}
			if last < 0 {
				t.Fatal("no message listed above the message under the cursor")
			}
			if last >= at-1 {
				t.Fatalf("the list runs into the message: row %d lists %q, and the message starts at row %d", last, lines[last], at)
			}
			for i := last + 1; i < at; i++ {
				if strings.Trim(lines[i], " │┃║|") != "" {
					t.Errorf("row %d between the list and the message: %q, want it empty", i, lines[i])
				}
			}
			if list, message := at-header, status-at; message < list-1 {
				t.Errorf("the message has %d rows and the list's half %d: the empty row came out of the message's", message, list)
			}
		})
	}
}
