package tui

import (
	"path"
	"slices"
	"strconv"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE BUFFER TABS — the open buffers on the GUI editor's title bar (golib's Editor.Tabs,
// docs/decisions/editor.md#switching-buffers): a tab for each buffer, in opening order, the shown
// one active. A press shows a buffer, and a press on its × closes it (asking first over unsaved
// changes); + makes a new, empty one. A tab's menu (≡, a right press) has New buffer, Open file in
// a new buffer… and Go to › every buffer, then Save, Close and Close others for the tab it was
// opened for, each asking about unsaved changes as the TUI's do. A tab is never wider than
// bufferTabWidth cells: a longer name is cut in its middle (bufferTabLabel), and a tab never
// changes width as tabs open and close or the window resizes (consistent, not re-fitted each
// time). The strip has the bar's room up to Raw | Rendered; tabs that do not fit scroll between ←
// and →. The TUI has no title bar: its editor has no strip (bufferStrip is nil there), and :b and
// the Buffers picker switch instead.

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
	labels := h.bufferTabLabels(bufferTabWidth - 2) // 2: the padding
	tabs := make([]widget.StripTab, len(h.buffers))
	for i, b := range h.buffers {
		tabs[i] = widget.StripTab{ID: strconv.Itoa(b.n), Label: labels[b]}
	}
	strip.SetTabs(tabs)
	if h.active != nil {
		strip.SetActive(strconv.Itoa(h.active.n))
	}
}

// bufferTabLabels are every buffer's tab within w cells, its padding not counted (0: whole), each told
// apart from every other buffer's: its file's name; its path where another buffer's file has that
// name; its workspace and path where another's has that path (in another workspace); a draft's hint
// (buffer.new's), or untitled, with its number while another draft has that name. [+] while
// modified. A name longer than w is cut in its middle, its start (the workspace, the folder) and its
// end (the file's name and kind) kept, and [+] after it; two cut alike take their buffer's number, so
// the tabs drawn still tell every buffer apart. They are resolved together, from facts counted once
// (bufferTabNames), so the tabs and SPC b's card cost one pass over the buffers a change.
func (h *Host) bufferTabLabels(w int) map[*openBuffer]string {
	if labelsResolved != nil {
		labelsResolved()
	}
	names := h.bufferTabNames()
	cut := make(map[*openBuffer]string, len(h.buffers))
	same := map[string]int{}
	for _, b := range h.buffers {
		cut[b] = cutTabName(names[b], "", w)
		same[cut[b]]++
	}
	out := make(map[*openBuffer]string, len(h.buffers))
	for _, b := range h.buffers {
		label := cut[b]
		if same[label] > 1 {
			label = cutTabName(names[b], " #"+strconv.Itoa(b.n), w)
		}
		out[b] = label + bufferMarks(b)
	}
	return out
}

// labelsResolved, when a test sets it, is told each pass bufferTabLabels makes over the buffers (the
// cells count them, bufferkeys_test.go); nil in AutoDoc.
var labelsResolved func()

// bufferTabLabel is b's tab within w cells (bufferTabLabels).
func (h *Host) bufferTabLabel(b *openBuffer, w int) string { return h.bufferTabLabels(w)[b] }

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

