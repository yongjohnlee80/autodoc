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
// buffer, and a press on its × closes it (asking first over unsaved changes); + makes a new, empty
// one. A tab's menu (≡, a right press) has New buffer, Open file in a
// new buffer… and Go to › every buffer, then Save, Close and Close others for the tab it was opened
// for, each asking about unsaved changes as the TUI's do. A tab is never wider than bufferTabWidth
// cells: a longer name is cut in its middle (bufferTabLabel), and a tab never changes width as tabs
// open and close or the window resizes (Johno: consistent, not re-fitted each time). The strip has
// the bar's room up to Raw | Rendered; tabs that do not fit scroll between ← and →. The TUI has no title
// bar: its editor has no strip (bufferStrip is nil there), and :b and the Buffers picker switch
// instead.

// bufferTabWidth is a buffer tab's widest, in cells, its padding included: a longer name is cut in
// its middle there, the same however many tabs are open; its menu's Go to › has the name whole.
const bufferTabWidth = 32

// syncBufferTabs feeds the editor's strip the buffers as they are now.
func (h *Host) syncBufferTabs() { h.feedBufferTabs(h.bufferStrip()) }

// feedBufferTabs gives strip a tab per buffer and the shown one active, wiring it the first time
// it is seen (a new editor, after a reload, is a new strip); nil, the TUI's editor's, is left.
func (h *Host) feedBufferTabs(strip *widget.TabStrip) {
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
		strip.SetMaxTabWidth(bufferTabWidth) // a backstop: each label comes cut to fit (bufferTabLabel)
		strip.SetOnClose(h.closeBufferTab)
	}
	tabs := make([]widget.StripTab, len(h.buffers))
	for i, b := range h.buffers {
		tabs[i] = widget.StripTab{ID: strconv.Itoa(b.n), Label: h.bufferTabLabel(b, bufferTabWidth-2)} // 2: the padding
	}
	strip.SetTabs(tabs)
	if h.active != nil {
		strip.SetActive(strconv.Itoa(h.active.n))
	}
}

// bufferTabLabel is b's tab within w cells, its padding not counted (0: whole), told apart from
// every other buffer's: its file's name; its path where another buffer's file has that name; its
// workspace and path where another's has that path (in another workspace); a draft's hint
// (buffer.new's), or untitled, with its number while another draft has that name. [+] while modified. A name longer than w is cut in its middle,
// its start (the workspace, the folder) and its end (the file's name and kind) kept, and [+] after
// it; two cut alike take their buffer's number, so the tabs drawn still tell every buffer apart.
func (h *Host) bufferTabLabel(b *openBuffer, w int) string {
	label := h.cutTabName(b, "", w)
	if slices.ContainsFunc(h.buffers, func(o *openBuffer) bool { return o != b && h.cutTabName(o, "", w) == label }) {
		label = h.cutTabName(b, " #"+strconv.Itoa(b.n), w)
	}
	return label + bufferMarks(b)
}

// bufferMarks are b's marks after its name: [+] while modified, ! while its file changed or went
// on disk (diskwatch.go); "" for neither.
func bufferMarks(b *openBuffer) string {
	marks := ""
	if b.file.dirty {
		marks += "+"
	}
	if b.file.disk != diskSame {
		marks += "!"
	}
	if marks == "" {
		return ""
	}
	return " [" + marks + "]"
}

// cutTabName is b's name, then mark, cut in the middle to fit w cells with room for its marks
// (always, so a cut label does not change length as the buffer is edited); 0 leaves it whole.
func (h *Host) cutTabName(b *openBuffer, mark string, w int) string {
	if w <= 0 {
		return h.bufferTabName(b) + mark
	}
	room := w - len(" [+!]") - tuicore.StringWidth(mark)
	return elideMiddle(h.bufferTabName(b), room) + mark
}

// elideMiddle is s within w cells: whole if it fits, else its start and its longer end around an
// ellipsis.
func elideMiddle(s string, w int) string {
	if tuicore.StringWidth(s) <= w {
		return s
	}
	var cs []string
	for c := range tuicore.Graphemes(s) {
		cs = append(cs, c)
	}
	tailW := (w - 1) * 2 / 3
	headW := w - 1 - tailW
	head, used := "", 0
	for _, c := range cs {
		if cw := tuicore.StringWidth(c); used+cw <= headW {
			head, used = head+c, used+cw
			continue
		}
		break
	}
	tail, used := "", 0
	for i := len(cs) - 1; i >= 0; i-- {
		if cw := tuicore.StringWidth(cs[i]); used+cw <= tailW {
			tail, used = cs[i]+tail, used+cw
			continue
		}
		break
	}
	return head + "…" + tail
}

func (h *Host) bufferTabName(b *openBuffer) string {
	other := func(match func(o *openBuffer) bool) bool {
		return slices.ContainsFunc(h.buffers, func(o *openBuffer) bool { return o != b && match(o) })
	}
	if !b.file.open {
		name := b.file.name() // its hint, or untitled
		if other(func(o *openBuffer) bool { return !o.file.open && o.file.name() == name }) {
			return name + " " + strconv.Itoa(b.n)
		}
		return name
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

// bufferChord is H and L in Normal mode (bufferline's <S-h> and <S-l>, as in autovim): the buffer
// before or after the shown one, in the order the tabs show them, wrapping at the ends, in the GUI
// and in the TUI alike. It overrides vim's H and L (the screen's top and bottom line) in Normal mode
// only: Visual mode keeps them, and Insert mode, a text field or a terminal never sees them (the
// chord hook runs for the editor's keys, in Normal or Visual mode, with nothing pending).
func (h *Host) bufferChord(kc widget.KeyChord) bool {
	if kc.Mode != widget.ModeNormal || kc.Ctrl {
		return false
	}
	step := 0
	switch kc.Code {
	case 'H':
		step = -1
	case 'L':
		step = 1
	default:
		return false
	}
	n := len(h.buffers)
	if i := slices.Index(h.buffers, h.active); i >= 0 && n > 1 {
		h.showBuffer(h.buffers[(i+step+n)%n])
	}
	return true
}

// closeBufferTab is a press on a tab's ×: its buffer closed, asked about first when it is modified.
func (h *Host) closeBufferTab(id string) {
	if b := h.bufferOfTab(id); b != nil {
		h.closeBuffer(b)
	}
}

// showBuffer shows b, if it is still open, and gives the editor the keys.
func (h *Host) showBuffer(b *openBuffer) {
	if !slices.Contains(h.buffers, b) {
		return
	}
	h.activate(b)
	h.keep(h.p.Call("editor", "forceActiveFocus"))
	h.askDiskOnEnter(b)
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
		// File › Open, the file chosen in a buffer of its own, even over an empty one shown
		bufferTabRow("buffer.tabs.open", "&Open file in a new buffer…", h.openPickerNewBuffer),
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
