package tui

import (
	"path"
	"slices"
	"strconv"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE BUFFER TABS — the open buffers on the GUI editor's title bar (golib's Editor.Tabs, ADR
// 1792076688 §4.7): a tab for each buffer, in opening order, the shown one active. A press shows a
// buffer; + makes a new, empty one. A tab's menu (≡, a right press) has New buffer, Open file in a
// new buffer… and Go to › every buffer, then Save, Close and Close others for the tab it was opened
// for, each asking about unsaved changes as the TUI's do. A tab is capped at bufferTabWidth cells;
// tabs wider than the bar's share scroll between < and > (golib's TabStrip). The TUI has no title
// bar: its editor has no strip (bufferStrip is nil there), and :b and the Buffers picker switch
// instead.

// bufferTabWidth is a buffer tab's widest, in cells, its padding included: a long name is cut to
// an ellipsis there, and its menu's Go to › has it whole.
const bufferTabWidth = 24

// syncBufferTabs feeds the editor's strip the buffers as they are now, wiring it the first time
// it is seen (a new editor, after a reload, is a new strip).
func (h *Host) syncBufferTabs() {
	strip := h.bufferStrip()
	if strip == nil {
		return
	}
	if strip != h.tabStrip {
		h.tabStrip = strip
		strip.SetOnSelect(func(id string) {
			if b := h.bufferOfTab(id); b != nil {
				h.showBuffer(b)
			}
		})
		strip.SetMenu(h.bufferTabRows)
		strip.SetActions(h.newEmptyBuffer, nil)
		strip.SetMaxTabWidth(bufferTabWidth)
	}
	tabs := make([]widget.StripTab, len(h.buffers))
	for i, b := range h.buffers {
		tabs[i] = widget.StripTab{ID: strconv.Itoa(b.n), Label: h.bufferTabLabel(b)}
	}
	strip.SetTabs(tabs)
	if h.active != nil {
		strip.SetActive(strconv.Itoa(h.active.n))
	}
}

// bufferTabLabel is b's tab, told apart from every other buffer's: its file's name; its path where
// another buffer's file has that name; its workspace and path where another's has that path (in
// another workspace); untitled, with its number while there is more than one draft. [+] while
// modified.
func (h *Host) bufferTabLabel(b *openBuffer) string {
	label := h.bufferTabName(b)
	if b.file.dirty {
		label += " [+]"
	}
	return label
}

func (h *Host) bufferTabName(b *openBuffer) string {
	other := func(match func(o *openBuffer) bool) bool {
		return slices.ContainsFunc(h.buffers, func(o *openBuffer) bool { return o != b && match(o) })
	}
	if !b.file.open {
		if other(func(o *openBuffer) bool { return !o.file.open }) {
			return untitled + " " + strconv.Itoa(b.n)
		}
		return untitled
	}
	base := path.Base(b.file.path)
	if !other(func(o *openBuffer) bool { return o.file.open && path.Base(o.file.path) == base }) {
		return base
	}
	if !other(func(o *openBuffer) bool { return o.file.open && o.file.path == b.file.path }) || b.file.ws == "" {
		return b.file.path
	}
	return b.file.ws + ": " + b.file.path
}

// bufferOfTab is the buffer a tab's id names (its number), or nil.
func (h *Host) bufferOfTab(id string) *openBuffer {
	n, err := strconv.Atoi(id)
	if err != nil {
		return nil
	}
	return h.bufferNumbered(n)
}

// showBuffer shows b, if it is still open, and gives the editor the keys.
func (h *Host) showBuffer(b *openBuffer) {
	if !slices.Contains(h.buffers, b) {
		return
	}
	h.activate(b)
	h.keep(h.p.Call("editor", "forceActiveFocus"))
}

// newEmptyBuffer is the tabs' +: a new, empty buffer, shown, for the user to type in, name, or
// open a file into (File › Open fills an empty, clean buffer rather than making another).
func (h *Host) newEmptyBuffer() {
	h.showBuffer(h.newBuffer())
}

// bufferTabRows is the menu of the tab id ("" off a tab, with none active): New buffer, Open file
// in a new buffer… and Go to › every buffer; then, for its buffer, Save (while it is modified),
// Close and Close others.
func (h *Host) bufferTabRows(id string) []widget.MenuItemModel {
	goTo := make([]widget.MenuItemModel, 0, len(h.buffers))
	for _, o := range h.buffers {
		target := o
		goTo = append(goTo, bufferTabRow("buffer.tabs.goto."+strconv.Itoa(o.n), bufferLabel(o), func() { h.showBuffer(target) }))
	}
	sub := widget.NewSubmenu("buffer.tabs.goto", "Go to", goTo)
	sub.Enabled = len(goTo) > 0
	rows := []widget.MenuItemModel{
		bufferTabRow("buffer.tabs.new", "&New buffer", h.newEmptyBuffer),
		// File › Open: a file opens in a buffer of its own (an empty, clean one shown is filled instead)
		bufferTabRow("buffer.tabs.open", "&Open file in a new buffer…", h.openPicker),
		sub,
	}
	b := h.bufferOfTab(id)
	if b == nil {
		return rows
	}
	save := bufferTabRow("buffer.tabs.save", "&Save", func() { h.saveBuffer(b) })
	save.Enabled = b.file.dirty
	others := bufferTabRow("buffer.tabs.closeothers", "Close &others", func() { h.closeOtherBuffers(b) })
	others.Enabled = len(h.buffers) > 1
	return append(rows, widget.NewSeparator("buffer.tabs.sep"), save,
		bufferTabRow("buffer.tabs.close", "&Close", func() { h.closeBuffer(b) }), others)
}

// bufferTabRow is a row of a buffer tab's menu: run when chosen. An & in label marks its key; never
// h, j, k or l, which move in the menu.
func bufferTabRow(id, label string, run func()) widget.MenuItemModel {
	text, key, at := tuicore.ParseMnemonic(label)
	row := widget.NewCommand(widget.ItemID(id), text, widget.TabStripMenuAction{ID: tuicore.ActionID(id), Run: func(string) { run() }})
	row.Hotkey, row.HotkeyIdx = key, at
	return row
}

// saveBuffer is a tab's Save: b shown, then saved as Ctrl+S saves it (a draft is named first).
func (h *Host) saveBuffer(b *openBuffer) {
	if !slices.Contains(h.buffers, b) {
		return
	}
	h.activate(b)
	h.save()
}

// closeOtherBuffers is a tab's Close others: b shown, and every other buffer closed, each modified
// one asked about first, in turn; Stay on any keeps them all.
func (h *Host) closeOtherBuffers(b *openBuffer) {
	if !slices.Contains(h.buffers, b) {
		return
	}
	h.activate(b)
	others := slices.DeleteFunc(slices.Clone(h.buffers), func(o *openBuffer) bool { return o == b })
	h.guardEach(others, "close it", func() {
		for _, o := range others {
			if slices.Contains(h.buffers, o) {
				h.dropBuffer(o)
			}
		}
		h.activate(b)
	})
}
