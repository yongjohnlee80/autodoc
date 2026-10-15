package tui

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// THE NOTE IN THE EDITOR — opening, saving, and what is asked on the way.
//
// UNSAVED WORK IS NEVER LOST QUIETLY. Opening another file, switching workspace, reloading or
// quitting over unsaved edits asks first (save, discard, or stay), with Save focused. A save is
// written only over the version the file was read at (doc.write's condition), so a file that
// changed on disk since is never overwritten without asking: the conflict dialog offers keep,
// reload or overwrite.
//
// A load is off the loop, and applied only if it is still the latest open under the same workspace
// and connection: a slow load cannot replace a later one.
//
// A DERIVED DOCUMENT IS READ, NEVER WRITTEN. A PDF or a DOCX the daemon's build derives opens as
// its derived text in a read-only editor, badged with its kind ("[PDF · read-only]"): motions,
// find, copy and the outline work, edits and saves do not. File › Open with Default App opens the
// original with the desktop's default app.
//
// A FILE OUTSIDE EVERY WORKSPACE (ADR 1791430651) is opened by File › Open file… (SPC f) or made by
// File › New file… (SPC n): its workspace is "" and its path absolute, and the daemon reads and
// writes it with file.read and file.write, versions and conflicts as a workspace file's. A file
// inside a workspace's root opens as that workspace's (file.locate). An outside file stays open
// when the workspace changes, and has no search, links, backlinks or frontmatter check: the
// status line badges it.
//
// THE PAGE IS ALWAYS WRITABLE. With no file open, what is typed is an untitled draft: unsaved work
// like a file's, guarded the same way; saving it asks for its path in the new-file picker, creates
// the file with the draft's text, and opens it there, the cursor where it was.

type openedFile struct {
	ws      string // the workspace the file is in; "" for one outside every workspace, path absolute
	path    string
	version string // the version the editor's text was read or written at
	open    bool
	dirty   bool
	gen     uint64 // numbers the opens; the latest wins
	// derived is the kind of a derived document ("PDF"), open read-only; "" for a file of text
	derived string
	// raw is an HTML file opened Raw (ADR 1791474356): its own bytes, editable, read and written
	// through the *_raw verbs; false for one opened Simplified, as its derived text
	raw bool
	// then is what the unsaved question guards: run after save or discard, dropped by stay.
	then func()
}

// untitled is the draft's name wherever a file's path would show.
const untitled = "untitled"

// name is the file's path, or untitled for the draft.
func (n openedFile) name() string {
	if n.open {
		return n.path
	}
	return untitled
}

// outside reports whether the open file is outside every workspace.
func (n openedFile) outside() bool { return n.open && n.ws == "" }

// title is the file's name as the page and the status line show it: a derived document's carries
// its read-only badge, an outside file's that it is in no workspace.
func (n openedFile) title() string {
	badges := []string{}
	if n.outside() {
		badges = append(badges, outsideBadge)
	}
	if n.open && n.derived != "" {
		badges = append(badges, n.derived+" · read-only")
	}
	if len(badges) == 0 {
		return n.name()
	}
	return n.name() + "  [" + strings.Join(badges, " · ") + "]"
}

// outsideBadge marks a file outside every workspace; outsideLimits says what that costs.
const (
	outsideBadge  = "no workspace"
	outsideLimits = "it is in no workspace: no search, links, backlinks, frontmatter check or embeddings"
)

// readFile is doc.read of p in workspace ws, or file.read of the absolute p when ws is "".
// raw reads an HTML file's own bytes (doc.read_raw, file.read_raw) where the plain verbs give its
// derived text.
func (h *Host) readFile(ctx context.Context, ws, p string, raw bool) (any, error) {
	suffix := ""
	if raw {
		suffix = "_raw"
	}
	if ws == "" {
		res, err := h.call(ctx, "file.read"+suffix, p)
		return res, localOnly(err)
	}
	return h.call(ctx, "doc.read"+suffix, ws, p)
}

