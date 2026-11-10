package tui

import (
	"os"
	"slices"
	"strconv"
)

// TITLE BAR — tui.gui.titlebar, who draws the window's title bar: the system (its frame, title and
// buttons), or AutoDoc, a title bar of its own at the top of the window (golib's TitleBar) with the
// window's controls, its empty part the window's drag area. The system's is the default. The window
// is opened with or without the system's frame before the store is read, so the row mirrors its
// value into the state directory (Options.TitleBarFile, GUITitleBarFile), and the GUI build reads
// that as the next start opens its window (app/titlebar.go), then asks for the bar to be written into
// the layout (Options.TitleBar). A change applies from the next start. The terminal build ignores
// it: a terminal has no window to draw a title bar for.

// Who tui.gui.titlebar says draws the window's title bar.
const (
	TitleBarSystem = "system"
	TitleBarOwn    = "own"
)

var titleBars = []string{TitleBarSystem, TitleBarOwn}

// guiTitleBarName is the state directory's mirror of tui.gui.titlebar.
const guiTitleBarName = "gui-titlebar"

// GUITitleBarFile is where the state directory of the config at configPath mirrors
// tui.gui.titlebar, for the GUI build to read as it opens its window, before the store can be read;
// "" when the config cannot be read.
func GUITitleBarFile(configPath string) string {
	return stateFile(configPath, guiTitleBarName)
}

// titleBarMarker is the line in main.qml above which the window's own title bar is written: before
// the menu bar, so the window's dock pins it first, at the very top.
const titleBarMarker = "    // ---- the window's own title bar (the GUI's, tui.gui.titlebar) ----\n"

// titleBarQML is the window's own title bar: golib's TitleBar, pinned to the top. Its type is the
// GUI build's (titleBarTypes); nothing else writes it.
const titleBarQML = `    TitleBar {
        id: titleBar
        Dock.edge: Tui.Top
        title: "AutoDoc"
    }
`

// withTitleBar is src with the window's own title bar written in when own is set: the GUI, with
// tui.gui.titlebar on AutoDoc's own as the window opened. Otherwise src is as it is.
func withTitleBar(src []byte, own bool) []byte {
	if !own {
		return src
	}
	return insertAfter(src, titleBarMarker, titleBarQML)
}

// windowLayout is src with what the program running as a window writes into it: the GUI's relation
// views (relationmap.go), and its own title bar when the window opened without the system's.
func (h *Host) windowLayout(src []byte) []byte {
	return withTitleBar(withRelationViews(src, h.gui), h.gui && h.titleBar)
}

// mirrorTitleBar writes v to the state directory's mirror, as mirrorRenderer does: for the next
// start, and nothing waits on it. The tui.gui.titlebar row's State is its one writer.
func (h *Host) mirrorTitleBar(v string) {
	if h.titleBarFile == "" || v == h.titleBarMirrored {
		return
	}
	if os.WriteFile(h.titleBarFile, []byte(v+"\n"), 0o600) == nil {
		h.titleBarMirrored = v // only once written: a write that failed is tried again at the next
	}
}

// setTitleBar is Options › Title bar: the preference, which the window takes at the next start.
func (h *Host) setTitleBar(name string) {
	if !slices.Contains(titleBars, name) {
		h.notify(h.trf("autodoc.notice.titlebar.no_title_bar_s", strconv.Quote(name)))
		return
	}
	h.setPref(prefTitleBar, name, func(p *prefs) { p.titleBar = name })
	h.notify(h.tr("autodoc.notice.titlebar.from_the_next_start"))
}
