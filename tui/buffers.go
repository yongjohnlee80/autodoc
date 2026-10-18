package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// BUFFERS — the open files, one shown in the editor (ADR 1792076688).
//
// Each open file, and the untitled draft, is a buffer: its openedFile and its golib Buffer (the
// text, undo, marks and revision). The editor shows one, the active buffer; h.file points at the
// active buffer's openedFile, so everything that reads "the page" reads the active buffer's.
//
// OPENING ASKS NOTHING. Opening a file that is open shows its buffer; opening another adds a buffer.
// The buffer left keeps its unsaved edits, so there is nothing to ask about. The question moves to
// what still ends work: closing a buffer, reloading it, quitting (each modified buffer in turn).
//
// ASYNC WORK STAYS WITH ITS BUFFER. A load, a save and its conflict carry the buffer they started
// for (bufferTicket), and land in it whatever is active when they answer. A buffer not shown is
// edited through a headless editor of its own (editorFor), which runs the user's commands by the
// user's code.

// openBuffer is an open file in the editor, or the draft.
type openBuffer struct {
	n    int            // its number, as :b and the tabs show it; never reused in a run
	file openedFile     // ws, path, version, open, dirty, gen, derived, raw, then
	buf  *widget.Buffer // its text, history, marks and revision (golib)
	// ed is the buffer's own headless editor, made on first use (editorFor); nil until then
	ed *widget.EditorCore
	// table is a CSV or TSV buffer's Rendered view, made when it is first shown (tabledoc.go)
	table *widget.TableDocument
	// luaRev and luaGen are the Lua change event's baseline and debounce generation, per buffer
	luaRev, luaGen uint64
	// restored is the kept copy this buffer's text came from (recovery.go); pendingRestore, a copy
	// waiting for this buffer's load
	restored       *recoveryKey
	pendingRestore *recovered
	// draft is an untitled draft's recovery id (ownRecovery), made when it is first kept; "" until
	// then. Kept again, a restored draft's copy replaces the one it came from (keepRecovery)
	draft string
	// following the disk (diskwatch.go): its folder's watch and its file's name there; diskGen the
	// debounce of its checks; saving and recheck hold a check while a save is out; saves counts
	// the saves started, so a check that began before one is stale
	watch     *diskWatch
	watchName string
	diskGen   uint64
	saving    bool
	recheck   bool
	saves     uint64
}

// newBuffer is an empty buffer, added after the others. It is not shown until activate.
func (h *Host) newBuffer() *openBuffer {
	h.bufN++
	b := &openBuffer{n: h.bufN, buf: widget.NewBuffer()}
	b.file.gen = 1
	h.buffers = append(h.buffers, b)
	return b
}

// newBufferMax is the most text buffer.new takes, in bytes: a scaffold to start from, not a document
// to carry.
const newBufferMax = 256 << 10

// newBufferWith is buffer.new: a new buffer, shown, whatever the page shown is: an untitled draft
// holding text, modified when it holds any, the cursor at its start. name, when given, is the
// draft's hint: what it is called and read as ("public.users.sql" is SQL), and what Ctrl+S's
// picker starts with. Nothing is written: only that save names a path, and every other buffer
// keeps its text. A name with a path in it, or text past newBufferMax, is refused, and no buffer
// is added.
func (h *Host) newBufferWith(name, text string) error {
	name = strings.TrimSpace(name)
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("buffer.new: %q is a name, not a path: no /, \\ or ..", name)
	}
	if len(text) > newBufferMax {
		return fmt.Errorf("buffer.new: the text is %d KiB; at most %d KiB", (len(text)+1023)>>10, newBufferMax>>10)
	}
	b := h.newBuffer()
	b.file.hint = name
	h.activate(b) // readPage reads the hint
	if text != "" {
		// an edit, as typing is: the editor's textChanged marks the draft modified (edited), and
		// the editor's own :x sees it so
		if _, err := h.core.Replace(widget.TextRange{}, text); err != nil {
			h.dropBuffer(b)
			return fmt.Errorf("buffer.new: %w", err)
		}
		h.core.SetLine(0, 0)
	}
	h.keep(h.p.Call("editor", "forceActiveFocus"))
	return nil
}

// bufferOf is the open buffer of ref's file, or nil.
func (h *Host) bufferOf(ref fileRef) *openBuffer {
	for _, b := range h.buffers {
		if b.file.open && b.file.ws == ref.ws && b.file.path == ref.path {
			return b
		}
	}
	return nil
}

// bufferNumbered is buffer n, or nil.
func (h *Host) bufferNumbered(n int) *openBuffer {
	for _, b := range h.buffers {
		if b.n == n {
			return b
		}
	}
	return nil
}

// activate shows b in the editor. Not an edit: the editor's SetBuffer sends no textChanged, so b
// keeps the dirty state it had.
func (h *Host) activate(b *openBuffer) {
	if b == h.active {
		return
	}
	if h.active != nil {
		h.emitLua("leave", h.bufferInfo(h.active))
	}
	h.active, h.file = b, &b.file
	h.bufRecent = append([]*openBuffer{b}, slices.DeleteFunc(h.bufRecent, func(o *openBuffer) bool { return o == b })...)
	h.core.SetBuffer(b.buf)
	if b.ed != nil {
		b.ed.SetReadOnly(false) // the shown editor edits it now; its headless one rests
	}
	h.documentFor(b.file.ws, b.file.path, b.file.raw)
	h.syncPageWidth()
	h.page = h.takePage()
	h.readPage() // read-only, table editing, rendered view, syntax, the dirty title: b's
	h.showPage()
	h.htmlPaneNoteChanged()
	h.syncBuffers()
	h.emitLua("enter", h.bufferInfo(b))
}