// writeFile is doc.write of p in workspace ws, or file.write of the absolute p when ws is "".
// raw writes an HTML file's own bytes (doc.write_raw, file.write_raw), which the plain verbs refuse.
func (h *Host) writeFile(ctx context.Context, ws, p string, content []byte, want string, raw bool) (any, error) {
	suffix := ""
	if raw {
		suffix = "_raw"
	}
	if ws == "" {
		res, err := h.call(ctx, "file.write"+suffix, p, content, want)
		return res, localOnly(err)
	}
	return h.call(ctx, "doc.write"+suffix, ws, p, content, want)
}

// guard runs then, asking first when b has unsaved changes. b is shown first, so the user sees what
// the question is about, and the answer's Save writes the shown editor's text.
func (h *Host) guard(b *openBuffer, action string, then func()) {
	if !b.file.dirty {
		then()
		return
	}
	h.activate(b)
	b.file.then = then
	h.set("App.unsavedQuestion", fmt.Sprintf("%s has unsaved changes. Save them before you %s?", b.file.name(), action))
	h.open("unsavedFile")
}

// unsaved answers the unsaved question.
func (h *Host) unsaved(answer string) {
	then := h.file.then
	h.file.then = nil
	h.closeDialog("unsavedFile")
	switch answer {
	case "save":
		if !h.file.open {
			h.nameDraft(then) // the draft has no path yet: name it, then go on
			return
		}
		h.write(h.active, h.core.Value(), h.file.version, then)
	case "discard":
		h.settleRecovery(h.active, h.file.ws, map[bool]string{true: h.file.path, false: ""}[h.file.open])
		h.setDirty(false)
		if then != nil {
			then()
		}
	default: // stay
		h.restoreSettled()
	}
}

// openPath opens a file of the workspace in use, asking first over unsaved changes.
func (h *Host) openPath(p string) { h.openRef(fileRef{h.ws, p}, true, nil) }

// openIn opens file p of workspace ws, entering ws first when it is not the one in use; either
// way, unsaved changes are asked about first.
func (h *Host) openIn(ws, p string) { h.openRef(fileRef{ws, p}, true, nil) }

// openRef opens ref, asking first over unsaved changes: in the workspace in use, outside every
// workspace (ws ""), or in another workspace, entering it. The document left behind goes on the
// history (SPC b) when remember is set; going back does not set it. proceed, when not nil, runs
// once the open goes ahead — after the unsaved question's Save or Discard, never after its Stay.
func (h *Host) openRef(ref fileRef, remember bool, proceed func()) {
	h.openRefTo(ref, remember, proceed, nil)
}

// openRefTo is openRef, the cursor landing as land says once the document is shown (navigation.go):
// land goes with this open and no other, so an open that doesn't go ahead drops it.
func (h *Host) openRefTo(ref fileRef, remember bool, proceed func(), land *landing) {
	// an HTML file asks how to open it before anything else: entering its workspace closes the
	// file open now, which Cancel must leave as it is
	if kind.RawText(ref.path) && !(h.file.open && h.file.ws == ref.ws && h.file.path == ref.path) {
		h.askHowToOpen(ref.path, func(raw bool) { h.openRefAs(ref, remember, proceed, &raw, land) })
		return
	}
	h.openRefAs(ref, remember, proceed, nil, land)
}

