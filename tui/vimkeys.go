package tui

import (
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/style"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
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
	// the text wears the card's colours: a Text with none clears its cells to the screen's default
	// background, a dark block on the card in a themed window
	look := style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)
	card := widgetcore.NewBox(widgetcore.NewText(vimKeysText, widgetcore.WithWrapMode(widgetcore.Wrap), widgetcore.WithTextStyle(look)),
		widgetcore.WithTitle("Vim keys · Normal mode · ? closes"), widgetcore.WithBorder(style.BorderRounded),
		widgetcore.WithStyle(look.Padding(0, 1)))
	col := tuicore.NewFlex(tuicore.Vertical)
	col.Add(card, widgetcore.NewText("")) // the empty row keeps the status line under the card
	h.vimKeys = widgetcore.NewFloat(col, widgetcore.WithAnchor(widgetcore.BottomRight))
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