// bufferTicket guards an answer for b: it holds while the session lasts, b is open, and b has not
// been loaded again since. Neither a switch of the active buffer nor of the workspace ends it, as
// ticket's TicketFile (h.file.gen) and TicketWorkspace (h.epoch) scopes would. load takes a new
// generation (a load); otherwise it only reads the current one (a save, a check).
func (h *Host) bufferTicket(b *openBuffer, load bool) Ticket {
	if load {
		b.file.gen++
	}
	gen, sess := b.file.gen, h.session.Gen()
	return Ticket{current: func() bool {
		return h.session.Gen() == sess && b.file.gen == gen && slices.Contains(h.buffers, b)
	}}
}

// editorFor is b's editor: the widget's when b is active, else b's headless one, made once with the
// widget's command registry and jump list, read-only for a derived document.
func (h *Host) editorFor(b *openBuffer) *widget.EditorCore {
	if b == h.active {
		return h.core
	}
	if b.ed == nil {
		b.ed = widget.NewEditorCore(widget.CoreBuffer(b.buf), widget.CoreCommands(h.core.Commands()))
		b.ed.SetJumps(h.jumps, h.jumpTo)
	}
	b.ed.SetReadOnly(b.file.derived != "")
	return b.ed
}

// bufferEdited is what an edit is to b, shown or not: unsaved, the buffer list's [+], and the
// debounced Lua change with b's own baseline. A headless edit calls it after its Batch: no
// textChanged reaches the host from a headless editor.
func (h *Host) bufferEdited(b *openBuffer) {
	if b == h.active {
		h.setDirty(true)
	} else {
		b.file.dirty = true
	}
	h.syncBuffers()
	h.changeSoonFor(b)
}

// closeBuffer asks about b's unsaved edits, then drops it and shows the most recently active other
// buffer; closing the last one leaves an empty draft.
func (h *Host) closeBuffer(b *openBuffer) {
	h.guard(b, "close it", func() {
		h.dropBuffer(b)
	})
}

// dropBuffer drops b, unasked: its edits are saved or discarded.
func (h *Host) dropBuffer(b *openBuffer) {
	h.emitLua("close", h.bufferInfo(b))
	h.unwatchFile(b)
	if b.ed != nil {
		b.ed.Detach()
		b.ed = nil
	}
	b.file.gen++ // an answer still coming for b finds it gone
	h.buffers = slices.DeleteFunc(h.buffers, func(o *openBuffer) bool { return o == b })
	h.bufRecent = slices.DeleteFunc(h.bufRecent, func(o *openBuffer) bool { return o == b })
	if h.active != b {
		h.syncBuffers()
		return
	}
	next := h.newDraftIfNone()
	if len(h.bufRecent) > 0 {
		next = h.bufRecent[0]
	}
	h.activate(next)
}

// newDraftIfNone is an empty draft buffer, made when no buffer is left; else the first buffer.
func (h *Host) newDraftIfNone() *openBuffer {
	if len(h.buffers) > 0 {
		return h.buffers[0]
	}
	return h.newBuffer()
}

// guardAll asks about each modified buffer in turn, each shown as it is asked about; Stay on any
// ends it, and then never runs. With none modified, then runs at once.
func (h *Host) guardAll(action string, then func()) {
	h.guardEach(slices.Clone(h.buffers), action, then)
}

func (h *Host) guardEach(left []*openBuffer, action string, then func()) {
	for len(left) > 0 && !left[0].file.dirty {
		left = left[1:]
	}
	if len(left) == 0 {
		then()
		return
	}
	b := left[0]
	// the next question waits for the next turn of the loop: the answer that runs this closes the
	// dialog, and the same dialog opened again in the same turn does not show
	h.guard(b, action, func() { h.p.Post(func() { h.guardEach(left[1:], action, then) }) })
}

// bufferInfo is b as Lua and the editor socket see it.
func (h *Host) bufferInfo(b *openBuffer) map[string]any {
	info := map[string]any{"buffer": b.n}
	if b.file.open {
		info["path"] = b.file.path
		if b.file.ws != "" {
			info["workspace"] = b.file.ws
		}
	}
	return info
}

// bufferLabel is b's name in the buffer list: its path, or untitled, with [+] while modified.
func bufferLabel(b *openBuffer) string {
	return fmt.Sprintf("%d %s", b.n, b.file.name()) + bufferMarks(b)
}

// syncBuffers tells the buffer list's surfaces (:b completion, the Buffers picker, the GUI's tabs)
// the buffers as they are now.
func (h *Host) syncBuffers() {
	if h.bufferPick != nil {
		h.bufferPick.setAll(h.bufferLabels())
	}
	h.syncBufferTabs()
}

// bufferLabels are the buffers' labels in opening order.
func (h *Host) bufferLabels() []string {
	out := make([]string, len(h.buffers))
	for i, b := range h.buffers {
		out[i] = bufferLabel(b)
	}
	return out
}