// openRefAs is openRef once an HTML file's way of opening is known: raw, or nil for a file that
// is no question (load asks for one that is).
func (h *Host) openRefAs(ref fileRef, remember bool, proceed func(), raw *bool, land *landing) {
	open := func(prev *widget.JumpPos) {
		if raw != nil {
			h.loadAs(ref.ws, ref.path, prev, *raw, land)
			return
		}
		h.loadTo(ref.ws, ref.path, prev, land)
	}
	goAhead := func() {
		if proceed != nil {
			proceed()
		}
	}
	leaving := func() *widget.JumpPos {
		if !remember || !h.file.open {
			return nil
		}
		at := h.core.JumpHere() // the place left, with the cursor and the view, for Ctrl+O
		return &at
	}
	// nothing is asked: the buffer left keeps its unsaved edits (buffers.go)
	prev := leaving()
	goAhead()
	if ref.ws != "" && ref.ws != h.ws {
		h.enter(ref.ws) // entering closes no buffer
	}
	if b := h.bufferOf(ref); b != nil {
		if prev != nil && prev.Doc != docKey(ref) {
			h.jumps.Push(*prev) // a jump: the place left
		}
		if raw != nil && *raw != b.file.raw {
			// an HTML file open one way, asked for the other: its one buffer is read again that way,
			// which replaces its text, so unsaved changes are asked about first. The new way is set
			// before it is shown, so its page is never switched on (or off) the old way first.
			h.guard(b, "reopen it", func() {
				b.file.raw = *raw
				h.activate(b)
				h.loadInto(b, ref.ws, ref.path, nil, *raw, land)
			})
			return
		}
		h.activate(b)
		h.say("opened " + ref.path)
		h.land(land, ref, h.core.Value()) // the document is open already: the open lands at once
		h.keep(h.p.Call("editor", "forceActiveFocus"))
		return
	}
	if h.file.open || h.file.dirty || h.core.Value() != "" {
		h.activate(h.newBuffer()) // the page shown is a file or a draft with text: the file gets its own
	}
	open(prev)
}

// reload reads the file again from disk, asking first over unsaved changes.
func (h *Host) reload() {
	if !h.file.open {
		return
	}
	h.guard(h.active, "reload it", func() { h.load(h.file.ws, h.file.path, nil) })
}

// load reads p of workspace ws ("" for the absolute path of a file outside every workspace) into
// the editor; once it is shown, prev (when not nil, and another document) goes on the history.
// Reading the open file again yields to a save that lands while it is read: the page is then what
// the disk holds, and the read is older.
//
// An HTML file it is not reading again asks first how to open it (ADR 1791474356): Simplified, its
// derived text read-only as any derived document, or Raw, its own bytes, editable. Read again (a
// reload, a conflict's), it keeps the way it was opened.
func (h *Host) load(ws, p string, prev *widget.JumpPos) { h.loadTo(ws, p, prev, nil) }

// loadTo is load, the cursor landing as land says once the document is shown.
func (h *Host) loadTo(ws, p string, prev *widget.JumpPos, land *landing) {
	again := h.file.open && h.file.ws == ws && h.file.path == p
	if !again && kind.RawText(p) {
		h.askHowToOpen(p, func(raw bool) { h.loadAs(ws, p, prev, raw, land) })
		return
	}
	h.loadAs(ws, p, prev, again && h.file.raw, land)
}

// htmlOpen is the open question: what opens the HTML file once it is answered.
type htmlOpen struct{ then func(raw bool) }

// openAsChoices are how a document with two views opens, as Preferences stores them: asked each
// time, or always one way. A document with two views is one whose source is itself text a person
// edits (kind.RawText: HTML today; other formats join as they gain a raw view).
var openAsChoices = []string{"prompt", "raw", "simplified"}

// openAsLabels are openAsChoices as the chooser shows them: raw is a document's source, to edit;
// simplified, its text, read-only.
var openAsLabels = []string{"ask each time", "raw", "simplified"}

// askHowToOpen asks how to open p, a document with two views (its source raw, or its text
// simplified), then runs then with the answer; Cancel runs nothing. Preferences may answer for
// the user: raw, or simplified, every time.
func (h *Host) askHowToOpen(p string, then func(raw bool)) {
	switch h.prefs.openAs { // Preferences: how a document with two views opens
	case "raw":
		then(true)
		return
	case "simplified":
		then(false)
		return
	}
	h.htmlAsk = &htmlOpen{then: then}
	h.set("App.htmlOpenQuestion", "Open "+filepath.Base(p)+" simplified, its text read-only, or raw, its HTML to edit?")
	h.open("openHTML")
}

// openHTMLAs is the open question's answer: "simplified", "raw", or "cancel", which leaves the page
// as it was.
func (h *Host) openHTMLAs(how string) {
	h.closeDialog("openHTML")
	ask := h.htmlAsk
	h.htmlAsk = nil
	if ask == nil {
		return
	}
	switch how {
	case "simplified":
		ask.then(false)
	case "raw":
		ask.then(true)
	default: // cancel
		h.restoreSettled()
	}
}

