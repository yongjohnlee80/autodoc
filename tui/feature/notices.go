package feature

import (
	"fmt"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// NOTIFICATION HISTORY — every notification kept, newest first (Go › Notifications…, SPC h), as a
// feature of its own. The toasts that show a notification as it comes are the host's (tui/notify.go):
// it hands each one it keeps to keep.

// notices is the history of notifications.
type Notices struct {
	c     Core
	Items []Notice // newest first, at most maxNotices
	Rows  *Rows[Notice]
	Shown bool // the dialog shows: a notification kept now shows at once
}

func NewNotices(c Core) *Notices {
	return &Notices{c: c, Rows: NewRows(tuidecl.NewListModel("key", "when", "text"), noticeRowOf)}
}

func (*Notices) Name() string { return "notices" }

func (n *Notices) State() map[string]any {
	return map[string]any{"App.notices": n.Rows.Model(), "App.noticesTitle": n.c.Trf("autodoc.text.notices.notifications", 0)}
}

func (n *Notices) Commands() map[string]Command {
	return map[string]Command{
		"App.openNotices":   None(n.Open),
		"App.noticesClosed": None(func() { n.Shown = false }),
		"App.clearNotices":  None(n.Clear),
	}
}

// Catalog is the history's user command (catalog.go): it runs what App.openNotices runs.
func (n *Notices) Catalog() []Entry {
	return []Entry{Builtin("notices.open", "notifications", "App.openNotices()", n.Open)}
}

// keep adds a notification to the history, newest first.
func (n *Notices) Keep(msg string) {
	n.Items = append([]Notice{{at: time.Now(), Text: msg}}, n.Items...)
	if len(n.Items) > maxNotices {
		n.Items = n.Items[:maxNotices]
	}
	if n.Shown {
		n.Show()
	}
}

// open is Go › Notifications…: every notification kept, newest first.
func (n *Notices) Open() {
	n.Shown = true
	n.Show()
	n.c.Open("notifications")
}

// clear empties the history.
func (n *Notices) Clear() {
	n.Items = nil
	n.Show()
}

// LanguageChanged titles the history again in the UI's language.
func (n *Notices) LanguageChanged() {
	n.c.Set("App.noticesTitle", n.c.Trf("autodoc.text.notices.notifications", len(n.Items)))
}

// show puts the history in the dialog's list. It runs while the dialog shows, not on every
// notification: the list holds up to maxNotices rows.
func (n *Notices) Show() {
	n.Rows.Reset(n.Items)
	n.c.Set("App.noticesTitle", n.c.Trf("autodoc.text.notices.notifications", len(n.Items)))
}

// noticeRowOf is a notification as the history's list shows it.
func noticeRowOf(i int, m Notice) RowOf {
	return RowOf{"key": fmt.Sprint(i), "when": m.at.Format("15:04:05"), "text": m.Text}
}

// maxNotices bounds the history.
const maxNotices = 500

type Notice struct {
	at   time.Time
	Text string
}