// cutTabName is name, then mark, cut in the middle to fit w cells with room for its marks (always,
// so a cut label does not change length as the buffer is edited); 0 leaves it whole.
func cutTabName(name, mark string, w int) string {
	if w <= 0 {
		return name + mark
	}
	room := w - len(" [+!]") - tuicore.StringWidth(mark)
	return elideMiddle(name, room) + mark
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

// bufferTabNames are the buffers' names, each told apart from the others' (bufferTabLabels' rules),
// from facts counted once: the drafts by name, the files by base name and by path.
func (h *Host) bufferTabNames() map[*openBuffer]string {
	drafts, bases, paths := map[string]int{}, map[string]int{}, map[string]int{}
	for _, b := range h.buffers {
		if !b.file.open {
			drafts[b.file.name()]++
			continue
		}
		bases[path.Base(b.file.path)]++
		paths[b.file.path]++
	}
	out := make(map[*openBuffer]string, len(h.buffers))
	for _, b := range h.buffers {
		switch base := path.Base(b.file.path); {
		case !b.file.open && drafts[b.file.name()] > 1:
			out[b] = b.file.name() + " " + strconv.Itoa(b.n)
		case !b.file.open:
			out[b] = b.file.name() // its hint, or untitled
		case bases[base] == 1:
			out[b] = base
		case paths[b.file.path] == 1 || b.file.ws == "":
			out[b] = b.file.path
		default:
			out[b] = b.file.ws + ": " + b.file.path
		}
	}
	return out
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

// bufferCardText is SPC b's card as the buffers are now: a row for each open buffer up to the 10th,
// its slot key and its tab's label (bufferTabLabels, as wide as the card allows: a longer one is cut
// in its middle, its file's name kept), and no row for a slot with no buffer; then the card's other
// keys, as cardText lists them. F1 lists the card's keys as declared (helpCards).
func (h *Host) bufferCardText() string {
	slot := map[string]Key{}
	var rest []Key
	for _, key := range h.keys.on(bufferCard) {
		if strings.HasPrefix(key.Run, "App.showBufferAt(") {
			slot[key.Run] = key
			continue
		}
		rest = append(rest, key)
	}
	var rows []string
	labels := map[int]map[*openBuffer]string{} // by width: one unless a scheme gives slot keys of other widths
	for i, b := range h.buffers[:min(len(h.buffers), bufferSlots)] {
		key, ok := slot[bufferSlotRun(i+1)]
		if !ok {
			continue // a test's catalog of features alone declares no buffer card
		}
		shown := h.keys.shown(key)
		w := cardBodyWidth - tuicore.StringWidth(shown) - 2
		if labels[w] == nil {
			labels[w] = h.bufferTabLabels(w)
		}
		rows = append(rows, shown+"  "+labels[w][b])
	}
	return strings.Join(append(rows, h.keys.cardText(nil, rest)), "\n")
}

// cardState is every card's body, as the document reads it: the catalog's (keyset.cardState), SPC
// b's as the buffers are now.
func (h *Host) cardState() map[string]any {
	m := h.keys.cardState()
	m[textSource(bufferCard)] = h.bufferCardText()
	return m
}

// showBufferAt is SPC b 1 to 9, and 0 for the 10th: the nth buffer in the order the tabs show
// them, H and L's. A place past the last buffer shows none.
func (h *Host) showBufferAt(n int) {
	if n >= 1 && n <= len(h.buffers) {
		h.showBuffer(h.buffers[n-1])
	}
}

// closeBeside is SPC b r (step 1) and SPC b l (step -1): the buffers to the right or the left of
// the shown one, in the tabs' order, closed nearest first, each as SPC b d closes it: a modified
// one is shown and asked about. A Stay stops the rest, the buffer asked about shown, as a Stay
// leaves it after Close others; else the shown one is shown again: a buffer asked about is shown
// from it, and closing that buffer shows the most recent other (dropBuffer), the shown one.
func (h *Host) closeBeside(step int) {
	i := slices.Index(h.buffers, h.active)
	if i < 0 {
		return
	}
	var side []*openBuffer
	for j := i + step; j >= 0 && j < len(h.buffers); j += step {
		side = append(side, h.buffers[j])
	}
	var next func(left []*openBuffer)
	next = func(left []*openBuffer) {
		for len(left) > 0 && !slices.Contains(h.buffers, left[0]) {
			left = left[1:] // closed meanwhile
		}
		if len(left) == 0 {
			return
		}
		b := left[0]
		h.guard(b, h.askBefore("close"), func() {
			h.dropBuffer(b)
			// the next question waits for the next turn of the loop, as guardEach's: the answer
			// that runs this closes the dialog, and the same dialog opened again in this turn does
			// not show
			h.p.Post(func() { next(left[1:]) })
		})
	}
	next(side)
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
// in a new buffer…, the editor's Layout › and Go to › every buffer; then, for its buffer, Save
// (while it is modified), Close and Close others.
func (h *Host) bufferTabRows(id string) []widget.MenuItemModel {
	goTo := make([]widget.MenuItemModel, 0, len(h.buffers))
	for _, o := range h.buffers {
		target := o
		goTo = append(goTo, bufferTabRow("buffer.tabs.goto."+strconv.Itoa(o.n), bufferLabel(o), func() { h.showBuffer(target) }))
	}
	sub := withLabelMessage(widget.NewSubmenu("buffer.tabs.goto", "Go to", goTo), "Go to")
	sub.Enabled = len(goTo) > 0
	rows := []widget.MenuItemModel{
		bufferTabRow("buffer.tabs.new", "&New buffer", h.newEmptyBuffer),
		// File › Open, the file chosen in a buffer of its own, even over an empty one shown
		bufferTabRow("buffer.tabs.open", "&Open file in a new buffer…", h.openPickerNewBuffer),
	}
	rows = append(append(rows, layoutRows(core{h}, "editor", bufferTabRow)...), sub)
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
	return withLabelMessage(row, label)
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
	h.guardEach(others, h.askBefore("close"), func() {
		for _, o := range others {
			if slices.Contains(h.buffers, o) {
				h.dropBuffer(o)
			}
		}
		h.activate(b)
	})
}