// loadAs is load, raw or not, into the active buffer.
func (h *Host) loadAs(ws, p string, prev *widget.JumpPos, raw bool, land *landing) {
	h.loadInto(h.active, ws, p, prev, raw, land)
}

// loadInto reads p into b off the loop; the answer lands in b, active or not, under b's own ticket:
// a switch of buffer or workspace meanwhile does not drop it.
func (h *Host) loadInto(b *openBuffer, ws, p string, prev *widget.JumpPos, raw bool, land *landing) {
	tk := h.bufferTicket(b, true)
	again, read := b.file.open && b.file.ws == ws && b.file.path == p, b.file.version
	type answer struct {
		content, version string
		err              error
	}
	h.say("opening " + p + "…")
	do(h, func(ctx context.Context) answer {
		res, err := h.readFile(ctx, ws, p, raw)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		b, _ := m["content"].([]byte)
		return answer{content: string(b), version: str(m, "version")}
	}, func(a answer) {
		if !tk.Current() {
			return
		}
		if again && b.file.version != read {
			h.say("kept " + p + " as it was saved while it was read")
			return
		}
		if a.err != nil {
			h.failed("open "+p, a.err)
			h.missedLanding(land, a.err)
			if h.restoreOpenFailed(b, ws, p) { // a kept copy's file gone: its text becomes a draft
				return
			}
			if !b.file.open && !b.file.dirty && b.buf != nil && b.buf.Value() == "" && len(h.buffers) > 1 {
				h.dropBuffer(b) // the buffer made for this file, never loaded: the page goes back
			}
			return
		}
		if prev != nil && prev.Doc != docKey(fileRef{ws, p}) {
			h.jumps.Push(*prev) // a jump: the place left, once the new document is shown
		}
		h.show(b, ws, p, a.content, a.version, raw) // the recent files note it (recent.go: Shown)
		if b == h.active {
			h.land(land, fileRef{ws, p}, a.content)
		}
		h.say("opened " + p)
		if ws == "" {
			h.notify(p + ": " + outsideLimits)
		}
		h.keep(h.p.Call("editor", "forceActiveFocus"))
	})
}

// show puts a file's text in buffer b, clean: p of workspace ws, or outside every one (""). b shown,
// the page follows it; not shown, only the buffer changes, and the page is set up when it is.
func (h *Host) show(b *openBuffer, ws, p, content, version string, raw bool) {
	b.file.ws, b.file.path, b.file.version, b.file.open, b.file.dirty = ws, p, version, true, false
	b.file.raw = raw
	b.buf.SetName(docKey(fileRef{ws, p}))
	if b != h.active {
		h.editorFor(b).SetValue(content) // a headless editor's: no widget, no textChanged
		h.syncBuffers()
		h.emitOpenFor(b)
		h.applyPendingRestore(b, ws, p)
		return
	}
	h.documentFor(ws, p, raw)  // before the text: a page shown for the last file must not get this one's
	h.editor.SetValue(content) // reports no textChanged: only typing does
	h.syncPageWidth()
	h.page = h.takePage() // readPage's consumers read it
	h.readPage()
	h.showPage() // the relations are read for it (relations.go: Shown)
	h.htmlPaneNoteChanged()
	h.syncBuffers()
	// a script hears the file once it is in (luaevents.go); before a restored copy, which is an edit
	// of it: readPage again (registrations arriving) is no new page
	h.emitOpenFor(b)
	h.applyPendingRestore(b, ws, p)
}

