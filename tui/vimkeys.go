package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/style"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE VIM KEYS' CARD — ? in Normal mode: the Vim editor mode's keys, in a card at the bottom
// right, over the status line. It takes no keyboard: the page keeps it, so the card stays open
// while you work, and ? again closes it. A native layer (golib's Float), not a dialog: a dialog
// is centred and takes the keys.

// attachVimKeys builds the card, hidden, over the page.
func (h *Host) attachVimKeys() {
	host, ok := h.p.Overlay()
	if !ok || h.vimKeys != nil {
		return
	}
	card := widget.NewBox(widget.NewText(vimKeysText, widget.WithWrapMode(widget.Wrap)),
		widget.WithTitle("Vim keys · Normal mode · ? closes"), widget.WithBorder(style.BorderRounded),
		widget.WithStyle(style.New().Background(style.TokenPanel).Foreground(style.TokenForeground).Padding(0, 1)))
	col := tuicore.NewFlex(tuicore.Vertical)
	col.Add(card, widget.NewText("")) // the empty row keeps the status line under the card
	h.vimKeys = widget.NewFloat(col, widget.WithAnchor(widget.BottomRight))
	host.Attach(h.vimKeys)
}

// toggleVimKeys is ?: the card, or not.
func (h *Host) toggleVimKeys() {
	if h.vimKeys == nil {
		return
	}
	if h.vimKeys.Shown() {
		h.vimKeys.Hide()
	} else {
		h.vimKeys.Show()
	}
}
