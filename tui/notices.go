package tui

import (
	"fmt"
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// NOTIFICATION HISTORY — every notification kept, newest first (Go › Notifications…, SPC h), as a
// feature of its own. The toasts that show a notification as it comes are the host's (notify.go):
// it hands each one it keeps to keep.

// notices is the history of notifications.
type notices struct {
	c     Core
	items []notice // newest first, at most maxNotices
	rows  *rows[notice]
	shown bool // the dialog shows: a notification kept now shows at once
}

func newNotices(c Core) *notices {
	return &notices{c: c, rows: newRows(tuidecl.NewListModel("key", "when", "text"), noticeRowOf)}
}

func (*notices) Name() string { return "notices" }

func (n *notices) State() map[string]any {
	return map[string]any{"App.notices": n.rows.Model(), "App.noticesTitle": "notifications (0)"}
}

func (n *notices) Commands() map[string]Command {
	return map[string]Command{
		"App.openNotices":   none(n.open),
		"App.noticesClosed": none(func() { n.shown = false }),
		"App.clearNotices":  none(n.clear),
	}
}

// keep adds a notification to the history, newest first.
func (n *notices) keep(msg string) {
	n.items = append([]notice{{at: time.Now(), text: msg}}, n.items...)
	if len(n.items) > maxNotices {
		n.items = n.items[:maxNotices]
	}
	if n.shown {
		n.show()
	}
}

// open is Go › Notifications…: every notification kept, newest first.
func (n *notices) open() {
	n.shown = true
	n.show()
	n.c.Open("notifications")
}

// clear empties the history.
func (n *notices) clear() {
	n.items = nil
	n.show()
}

// show puts the history in the dialog's list. It runs while the dialog shows, not on every
// notification: the list holds up to maxNotices rows.
func (n *notices) show() {
	n.rows.Reset(n.items)
	n.c.Set("App.noticesTitle", fmt.Sprintf("notifications (%d)", len(n.items)))
}

// noticeRowOf is a notification as the history's list shows it.
func noticeRowOf(i int, m notice) rowOf {
	return rowOf{"key": fmt.Sprint(i), "when": m.at.Format("15:04:05"), "text": m.text}
}