// readPage decides how the open page reads its file, the one place that does: on every open, and
// again whenever the daemon's registrations arrive, which a read may have beaten.
//
//   - A derived format's file is a derived document, read-only and badged, whatever the registrations
//     say or whether they have come: no build reads one but by its deriver, and none writes one.
//
//   - Its highlighting, outline and frontmatter check follow the daemon's kinds as last known: a
//     registered file's change from Markdown to plain text when they come.
//
//   - Its Rendered view, where the editor has one, only for a file read as Markdown.
//
// Unsaved text stays as it is: only how it is read changes.
func (h *Host) readPage() {
	p := h.file.path
	h.file.derived = ""
	if kind.Of(p, nil) == kind.Derived && !h.file.raw {
		h.file.derived = kind.Label(p)
	}
	h.core.SetReadOnly(h.file.derived != "")
	k := h.resolver().Of(p) // the daemon's registrations: it is the one indexing
	// Tab walks a Markdown table's cells (the draft is Markdown too)
	h.core.SetTableEditing(k == kind.Markdown || !h.file.open)
	// a raw HTML file's Rendered view is its page (documentFor), where there is one: the GUI's
	h.setRendered(k == kind.Markdown || h.file.derived != "" || h.file.raw && h.native())
	switch {
	case h.file.raw:
		h.set("App.syntaxDefinition", "Plain text")
		h.setDirty(h.file.dirty)
		h.validateSoon(validateDelay)
		return
	}
	if h.file.derived == "" {
		if d, ok := h.p.SourceLanguages().DefinitionForFileName(p); ok && !d.DocumentAdapter {
			h.set("App.syntaxDefinition", d.Name)
			h.setDirty(h.file.dirty)
			h.validateSoon(validateDelay)
			return
		}
	}
	h.set("App.syntaxDefinition", syntaxOf.Of(k))
	h.setDirty(h.file.dirty) // the status line's title carries the badge
	h.validateSoon(validateDelay)
}

// syntaxOf is the editor's syntax for a kind, where no source language claims the file's name
// (kind.Table: a kind added is a kind given its syntax).
var syntaxOf = kind.NewTable(map[kind.Kind]string{
	kind.Markdown:   "Markdown",
	kind.Derived:    "Markdown",
	kind.YAML:       "YAML",
	kind.Text:       "Plain text",
	kind.Registered: "Plain text",
})

// resolver is how the host reads a path's kind: the daemon's registrations, as last known, and the
// workspace's text extensions.
func (h *Host) resolver() kind.Resolver {
	return kind.Resolver{Registrations: h.kinds, Text: h.textExtensions}
}

// setRendered gives the editor its Rendered view, or withholds it. The view draws Markdown, so
// only a page read as Markdown has one: a note, a derived document (its text is Markdown), or the
// untitled draft. Code, YAML and plain text stay Raw, and the key that switches views does nothing
// there. Called at each change of document: load (through readPage) and closeFile, the two places
// that set the editor's text. Only an editor with a Rendered view answers (the GUI's).
func (h *Host) setRendered(on bool) {
	if rs, ok := h.editor.(renderedSwitch); ok {
		rs.SetRenderedEnabled(on)
	}
}

// recheckFile checks every open buffer's file against the disk on a new connection, which may have
// missed a change (recheckBuffer).
func (h *Host) recheckFile() {
	for _, b := range slices.Clone(h.buffers) {
		if b.file.open {
			h.recheckBuffer(b)
		}
	}
}

// recheckBuffer checks b's file against the disk: kept as it is at the version it was read at; read
// again, the cursor where it was, when it changed and has no unsaved changes (one that has finds
// out at its save, as a conflict); when it is gone, closed, after asking over unsaved changes, whose
// save writes it anew. A save that lands while it reads moves b's version on, and the read, older,
// is dropped.
func (h *Host) recheckBuffer(b *openBuffer) {
	tk, ws, p, read, raw := h.bufferTicket(b, false), b.file.ws, b.file.path, b.file.version, b.file.raw
	where := "the workspace"
	if ws == "" {
		where = "disk"
	}
	type answer struct {
		content, version string
		err              error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.readFile(ctx, ws, p, raw)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		c, _ := m["content"].([]byte)
		return answer{content: string(c), version: str(m, "version")}
	}, func(a answer) {
		if !tk.Current() || !b.file.open || b.file.version != read {
			return
		}
		switch {
		case code(a.err) == rpc.CodeNotFound, code(a.err) == golibrpc.CodeInvalidParams:
			if !b.file.dirty {
				h.dropBuffer(b)
				h.notify(p + " is gone from " + where + ": closed")
				return
			}
			b.file.version = "" // gone: a save writes it anew
			h.guard(b, "close it: it is gone from "+where, func() { h.dropBuffer(b) })
		case a.err != nil:
			h.failed("check "+p, a.err)
		case a.version == b.file.version:
		case b.file.dirty:
			h.notify(p + " changed on disk while the backend was away: a save asks before it overwrites it")
		default:
			row, col := h.editorFor(b).Line()
			h.show(b, ws, p, a.content, a.version, raw)
			h.editorFor(b).SetLine(row, col)
			h.notify("read " + p + " again: it changed on disk while the backend was away")
		}
	})
}

