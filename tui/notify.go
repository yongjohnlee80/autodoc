package tui

import (
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// NOTIFICATIONS — what the TUI tells: toasts stacked in a corner over the page (golib's Toasts),
// each saying how long ago it came, and the history of them (View › Notifications…, SPC h).
//
// Two kinds of message, by what they are about:
//
//   - A notification (notify) is what happened: a save, a workspace added, a provider refused, the
//     connection. It shows as a toast, and the history keeps it. A long task's progress (indexing,
//     embedding) is an ongoing toast, updated in place, that stays until the task ends.
//   - A message about the page or the view (say) is the status line's: a find's result, the editor
//     mode, a note opening. It replaces the one before it, and nothing keeps it.
//
// Up to three toasts show; the rest wait, and show in turn, saying how long ago they came. A
// finished toast stays toastSeconds (Preferences: 1 to 10, default 3), in the corner the
// preferences name (default the bottom right, over the status line).

// maxNotices bounds the history.
const maxNotices = 500

// The toasts' IDs for the ongoing ones, each updated in place.
const (
	toastProgress   = "progress"    // indexing and embedding
	toastConnection = "connection"  // connecting, and a connection that failed
	toastPlugin     = "plugin"      // a plugin cloned, fetched and built
	toastWarming    = "warming"     // the daemon starting up: a first scan, a provider being set up
	toastSemantic   = "semantic"    // model switch: lexical until the new model fills
	toastSearchWait = "search-wait" // an open search waiting on embedding work
)

type notice struct {
	at   time.Time
	text string
}

// attachToasts puts the toasts over the page, once the Window is mounted.
func (h *Host) attachToasts() {
	host, ok := h.p.Overlay()
	if !ok || h.toasts != nil {
		return
	}
	h.toasts = widget.NewToasts(widget.WithToastCorner(cornerAnchor[h.prefs.toastCorner]),
		widget.WithToastLinger(time.Duration(h.prefs.toastSeconds)*time.Second),
		widget.WithToastMargin(h.toastMargin()), widget.WithToastWidth(60))
	host.AttachTopmost(h.toasts.Float()) // above the dialogs, too
	for _, t := range h.early {
		h.toasts.Post(t)
	}
	h.early = nil
}

// toastMargin is the rows kept clear at the corner's edge: the status line's, at the bottom while it
// shows; the menu bar's, at the top while it shows.
func (h *Host) toastMargin() int {
	switch h.prefs.toastCorner {
	case "top-left", "top-right":
		if !h.prefs.menuHidden {
			return 1
		}
	default:
		if h.statusShown() {
			return 1
		}
	}
	return 0
}

// applyToastPrefs moves the toasts as the preferences say.
func (h *Host) applyToastPrefs() {
	if h.toasts == nil {
		return
	}
	h.toasts.SetCorner(cornerAnchor[h.prefs.toastCorner])
	h.toasts.SetLinger(time.Duration(h.prefs.toastSeconds) * time.Second)
	h.toasts.SetMargin(h.toastMargin())
}

// post shows a toast. A click on it opens the notifications' history (Johno: "toast click opens
// the notification history"), unless the toast says otherwise.
func (h *Host) post(t widget.Toast) {
	if t.OnClick == nil {
		t.OnClick = h.openNotices
	}
	if h.toasts == nil {
		h.early = append(h.early, t) // before the Window mounted: shown once it is
		return
	}
	h.toasts.Post(t)
}

// notify is a notification: a toast, kept in the history.
func (h *Host) notify(msg string) {
	h.post(widget.Toast{Text: msg})
	h.keepNotice(msg)
}

// notifyOngoing is a long task's toast, id's, updated in place; the history keeps it once it ends.
func (h *Host) notifyOngoing(id, msg string) {
	h.post(widget.Toast{ID: id, Text: msg, Ongoing: true})
}

// notifyDone ends id's ongoing toast with its last message, which the history keeps.
func (h *Host) notifyDone(id, msg string) {
	h.post(widget.Toast{ID: id, Text: msg})
	h.keepNotice(msg)
}

// keepNotice adds a notification to the history, newest first.
func (h *Host) keepNotice(msg string) {
	h.notices = append([]notice{{at: time.Now(), text: msg}}, h.notices...)
	if len(h.notices) > maxNotices {
		h.notices = h.notices[:maxNotices]
	}
	if h.historyOpen {
		h.showNotices()
	}
}

// openNotices is View › Notifications…: every notification kept, newest first.
func (h *Host) openNotices() {
	h.historyOpen = true
	h.showNotices()
	h.open("notifications")
}

func (h *Host) noticesClosed() { h.historyOpen = false }

func (h *Host) showNotices() {
	rows := make([]rowOf, len(h.notices))
	for i, n := range h.notices {
		rows[i] = rowOf{"key": fmt.Sprint(i), "when": n.at.Format("15:04:05"), "text": n.text}
	}
	h.noticeList.Reset(rows)
	h.set("App.noticesTitle", fmt.Sprintf("notifications (%d)", len(h.notices)))
}

// clearNotices empties the history.
func (h *Host) clearNotices() {
	h.notices = nil
	h.showNotices()
}

// say is a message about the page or the view, on the status line's right until the next.
func (h *Host) say(msg string) {
	h.set("App.status", msg)
}

// cornerAnchor is where each corner preference puts the toasts.
var cornerAnchor = map[string]widget.Anchor{
	"bottom-right": widget.BottomRight, "bottom-left": widget.BottomLeft,
	"top-right": widget.TopRight, "top-left": widget.TopLeft,
}