// closeFile empties the active buffer: no file is open in it, and it is a new draft.
func (h *Host) closeFile() {
	h.file.gen++
	h.documentFor("", "", false)
	h.editor.SetValue("")
	h.syncPageWidth()
	*h.file = openedFile{gen: h.file.gen}
	h.core.SetJumpDoc("")
	h.core.SetReadOnly(false)
	h.set("App.syntaxDefinition", "Markdown")
	h.setRendered(true) // the draft is Markdown, whatever the file before it was
	h.set("App.statusCenter", "")
	h.clearDiagnostics()
	h.showPage()
	h.htmlPaneNoteChanged()
	h.syncBuffers()
	h.emitOpenFor(h.active) // the draft is the page now (luaevents.go)
}

// edited is the editor's text changing: typed, so the file (or the draft) has unsaved changes.
func (h *Host) edited() {
	h.setDirty(true)
	h.syncBuffers()
	h.pageVersion++ // an edit: the next page taken carries it (Page.Version)
	// a check or a feed the last pause scheduled is of text this keystroke changed: the next
	// pause schedules them again, from this keystroke
	h.fmGen++
	h.pageSoon()
	h.syncPageWidth()         // a line count with another number of digits widens the gutter
	h.changeSoonFor(h.active) // a script's change handlers, after the pause (luaevents.go)
}

func (h *Host) setDirty(v bool) {
	h.file.dirty = v
	mark := ""
	if v {
		mark = " [+]"
	}
	if h.file.open || v {
		h.set("App.statusCenter", h.file.title()+mark)
	} else {
		h.set("App.statusCenter", "")
	}
}

// syncMode brings the status line's mode up to date with the editor's.
func (h *Host) syncMode() {
	h.setWhere(h.where)
	h.emitMode() // luaevents.go
}

// save writes the file at the version it was read at; the draft is named first.
func (h *Host) save() {
	if !h.file.open {
		h.nameDraft(nil)
		return
	}
	if h.file.derived != "" {
		h.notify(h.file.path + " is read-only: its text is derived from the " + h.file.derived + "; File › Open with Default App opens the original")
		return
	}
	h.write(h.active, h.core.Value(), h.file.version, nil)
}

// nameDraft opens the new-file picker to save the draft under a path; then runs once it is saved
// (a guarded open or switch), and is dropped when the picker is closed without one.
func (h *Host) nameDraft(then func()) {
	h.draft = &draftSave{then: then, b: h.active}
	h.set("App.fileNameError", draftHelp)
	h.setField("App.newFilePath", "")
	h.newPick.setAll(h.files.Paths)
	h.newPick.filter("")
	h.open("fileName")
}

// draftSave is a draft being named: what runs once it is saved, and the buffer it is.
type draftSave struct {
	then func()
	b    *openBuffer
}

// draftHelp is the new-file picker's line when it names the draft.
const draftHelp = "save the draft: a path in the workspace; .md is added when it has none · Enter on a file takes its folder"

// cursorBytes is the editor's cursor as a byte offset of its text, for the file the draft becomes.
func (h *Host) cursorBytes() int { return cursorBytesOf(h.core) }

// cursorBytesOf is an editor's cursor as a byte offset of its text.
func cursorBytesOf(ed *widget.EditorCore) int {
	row, col := ed.Line()
	lines := ed.Lines()
	at := 0
	for i := 0; i < row && i < len(lines); i++ {
		at += len(lines[i]) + 1
	}
	if row < len(lines) {
		n := 0
		for c := range tuicore.Graphemes(lines[row]) {
			if n == col {
				break
			}
			at += len(c)
			n++
		}
	}
	return at
}

// write writes content over the version want, then runs after. A stale version opens the conflict
// dialog. A write that landed before a follow-up failed (Committed) is read back: when the disk
// holds what was written, its version is adopted; otherwise someone wrote after it, which is a
// conflict. It is never sent again blindly.
func (h *Host) write(b *openBuffer, content, want string, after func()) {
	tk, ws, p, raw := h.bufferTicket(b, false), b.file.ws, b.file.path, b.file.raw
	type answer struct {
		version string
		err     error
		readErr error // the read-back after Committed
		same    bool  // the read-back holds what was written
	}
	h.say("saving " + p + "…")
	do(h, func(ctx context.Context) answer {
		res, err := h.writeFile(ctx, ws, p, []byte(content), want, raw)
		if err == nil {
			return answer{version: str(asMap(res), "version")}
		}
		if code(err) != rpc.CodeCommitted {
			return answer{err: err}
		}
		back, rerr := h.readFile(ctx, ws, p, raw)
		if rerr != nil {
			return answer{err: err, readErr: rerr}
		}
		m := asMap(back)
		b, _ := m["content"].([]byte)
		return answer{err: err, version: str(m, "version"), same: string(b) == content}
	}, func(a answer) {
		if !tk.Current() {
			return
		}
		switch {
		case a.err == nil, code(a.err) == rpc.CodeCommitted && a.readErr == nil && a.same:
			b.file.version = a.version
			// typing during the save leaves the file unsaved: what is on disk is what was written
			newer := b.buf.Value() != content
			if !newer {
				b.buf.MarkSaved() // the command line's x reads it
				h.settleRecovery(b, ws, p)
			}
			if b == h.active {
				h.setDirty(newer)
			} else {
				b.file.dirty = newer
			}
			h.syncBuffers()
			h.notify("saved " + p)
			if !newer {
				h.reloadOnSave(ws, p) // the user's own script: loaded again at once
			}
			if !newer {
				h.emitLua("save", h.bufferInfo(b))
			}
			switch {
			case after == nil:
			case newer:
				// what the save guarded (a close, a quit) would drop the newer edit: ask again, over b
				h.activate(b)
				b.file.then = after
				h.set("App.unsavedQuestion", fmt.Sprintf("%s changed again while it was saved. Save the newer changes first?", p))
				h.open("unsavedFile")
			default:
				after()
			}
		case code(a.err) == rpc.CodeCommitted && a.readErr != nil:
			h.notify("saved " + p + ", but it could not be read back: " + wireMessage(a.readErr) + " — reload before saving again")
		case code(a.err) == rpc.CodeConflict, code(a.err) == rpc.CodeCommitted:
			h.activate(b) // the conflict is b's: its answers act on the buffer shown
			h.set("App.conflictQuestion", fmt.Sprintf("%s changed on disk since you opened it. Keep editing, reload the disk's version (your changes are lost), or overwrite it with yours?", p))
			h.open("fileConflict")
		case code(a.err) == rpc.CodeNotFound:
			h.notify(p + " is gone from disk: File › New file to write it again")
		default:
			h.failed("save "+p, a.err)
		}
	})
}

// conflict answers the conflict dialog.
func (h *Host) conflict(answer string) {
	h.closeDialog("fileConflict")
	switch answer {
	case "reload":
		// the file stays unsaved until the disk's version is in the editor: a failed read keeps the
		// edits guarded
		h.load(h.file.ws, h.file.path, nil)
	case "overwrite":
		h.overwrite()
	default:
		h.notify("kept your changes; the disk's version is newer")
	}
}

// overwrite writes the editor's text over whatever version is on disk now: it reads the version
// first, so the write is still conditional (a third writer in between is still a conflict).
func (h *Host) overwrite() {
	b := h.active
	tk, ws, p, raw := h.bufferTicket(b, false), b.file.ws, b.file.path, b.file.raw
	type answer struct {
		version string
		gone    bool
		err     error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.readFile(ctx, ws, p, raw)
		if code(err) == rpc.CodeNotFound {
			return answer{gone: true}
		}
		if err != nil {
			return answer{err: err}
		}
		return answer{version: str(asMap(res), "version")}
	}, func(a answer) {
		if !tk.Current() {
			return
		}
		switch {
		case a.err != nil:
			h.failed("overwrite "+p, a.err)
		default:
			// gone: a create ("") writes it back
			h.write(b, h.editorFor(b).Value(), a.version, nil)
		}
	})
}

// createFile creates a file at name (".md" added when it has no extension) and opens it, closing
// the picker (Enter in its path field is not its Create button): empty, or holding the draft when it
// is the draft being named. A path that exists, or is not a file, asks again with the reason.
func (h *Host) createFile(name string) {
	h.closeDialog("fileName")
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	if name == "" {
		h.set("App.fileNameError", "a name is required")
		h.open("fileName")
		return
	}
	if path.Ext(name) == "" {
		name += ".md"
	}
	ds := h.draft // the naming this create answers; a later nameDraft replaces h.draft; nil for New file
	if ds == nil {
		h.createEmpty(name)
		return
	}
	b := ds.b
	content := []byte(h.editorFor(b).Value())
	restored := b.restored // the kept copy the draft's text came from, if any (recovery.go)
	ws, tk := h.ws, h.bufferTicket(b, false)
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "doc.write", ws, name, content, "")
		return err
	}, func(err error) {
		if !tk.Current() {
			if err == nil || code(err) == rpc.CodeCommitted {
				h.notify("saved the draft as " + name) // its buffer was closed meanwhile; the file is on disk
			}
			return
		}
		if err != nil && code(err) != rpc.CodeCommitted {
			reason := wireMessage(err)
			if code(err) == rpc.CodeConflict {
				reason = name + " exists: pick another name, or open it"
			}
			h.set("App.fileNameError", reason)
			h.open("fileName")
			return
		}
		if h.draft == ds {
			h.draft = nil // a newer draft's picker, opened while this write was in flight, stays
		}
		// the draft's text is on disk now: the copy it was restored from goes (not another draft's,
		// which Later kept for the next start)
		if restored != nil {
			h.dropRecovery(restored.ws, restored.path)
			b.restored = nil
		}
		h.settleRecovery(b, ws, "")
		land := &landing{at: cursorBytesOf(h.editorFor(b))} // the cursor where it was in the draft
		b.file.dirty = false                                // written: the load below finds it saved
		h.loadInto(b, ws, name, nil, false, land)
		h.listFiles()
		h.loadWorkspaces() // the explorer lists it
		if ds.then != nil {
			ds.then()
		}
	})
}

// createEmpty is File › New file: an empty file at name, opened in a buffer of its own.
func (h *Host) createEmpty(name string) {
	ws, tk := h.ws, h.ticket(nil, TicketWorkspace)
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "doc.write", ws, name, []byte{}, "")
		return err
	}, func(err error) {
		if !tk.Current() {
			return
		}
		if err != nil && code(err) != rpc.CodeCommitted {
			reason := wireMessage(err)
			if code(err) == rpc.CodeConflict {
				reason = name + " exists: pick another name, or open it"
			}
			h.set("App.fileNameError", reason)
			h.open("fileName")
			return
		}
		h.openIn(ws, name)
		h.listFiles()
		h.loadWorkspaces() // the explorer lists it
	})
}

// quit quits, asking first over unsaved changes: Save (then quit once it is written), Discard, or
// Stay. Every way out comes here: File › Exit, Ctrl+Q, SPC Q, the command line's q, the too-small
// screen's Quit and the GUI window's close button (WithQuit).
func (h *Host) quit() { h.guardAll("quit", h.p.Quit) }
